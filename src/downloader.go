package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"go.senan.xyz/taglib"
)

type ConfigMisc struct {
	CompleteDir            string `json:"complete_dir"`
	EnableTVSorting        bool   `json:"enable_tv_sorting"`
	EnableMovieSorting     bool   `json:"enable_movie_sorting"`
	PreCheck               bool   `json:"pre_check"`
	HistoryRetention       string `json:"history_retention"`
	HistoryRetentionOption string `json:"history_retention_option"`
}

type ConfigCategory struct {
	Name     string `json:"name"`
	Pp       string `json:"pp"`
	Script   string `json:"script"`
	Dir      string `json:"dir"`
	Priority int    `json:"priority"`
}

type Config struct {
	Misc       ConfigMisc       `json:"misc"`
	Categories []ConfigCategory `json:"categories"`
	Sorters    []interface{}    `json:"sorters"`
}

type ConfigResponse struct {
	Config Config `json:"config"`
}

type File struct {
	Id           int
	Name         string
	Index        string
	mediaNumber  string
	isrc         string
	duration     int64
	DownloadLink string
	completed    bool
}

type Download struct {
	Id            string
	Source        string
	Artist        string
	Album         string
	Comment       string
	Genre         string
	Year          string
	UPC           string
	CoverUrl      string
	numTracks     int
	mediaCount    int
	label         string
	downloaded    int
	skippedTracks int
	previewTracks int
	FileName      string
	Quality       string
	Files         []File
}

var Downloads map[string]*Download = make(map[string]*Download)

func handleDownloaderRequest(w http.ResponseWriter, r *http.Request) {
	var queryApiKey string = r.URL.Query().Get("apikey")
	if ApiKey != queryApiKey {
		w.Write([]byte("error: API Key Incorrect"))
		return
	}
	switch query := r.URL.Query().Get("mode"); query {
	case "get_config":
		get_config(w, *r.URL)
	case "version":
		version(w, *r.URL)
	case "addfile":
		addfile(w, r)
	case "queue":
		queue(w, r)
	case "history":
		history(w, r)
	default:
		fmt.Println("Downloader unknown request:")
		fmt.Println(r.Method)
		fmt.Println(r.URL.String())
		fmt.Println(r.Header)
		buffer := make([]byte, 100)
		for {
			n, err := r.Body.Read(buffer)
			fmt.Printf("%q\n", buffer[:n])
			if err == io.EOF {
				break
			}
		}
		w.Write([]byte("Request received!"))
	}
}

func handleSimpleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("error: missing id parameter"))
		return
	}

	quality := r.URL.Query().Get("quality")
	if quality == "" {
		quality = QualityId
	}
	artist := r.URL.Query().Get("artist")
	album := r.URL.Query().Get("album")
	// Heal accents sent as Latin-1 instead of UTF-8 (see fixEncoding).
	artist = fixEncoding(artist)
	album = fixEncoding(album)

	fmt.Println("Triggering download for ID:", id, "Quality:", quality, "Artist:", artist, "Album:", album)

	// If download already exists and is not finished, ignore
	if d, ok := Downloads[id]; ok && d.downloaded != -1 && d.downloaded < d.numTracks {
		_, _ = w.Write([]byte(`{"status": "already_queued"}`))
		return
	}

	go func() {
		version := ""
		numTracks := 0

		if r.URL.Query().Get("source") != "deezer" && artist != "" && album != "" {
			albumJSON, err := qobuzSearchAlbum(id, artist, album)
			if err == nil {
				artist = gjson.Get(albumJSON, "artist.name").String()
				album = gjson.Get(albumJSON, "title").String()
				version = gjson.Get(albumJSON, "version").String()
				numTracks = int(gjson.Get(albumJSON, "tracks_count").Int())
			} else if Debug {
				fmt.Println("Qobuz album search failed:", err)
			}
		}

		filename := sanitizeFilename(artist + " - " + album)
		if version != "" && version != "null" {
			filename += " (" + version + ")"
		}
		if filename == sanitizeFilename(" - ") {
			filename = id
		}

		generateDownloadForSource(filename, id, numTracks, quality, artist, album, r.URL.Query().Get("source"))
		if Downloads[id] != nil && Downloads[id].downloaded != -1 {
			startDownload(id)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status": "queued", "id": "` + id + `"}`))
}

func get_config(w http.ResponseWriter, u url.URL) {
	resp := ConfigResponse{
		Config: Config{
			Misc: ConfigMisc{
				CompleteDir:            filepath.Join(DownloadPath, "complete"),
				EnableTVSorting:        false,
				EnableMovieSorting:     false,
				PreCheck:               false,
				HistoryRetention:       "",
				HistoryRetentionOption: "all",
			},
			Categories: []ConfigCategory{
				{
					Name:     "music",
					Pp:       "",
					Script:   "Default",
					Dir:      filepath.Join(DownloadPath, "incomplete", "music"),
					Priority: -100,
				},
			},
			Sorters: []interface{}{},
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func version(w http.ResponseWriter, u url.URL) {
	w.Write([]byte(`{
 	    "version": "4.5.1"
 	}`))
}

func addfile(w http.ResponseWriter, r *http.Request) {
	//extract filename, QobuzId and number of tracks
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Println("/downloader/api/addfile Failed to read body:")
		fmt.Println(err)
		return
	}
	bodyStr := string(body)

	reName := regexp.MustCompile(`filename="?([^"]+)\.nzb"?`)

	var filename string
	if nameMatch := reName.FindStringSubmatch(bodyStr); len(nameMatch) > 1 {
		filename = nameMatch[1]
	}
	if filename == "" {
		filename = "download"
	}
	filename = sanitizeFilename(filename)

	// Extract metadata from NZB XML comments, which fakenzb writes in a FIXED
	// order: QobuzId, NumTracks, Quality, Artist, Album.
	// QobuzId may be the ALPHANUMERIC catalog id (e.g. myy231ew9yh9b), so
	// comments must be read by POSITION — the old "numeric first" heuristic
	// misparsed every alphanumeric id (id became "artist", track count became
	// "id"), producing empty downloads with garbage folder names.
	reComment := regexp.MustCompile(`<!--\s+([^<]+?)\s+-->`)
	var Id string
	var NumTracks int
	var quality string
	var artist string
	var album string
	var source string
	for i, m := range reComment.FindAllStringSubmatch(bodyStr, -1) {
		if i > 5 {
			break
		}
		val := strings.TrimSpace(m[1])
		switch i {
		case 0:
			Id = val
		case 1:
			NumTracks, _ = strconv.Atoi(val)
		case 2:
			quality = val
		case 3:
			artist = val
		case 4:
			album = val
		case 5:
			source = val
		}
	}

	if Id == "" || NumTracks == 0 {
		fmt.Println("/downloader/api/addfile Failed to find ID or NumTracks in body")
		return
	}

	fmt.Println("Adding download:", filename, "ID:", Id, "Tracks:", NumTracks, "Quality:", quality, "Artist:", artist, "Album:", album)
	// generateDownload resolves metadata and one stream URL per track — slow
	// when the proxy fallback is used (an HTTP round trip per track). Run it in
	// the background so the client's "Download started" ack arrives immediately;
	// the actual file transfer happens in startDownload regardless.
	go func() {
		generateDownloadForSource(filename, Id, NumTracks, quality, artist, album, source)
		if Downloads[Id] != nil && Downloads[Id].downloaded != -1 {
			startDownload(Id)
		}
	}()

	//send response using QobuzId as nzo_id
	w.Write([]byte("{\n" +
		"\"status\": true,\n" +
		"\"nzo_ids\": [\"SABnzbd_nzo_" + Id + "\"]\n" +
		"}"))
}

func generateDownload(filename string, Id string, numTracks int, qualityParam string, artistHint string, albumHint string) {
	generateDownloadForSource(filename, Id, numTracks, qualityParam, artistHint, albumHint, Provider)
}

func generateDownloadForSource(filename string, Id string, numTracks int, qualityParam string, artistHint string, albumHint string, source string) {
	if source == "deezer" {
		generateDeezerDownload(filename, Id, qualityParam)
		return
	}
	if source == "tidal" {
		generateTidalDownload(filename, Id, qualityParam)
		return
	}
	// Heal accents that may have arrived as Latin-1 instead of UTF-8.
	artistHint = fixEncoding(artistHint)
	albumHint = fixEncoding(albumHint)
	var download Download
	download.Id = Id
	download.Source = source
	download.numTracks = numTracks
	download.FileName = filename
	download.downloaded = 0
	quality := qualityParam
	if quality == "" {
		quality = QualityId
	}
	download.Quality = quality

	var bodyStr string
	var tracksJSON string
	var albumItems gjson.Result
	if QobuzToken != "" {
		// Preferred: album/get returns the authoritative, COMPLETE track list.
		// track/search only surfaces a subset of an album (e.g. 19 of 24 tracks),
		// which was the regression that made multi-track albums "fail to download".
		// album/get needs the ALPHANUMERIC catalog id (the search result's "id",
		// e.g. d8xwaf7svx5m8), NOT the numeric qobuz_id, and must not be signed —
		// same pattern as monochrome / qobuz-tidal-eclipse.
		if artistHint != "" && albumHint != "" {
			if albumJSON, err := qobuzSearchAlbum(Id, artistHint, albumHint); err == nil {
				bodyStr = albumJSON
				if catalogID := gjson.Get(albumJSON, "id").String(); catalogID != "" {
					if fullJSON, aerr := qobuzGetAlbum(catalogID); aerr == nil {
						bodyStr = fullJSON
						albumItems = gjson.Get(bodyStr, "tracks.items")
						if Debug {
							fmt.Println("Qobuz album/get found", len(albumItems.Array()), "tracks for album", Id)
						}
					} else if Debug {
						fmt.Println("Qobuz album/get failed:", aerr)
					}
				}
			} else if Debug {
				fmt.Println("Qobuz album search failed:", err)
			}
		}

		// Fallback when album/get isn't usable: enumerate via track/search.
		if !albumItems.Exists() {
			if tracksStr, terr := qobuzGetTracksByAlbum(Id, artistHint, albumHint); terr == nil {
				tracksJSON = tracksStr
				if Debug {
					fmt.Println("Qobuz track search found", len(gjson.Parse(tracksJSON).Array()), "tracks for album", Id)
				}
			} else if Debug {
				fmt.Println("Qobuz track search failed:", terr)
			}
		}
	}

	// Navidrums fallback: unlike the official API its account streams full-length
	// audio, so it is both a metadata source and the primary stream backend.
	// /api/get-album only accepts the ALPHANUMERIC catalog id, so resolve the
	// numeric qobuz_id Lidarr passes back through a search first.
	if !albumItems.Exists() && tracksJSON == "" && NavLink != "" {
		if albumJSON, nerr := navGetAlbumByHints(Id, artistHint, albumHint); nerr == nil {
			bodyStr = albumJSON
			albumItems = gjson.Get(bodyStr, "data.tracks.items")
			if Debug {
				fmt.Println("Navidrums album/get found", len(albumItems.Array()), "tracks for album", Id)
			}
		} else if Debug {
			fmt.Println("Navidrums album/get failed:", nerr)
		}
	}

	if artistHint != "" {
		download.Artist = artistHint
	} else {
		download.Artist = gjson.Get(bodyStr, "artist.name").String()
	}
	download.Album = gjson.Get(bodyStr, "title").String()
	if download.Album == "" {
		download.Album = albumHint
	}
	download.Comment = gjson.Get(bodyStr, "version").String()
	releaseDate := gjson.Get(bodyStr, "release_date_original").String()
	download.Genre = gjson.Get(bodyStr, "genre.name").String()
	if releaseDate == "" && bodyStr != "" {
		releaseDate = gjson.Get(bodyStr, "data.release_date_original").String()
		if download.Artist == "" {
			download.Artist = gjson.Get(bodyStr, "data.artist.name").String()
		}
		if download.Album == "" {
			download.Album = gjson.Get(bodyStr, "data.title").String()
		}
		download.Comment = gjson.Get(bodyStr, "data.version").String()
		download.Genre = gjson.Get(bodyStr, "data.genre.name").String()
		download.UPC = gjson.Get(bodyStr, "data.upc").String()
		download.CoverUrl = gjson.Get(bodyStr, "data.image.large").String()
		download.numTracks = int(gjson.Get(bodyStr, "data.tracks_count").Int())
		download.mediaCount = int(gjson.Get(bodyStr, "data.media_count").Int())
		download.label = gjson.Get(bodyStr, "data.copyright").String()
	} else {
		download.UPC = gjson.Get(bodyStr, "upc").String()
		download.CoverUrl = gjson.Get(bodyStr, "image.large").String()
		download.numTracks = int(gjson.Get(bodyStr, "tracks_count").Int())
		download.mediaCount = int(gjson.Get(bodyStr, "media_count").Int())
		download.label = gjson.Get(bodyStr, "label.name").String()
	}
	if len(releaseDate) >= 4 {
		download.Year = releaseDate[:4]
	}
	if download.Comment == "null" || download.Comment == "" {
		download.Comment = ""
	}
	if download.numTracks == 0 && numTracks > 0 {
		download.numTracks = numTracks
	}

	var itemsResult gjson.Result
	if albumItems.Exists() {
		itemsResult = albumItems
	} else if tracksJSON != "" {
		itemsResult = gjson.Parse(tracksJSON)
	}
	if !itemsResult.Exists() && QobuzToken != "" {
		itemsResult = gjson.Get(bodyStr, "tracks.items")
	}
	if !itemsResult.Exists() {
		itemsResult = gjson.Get(bodyStr, "data.tracks.items")
	}

	// Resolve each track's stream URL. The official getFileUrl 401s fast, but the
	// proxy fallback is one slow HTTP round trip per track — serialized, a
	// 20-track album keeps the download waiting ~1-2 minutes. Resolve
	// concurrently (bounded so we don't hammer the proxy) and assemble results
	// in track order.
	type resolvedTrack struct {
		track    File
		link     string
		proxyFmt int
		official bool
	}
	items := itemsResult.Array()
	results := make([]resolvedTrack, len(items))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, value := range items {
		wg.Add(1)
		go func(i int, value gjson.Result) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var track File
			var valueString = value.String()
			track.Id = int(gjson.Get(valueString, "id").Int())
			track.Name = gjson.Get(valueString, "title").String()
			track.Index = gjson.Get(valueString, "track_number").String()
			track.mediaNumber = gjson.Get(valueString, "media_number").String()
			track.isrc = gjson.Get(valueString, "isrc").String()
			track.duration = gjson.Get(valueString, "duration").Int()
			track.completed = false

			var official bool
			var proxyFmt int
			link := ""
			if NavLink != "" {
				// Full-length Navidrums backend gets first shot; the official token
				// may be free-tier and only serve 30 s samples.
				var nerr error
				link, proxyFmt, nerr = navGetStreamUrl(track.Id, quality)
				if nerr != nil {
					link = ""
					if Debug {
						fmt.Println("Navidrums stream failed for track "+track.Name+" ("+strconv.Itoa(track.Id)+"):", nerr)
					}
				}
			}
			if link == "" {
				link = qobuzGetTrackStreamUrl(track.Id, quality)
				if link != "" {
					official = true
				}
			}
			results[i] = resolvedTrack{track: track, link: link, proxyFmt: proxyFmt, official: official}
		}(i, value)
	}
	wg.Wait()

	var officialStreams int
	var proxyStreams int
	proxyFormat := 0
	for _, r := range results {
		track := r.track
		track.DownloadLink = r.link
		if r.link != "" {
			if r.official {
				officialStreams += 1
			} else {
				proxyStreams += 1
			}
			if r.proxyFmt != 0 {
				proxyFormat = r.proxyFmt
			}
			download.Files = append(download.Files, track)
		} else {
			fmt.Println("Couldn't get a download link for track " + track.Name + " (" + strconv.Itoa(track.Id) + "), skipping...")
			download.skippedTracks += 1
		}
	}

	// If the proxy took over every stream and demoted the format, relabel the
	// release so the folder name and file extension match the actual bytes.
	if proxyStreams > 0 && officialStreams == 0 && proxyFormat != 0 && strconv.Itoa(proxyFormat) != download.Quality {
		fmt.Println("Stream backend served format", proxyFormat, "for all", proxyStreams, "tracks (requested", download.Quality+"); relabelling release")
		download.Quality = strconv.Itoa(proxyFormat)
	}

	// Rebuild folder name to match the release name format served by the indexer.
	if download.Artist != "" && download.Album != "" {
		download.FileName = sanitizeFilename(download.Artist + "-" + download.Album)
		provider := releaseProviderTag(download.Source)
		if download.Quality == "5" {
			download.FileName += "-WEB-320-MP3-" + download.Year + "-" + provider
		} else {
			sr := gjson.Get(bodyStr, "maximum_sampling_rate").Float()
			if sr == 0 {
				sr = gjson.Get(bodyStr, "data.maximum_sampling_rate").Float()
			}
			if sr < 1000 && sr > 0 {
				sr *= 1000
			}
			if sr == 0 {
				sr = 44100
			}
			bd := gjson.Get(bodyStr, "maximum_bit_depth").Int()
			if bd == 0 {
				bd = gjson.Get(bodyStr, "data.maximum_bit_depth").Int()
			}
			if bd == 0 {
				bd = 16
			}
			srKHz := strconv.FormatFloat(sr/1000, 'f', -1, 64)
			download.FileName += "-" + strconv.FormatInt(bd, 10) + "BIT-" + srKHz + "-KHZ-WEB-FLAC-" + download.Year + "-" + provider
		}
	}

	// Base completion on the tracks we actually discovered and got a link for, so a
	// job can't stay "incomplete" forever (relevant when track/search must be used as
	// a fallback because album/get only surfaces a subset of an album's tracks).
	download.numTracks = len(download.Files)
	Downloads[Id] = &download
}

type QueueSlot struct {
	Status       string   `json:"status"`
	Index        int      `json:"index"`
	Password     string   `json:"password"`
	AvgAge       string   `json:"avg_age"`
	Script       string   `json:"script"`
	DirectUnpack string   `json:"direct_unpack"`
	Mb           string   `json:"mb"`
	MbLeft       string   `json:"mbleft"`
	MbMissing    string   `json:"mbmissing"`
	Size         string   `json:"size"`
	SizeLeft     string   `json:"sizeleft"`
	Filename     string   `json:"filename"`
	Labels       []string `json:"labels"`
	Priority     string   `json:"priority"`
	Cat          string   `json:"cat"`
	TimeLeft     string   `json:"timeleft"`
	Percentage   string   `json:"percentage"`
	NzoId        string   `json:"nzo_id"`
	UnpackOpts   string   `json:"unpackopts"`
}

type Queue struct {
	Paused bool        `json:"paused"`
	Slots  []QueueSlot `json:"slots"`
}

type QueueResponse struct {
	Queue Queue `json:"queue"`
}

func queue(w http.ResponseWriter, r *http.Request) {
	slots := []QueueSlot{}

	//fill slots with current download queue
	var index int = 0
	for id := range Downloads {
		var download Download = *Downloads[id]
		if download.downloaded == download.numTracks {
			//shouldnt be in queue anymore, skipping
			continue
		}
		//Don't know how long the download will take, so estimating 10 seconds per track remaining
		timeleft := (download.numTracks - download.downloaded) * 10
		//Guessing progress based on how many tracks are left, not based on file size
		progress := (int((float64(download.downloaded) / float64(download.numTracks)) * 100))

		slots = append(slots, QueueSlot{
			Status:       "Downloading",
			Index:        index,
			Password:     "",
			AvgAge:       "2895d",
			Script:       "None",
			DirectUnpack: "30/30",
			Mb:           "100",
			MbLeft:       strconv.Itoa(100 - progress),
			MbMissing:    "0.0",
			Size:         "100 MB",
			SizeLeft:     strconv.Itoa(100-progress) + " MB",
			Filename:     download.FileName,
			Labels:       []string{},
			Priority:     "Normal",
			Cat:          Category,
			TimeLeft:     "0:" + strconv.Itoa(timeleft/60) + ":" + strconv.Itoa(timeleft%60),
			Percentage:   strconv.Itoa(progress),
			NzoId:        "SABnzbd_nzo_" + download.Id,
			UnpackOpts:   "3",
		})
		index++
	}

	json.NewEncoder(w).Encode(QueueResponse{
		Queue: Queue{
			Paused: false,
			Slots:  slots,
		},
	})
}

type HistorySlot struct {
	Name         string `json:"name"`
	NzbName      string `json:"nzb_name"`
	Category     string `json:"category"`
	Bytes        int64  `json:"bytes"`
	DownloadTime int    `json:"download_time"`
	Status       string `json:"status"`
	Storage      string `json:"storage"`
	NzoId        string `json:"nzo_id"`
}

type History struct {
	Slots []HistorySlot `json:"slots"`
}

type HistoryResponse struct {
	History History `json:"history"`
}

func history(w http.ResponseWriter, r *http.Request) {
	//check for deletion call first
	//api?mode=history&name=delete&del_files=1&value=SABnzbd_nzo_0825646642830&archive=1&apikey=(removed)&output=json
	if r.URL.Query().Get("name") == "delete" {
		var id, _ = strings.CutPrefix(r.URL.Query().Get("value"), "SABnzbd_nzo_")
		if Downloads[id] != nil {
			if r.URL.Query().Get("del_files") == "1" {
				err := os.RemoveAll(filepath.Join(DownloadPath, "complete", Category, Downloads[id].FileName))
				if err != nil {
					fmt.Println("Couldn't delete folder " + Downloads[id].FileName)
					fmt.Println(err)
				}
			}
			delete(Downloads, id)
		}
	}

	slots := []HistorySlot{}
	//fill this with completed history
	for id := range Downloads {
		var download Download = *Downloads[id]
		if download.downloaded < download.numTracks && download.downloaded != -1 {
			//not finished yet, skipping...
			continue
		}
		// Get the fileinfo
		fileInfo, err := os.Stat(filepath.Join(DownloadPath, "complete", Category, download.FileName))
		var fileSize int64
		if err != nil {
			//cant get file stats on Docker for some reason? giving arbitrary size info
			fileSize = 10000
		} else {
			fileSize = fileInfo.Size()
		}
		var status string
		if download.downloaded == -1 {
			status = "Failed"
		} else {
			status = "Completed"
		}

		slots = append(slots, HistorySlot{
			Name:         download.FileName,
			NzbName:      download.FileName + ".nzb",
			Category:     Category,
			Bytes:        fileSize,
			DownloadTime: download.numTracks * 30,
			Status:       status,
			Storage:      filepath.Join(DownloadPath, "complete", Category, download.FileName),
			NzoId:        "SABnzbd_nzo_" + download.Id,
		})
	}

	json.NewEncoder(w).Encode(HistoryResponse{
		History: History{
			Slots: slots,
		},
	})
}

// mp3DurationSeconds returns the duration of an MPEG1 Layer III file by
// walking its frame headers (bitrate table + frame length). Returns 0 on
// parse failure or non-MP3 content. Used to detect 30-second free-account
// samples, which are valid MP3s that would otherwise look like full tracks.
func mp3DurationSeconds(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 {
		return 0
	}
	off := 0
	if len(data) >= 10 && data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
		size := int(data[6]&0x7f)<<21 | int(data[7]&0x7f)<<14 | int(data[8]&0x7f)<<7 | int(data[9]&0x7f)
		off = 10 + size
	}
	bitrates := [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	rates := [4]int{44100, 48000, 32000, 0}
	var frames int
	var bitrate, rate int
	for off+4 <= len(data) {
		h := binary.BigEndian.Uint32(data[off : off+4])
		if h>>21 != 0x7ff {
			break
		}
		if (h>>19)&3 != 3 || (h>>17)&3 != 1 { // MPEG1 Layer III only
			off++
			continue
		}
		if bitrate == 0 {
			bitrate = bitrates[(h>>12)&0xf]
			rate = rates[(h>>10)&3]
			if bitrate == 0 || rate == 0 {
				return 0
			}
		}
		off += 144*bitrate*1000/rate + int((h>>9)&1)
		frames++
	}
	if rate == 0 || frames == 0 {
		return 0
	}
	return float64(frames) * 1152 / float64(rate)
}

// flacDurationSeconds measures a FLAC file's real duration from STREAMINFO so a
// 30-second sample can't pass as a full track. Handles both plain FLAC and the
// MP4/FLAC container Qobuz uses for 24-bit files (fLaC header, then a dfLa box).
// isPreviewLength decides whether a downloaded file is a 30 s sample rather than
// the real track. Qobuz previews are cut at 30 s, so a file is only suspect when
// it is far shorter than the catalog duration; a genuinely short interlude
// (30 s track) must not be flagged.
func isPreviewLength(measured, expected float64) bool {
	if measured <= 0 {
		return false
	}
	if expected > 0 {
		return measured < expected*0.8 && expected-measured > 30
	}
	return measured < 60
}

func flacDurationSeconds(path string) float64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	head := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	idx := bytes.Index(head, []byte("fLaC"))
	if idx < 0 {
		return 0
	}
	stream := head[idx:]
	if d := bytes.Index(stream, []byte("dfLa")); d >= 0 && len(stream) >= d+8 {
		stream = append([]byte("fLaC"), stream[d+8:]...)
	}
	if len(stream) < 26 {
		return 0
	}

	info := binary.BigEndian.Uint64(stream[18:26])
	rate := info >> 44
	samples := info & ((1 << 36) - 1)
	if rate == 0 || samples == 0 {
		return 0
	}
	return float64(samples) / float64(rate)
}

func sanitizeFilename(name string) string {
	// Forbidden characters on Windows: < > : " / \ | ? *
	re := regexp.MustCompile(`[<>:"/\\|?*]`)
	return re.ReplaceAllString(name, "_")
}

// fixEncoding heals artist/album strings that arrive as Latin-1/Windows-1252 bytes
// (single 0x80-0xFF bytes) instead of proper UTF-8. Some clients (curl in MSYS,
// browser extensions on cp1252 systems) send accented characters as Latin-1, which
// makes Qobuz reject the query with HTTP 400. Valid UTF-8 passes through untouched;
// lone high bytes are reinterpreted as their U+00XX code point and re-encoded as UTF-8.
func fixEncoding(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune(rune(s[i])) // Latin-1 byte -> code point
			i++
		} else {
			sb.WriteRune(r)
			i += size
		}
	}
	return sb.String()
}

