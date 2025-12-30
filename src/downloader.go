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

	"github.com/cavaliergopher/grab/v3"
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
	Id         string
	Artist     string
	Album      string
	Comment    string
	CoverUrl   string
	numTracks  int
	mediaCount int
	label      string
	downloaded int
	FileName   string
	Files      []File
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
	}
	reNum := regexp.MustCompile("[a-zA-Z0-9]+")
	reName := regexp.MustCompile("filename=.*.nzb")
	var lines []string = strings.Split(string(body), "\n")
	var filename string = reName.FindString(lines[1])
	filename = strings.Trim(filename, "filename=\"")
	filename = strings.TrimRight(filename, ".nzb")
	var Id = reNum.FindString(lines[6])
	fmt.Println(filename)
	filename = sanitizeFilename(filename)
	var NumTracks, _ = strconv.Atoi(reNum.FindString(lines[7]))
	generateDownload(filename, Id, NumTracks)
	//send response using QobuzId as nzo_id
	w.Write([]byte("{\n" +
		"\"status\": true,\n" +
		"\"nzo_ids\": [\"SABnzbd_nzo_" + Id + "\"]\n" +
		"}"))
	if Downloads[Id].downloaded != -1 {
		go startDownload(Id)
	}
}

func generateDownload(filename string, Id string, numTracks int) {
	var download Download
	download.Id = Id
	download.numTracks = numTracks
	download.FileName = filename
	download.downloaded = 0
	var queryUrl string = ApiLink + "/get-album?album_id=" + Id
	resp, err := http.Get(queryUrl)
	if err != nil {
		fmt.Println(err)
		return
	}
	//making the request body usable
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
		return
	}
	download.Artist = gjson.Get(string(bodyBytes), "data.artist.name").String()
	download.Album = gjson.Get(string(bodyBytes), "data.title").String()
	download.Comment = gjson.Get(string(bodyBytes), "data.version").String()
	download.CoverUrl = gjson.Get(string(bodyBytes), "data.image.large").String()
	if download.Comment == "null" {
		download.Comment = ""
	}
	download.numTracks = int(gjson.Get(string(bodyBytes), "data.tracks_count").Int())
	download.mediaCount = int(gjson.Get(string(bodyBytes), "data.media_count").Int())
	download.label = gjson.Get(string(bodyBytes), "data.copyright").String()
	result := gjson.Get(string(bodyBytes), "data.tracks.items")
	result.ForEach(func(key, value gjson.Result) bool {
		var track File
		var valueString = value.String()
		track.Id = int(gjson.Get(valueString, "id").Int())
		track.Name = gjson.Get(valueString, "title").String()
		track.Index = gjson.Get(valueString, "track_number").String()
		track.mediaNumber = gjson.Get(valueString, "media_number").String()
		track.isrc = gjson.Get(valueString, "isrc").String()
		track.completed = false
		var queryUrl string = ApiLink + "/download-music?track_id=" + strconv.Itoa(track.Id) + "&quality=" + QualityId
		resp, err := http.Get(queryUrl)
		if err != nil {
			fmt.Println(err)
			return false
		}
		//making the request body usable
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println(err)
			return false
		}
		track.DownloadLink = gjson.Get(string(bodyBytes), "data.url").String()
		if track.DownloadLink == "" {
			fmt.Println("squid.wtf didn't give a link for track " + track.Name)
			fmt.Println("This is most likely an error with squid.wtf, Qobuz-DL or Qobuz itself...")
			fmt.Println("Cancelling download...")
			download.downloaded = -1
			err := os.RemoveAll(DownloadPath + "/incomplete/" + Category + "/" + download.FileName)
			if err != nil {
				fmt.Println("Couldn't delete folder " + download.FileName)
				fmt.Println(err)
			}
			return false
		}
		download.Files = append(download.Files, track)
		return true
	})
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
			break
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
		if r.URL.Query().Get("del_files") == "1" {
			err := os.RemoveAll(filepath.Join(DownloadPath, "complete", Category, Downloads[id].FileName))
			if err != nil {
				fmt.Println("Couldn't delete folder " + Downloads[id].FileName)
				fmt.Println(err)
			}
		}
		delete(Downloads, id)
	}

	slots := []HistorySlot{}
	//fill this with completed history
	for id := range Downloads {
		var download Download = *Downloads[id]
		if download.downloaded < download.numTracks && download.downloaded != -1 {
			//not finished yet, skipping...
			break
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

func startDownload(Id string) {
	download := Downloads[Id]
	//create folder
	var Folder string = filepath.Join(DownloadPath, "incomplete", Category, download.FileName)
	err := os.Mkdir(Folder, 0755)
	if err != nil {
		fmt.Println("Couldn't create folder in " + filepath.Join(DownloadPath, "incomplete", Category))
		fmt.Println(err)
		return
	}
	//Download cover art
	_, err = grab.Get(filepath.Join(Folder, "cover.jpg"), download.CoverUrl)
	if err != nil {
		fmt.Println("Failed to download cover")
		fmt.Println(err)
		return
	}
	//Download each track
	for _, track := range download.Files {
		var Name string = sanitizeFilename(track.Index+" - "+download.Artist+" - "+track.Name) + FileExtension
		_, err := grab.Get(filepath.Join(Folder, Name), track.DownloadLink)
		if err != nil {
			fmt.Println("Failed to download track " + track.Name)
			fmt.Println(err)
			return
		} else {
			track.completed = true
			download.downloaded += 1
			writeMetaData(*download, track, filepath.Join(Folder, Name))
		}
	}
	//Download (should be) complete, move to complete folder
	os.Rename(Folder, filepath.Join(DownloadPath, "complete", Category, download.FileName))
}

func writeMetaData(album Download, track File, fileName string) {
	err := taglib.WriteTags(fileName, map[string][]string{
		taglib.AlbumArtist: {album.Artist},
		taglib.Artist:      {album.Artist},
		taglib.Album:       {album.Album},
		taglib.TrackNumber: {track.Index},
		taglib.Title:       {track.Name},
		taglib.Comment:     {album.Comment},
		taglib.DiscNumber:  {track.mediaNumber},
		taglib.Label:       {album.label},
		taglib.ISRC:        {track.isrc},
	}, 0)
	if err != nil {
		fmt.Println("Couldn't write Metadata to file " + fileName)
		fmt.Println(err)
	}
}
