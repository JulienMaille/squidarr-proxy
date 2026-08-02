package main

import (
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
	DownloadLink string
	completed    bool
}

type Download struct {
	Id            string
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

		if artist != "" && album != "" {
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

		generateDownload(filename, id, numTracks, quality, artist, album)
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

	// Extract metadata from NZB XML comments (<!-- ... -->), in the order
	// written by fakenzb: QobuzId, NumTracks, Quality, Artist, Album.
	// Search the whole body (line indexes are fragile when the NZB layout changes).
	reComment := regexp.MustCompile(`<!--\s+([^<]+?)\s+-->`)
	var Id string
	var NumTracks int
	var quality string
	var artist string
	var album string
	for _, line := range strings.Split(bodyStr, "\n") {
		m := reComment.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		val := strings.TrimSpace(m[1])
		// Numeric comments in order: id, track count, quality
		if num, err := strconv.Atoi(val); err == nil {
			if Id == "" {
				Id = val
			} else if NumTracks == 0 {
				NumTracks = num
			} else if quality == "" {
				quality = val
			}
			continue
		}
		// Non-numeric comments in order: artist, album
		if artist == "" {
			artist = val
		} else if album == "" {
			album = val
		}
	}

	if Id == "" || NumTracks == 0 {
		fmt.Println("/downloader/api/addfile Failed to find ID or NumTracks in body")
		return
	}

	fmt.Println("Adding download:", filename, "ID:", Id, "Tracks:", NumTracks, "Quality:", quality, "Artist:", artist, "Album:", album)
	generateDownload(filename, Id, NumTracks, quality, artist, album)

	//send response using QobuzId as nzo_id
	w.Write([]byte("{\n" +
		"\"status\": true,\n" +
		"\"nzo_ids\": [\"SABnzbd_nzo_" + Id + "\"]\n" +
		"}"))
	if Downloads[Id] != nil && Downloads[Id].downloaded != -1 {
		go startDownload(Id)
	}
}

func generateDownload(filename string, Id string, numTracks int, qualityParam string, artistHint string, albumHint string) {
	// Heal accents that may have arrived as Latin-1 instead of UTF-8.
	artistHint = fixEncoding(artistHint)
	albumHint = fixEncoding(albumHint)
	var download Download
	download.Id = Id
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
	//Rebuild folder name to match the release name format served by the indexer
	if download.Artist != "" && download.Album != "" {
		download.FileName = sanitizeFilename(download.Artist + "-" + download.Album)
		if download.Quality == "5" {
			download.FileName += "-WEB-320-MP3-" + download.Year + "-SQUIDWTF"
		} else {
			sr := gjson.Get(bodyStr, "maximum_sampling_rate").Float()
			if sr < 1000 && sr > 0 {
				sr *= 1000
			}
			if sr == 0 {
				sr = 44100
			}
			bd := gjson.Get(bodyStr, "maximum_bit_depth").Int()
			if bd == 0 {
				bd = 16
			}
			srKHz := strconv.FormatFloat(sr/1000, 'f', -1, 64)
			download.FileName += "-" + strconv.FormatInt(bd, 10) + "BIT-" + srKHz + "-KHZ-WEB-FLAC-" + download.Year + "-SQUIDWTF"
		}
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
	itemsResult.ForEach(func(key, value gjson.Result) bool {
		var track File
		var valueString = value.String()
		track.Id = int(gjson.Get(valueString, "id").Int())
		track.Name = gjson.Get(valueString, "title").String()
		track.Index = gjson.Get(valueString, "track_number").String()
		track.mediaNumber = gjson.Get(valueString, "media_number").String()
		track.isrc = gjson.Get(valueString, "isrc").String()
		track.completed = false

		track.DownloadLink = qobuzGetTrackStreamUrl(track.Id, quality)
		if track.DownloadLink == "" {
			fmt.Println("Couldn't get a download link for track " + track.Name + " (" + strconv.Itoa(track.Id) + "), skipping...")
			download.skippedTracks += 1
		} else {
			download.Files = append(download.Files, track)
		}
		return true
	})

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
		}
	}

	//Download (should be) complete, move to complete folder
	destFolder := filepath.Join(DownloadPath, "complete", Category, download.FileName)
	if err := os.MkdirAll(filepath.Dir(destFolder), 0755); err != nil {
		fmt.Printf("Error creating destination category folder: %v\n", err)
	}
	if download.skippedTracks > 0 {
		fmt.Printf("Skipped %d track(s) with no download link\n", download.skippedTracks)
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