func grabFile(dest string, url string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		_ = os.Remove(dest)
		return err
	}

	return nil
}

func startDownload(Id string) {
	download := Downloads[Id]
	if download == nil {
		return
	}
	if download.Source == "deezer" {
		startDeezerDownload(Id)
		return
	}
	if download.Source == "tidal" {
		startTidalDownload(Id)
		return
	}

	//create folder
	var Folder string = filepath.Join(DownloadPath, "incomplete", Category, download.FileName)
	err := os.MkdirAll(Folder, 0755)
	if err != nil {
		fmt.Println("Couldn't create folder:", Folder, err)
		return
	}

	//Download cover art
	if download.CoverUrl != "" {
		_ = grabFile(filepath.Join(Folder, "cover.jpg"), download.CoverUrl)
	}

	//Download each track
	for _, track := range download.Files {
		var trackFolder string = Folder
		if download.mediaCount > 1 {
			// Create CD subfolder if multiple discs
			discNumber, _ := strconv.Atoi(track.mediaNumber)
			trackFolder = filepath.Join(Folder, fmt.Sprintf("CD %02d", discNumber))
			if err := os.MkdirAll(trackFolder, 0755); err != nil {
				fmt.Printf("Error creating track folder: %v\n", err)
			}
		}

		var ext string
		if download.Quality == "5" {
			ext = ".mp3"
		} else {
			ext = ".flac"
		}
		var Name string = sanitizeFilename(track.Index+" - "+download.Artist+" - "+track.Name) + ext
		err := grabFile(filepath.Join(trackFolder, Name), track.DownloadLink)
		if err != nil {
			fmt.Println("Failed to download track "+track.Name+":", err)
		} else {
			track.completed = true
			download.downloaded += 1
			writeMetaData(*download, track, filepath.Join(trackFolder, Name))
			// Free Qobuz accounts only get 30-second samples; a sample is a
			// valid audio file so it "succeeds" silently. Flag it by measured
			// duration so previews can never masquerade as full tracks.
			dur := 0.0
			if download.Quality == "5" {
				dur = mp3DurationSeconds(filepath.Join(trackFolder, Name))
			} else {
				dur = flacDurationSeconds(filepath.Join(trackFolder, Name))
			}
			if dur > 0 && isPreviewLength(dur, float64(track.duration)) {
				download.previewTracks += 1
				fmt.Printf("PREVIEW DETECTED: %q is only %.0f s (30s free-account sample)\n", track.Name, dur)
			}
		}
	}

	// If every track came back as a sample, say so in the folder name instead
	// of silently delivering previews as full tracks.
	if download.downloaded > 0 && download.previewTracks >= download.downloaded {
		fmt.Println("WARNING: no full-length tracks downloaded — token only has free-account preview access; marking folder")
		download.FileName += "-30S-SAMPLE"
	}

	//Download (should be) complete, move to complete folder
	destFolder := filepath.Join(DownloadPath, "complete", Category, download.FileName)
	if err := os.MkdirAll(filepath.Dir(destFolder), 0755); err != nil {
		fmt.Printf("Error creating destination category folder: %v\n", err)
	}
	if download.skippedTracks > 0 {
		fmt.Printf("Skipped %d track(s) with no download link\n", download.skippedTracks)
	}
	// Re-downloading an already-completed release collides with the existing
	// complete folder: Windows rename refuses to overwrite, which strands the
	// finished download in incomplete where the next startup deletes it.
	// Remove the stale copy (same release) before moving.
	if _, statErr := os.Stat(destFolder); statErr == nil {
		if rmErr := os.RemoveAll(destFolder); rmErr != nil {
			fmt.Println("Couldn't remove old complete folder:", destFolder, rmErr)
		}
	}
	err = os.Rename(Folder, destFolder)
	if err != nil {
		fmt.Println("Error moving to complete folder:", err)
	} else {
		fmt.Println("Download complete:", download.FileName)
	}
}

func writeMetaData(album Download, track File, fileName string) {
	tags := map[string][]string{
		taglib.AlbumArtist: {album.Artist},
		taglib.Artist:      {album.Artist},
		taglib.Album:       {album.Album},
		taglib.TrackNumber: {track.Index},
		taglib.Title:       {track.Name},
		taglib.Comment:     {album.Comment},
		taglib.DiscNumber:  {track.mediaNumber},
		taglib.Label:       {album.label},
		taglib.ISRC:        {track.isrc},
	}

	if album.Genre != "" {
		tags[taglib.Genre] = []string{album.Genre}
	}
	if album.Year != "" {
		tags[taglib.Date] = []string{album.Year}
	}
	if album.UPC != "" {
		tags["BARCODE"] = []string{album.UPC}
	}

	err := taglib.WriteTags(fileName, tags, 0)
	if err != nil {
		fmt.Println("Couldn't write Metadata to file "+fileName+":", err)
	}
}
