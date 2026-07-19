package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var DownloadPath string
var Category string
var Port string
var ApiLink string
var ApiKey string
var QualityId string
var FileExtension string
var Debug bool
var QobuzToken string

func apiRequest(endpoint string) (*http.Response, error) {
	url := ApiLink + endpoint

	if Debug {
		fmt.Println("Making request to:", url)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Always send headers required by the new API
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-language", "fr-FR,fr;q=0.9,en-US;q=0.8,en-GB;q=0.7,en;q=0.6,it-IT;q=0.5,it;q=0.4,sv;q=0.3")
	req.Header.Set("dnt", "1")
	req.Header.Set("origin", "https://monochrome.tf")
	req.Header.Set("priority", "u=1, i")
	req.Header.Set("sec-ch-ua", `"Microsoft Edge";v="147", "Not.A/Brand";v="8", "Chromium";v="147"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "cross-site")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36 Edg/147.0.0.0")

	client := &http.Client{}
	return client.Do(req)
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}

func main() {
	DownloadPath = getEnv("DOWNLOAD_PATH", "/data/squidarr/")
	Category = getEnv("CATEGORY", "music")
	Port = getEnv("PORT", "8687")
	// Alternative API links:
	// ApiLink = getEnv("API_LINK", "https://qobuz.squid.wtf/api")
	// ApiLink = getEnv("API_LINK", "https://qobuz-api1.onrender.com")
	// ApiLink = getEnv("API_LINK", "https://trypt-hifi-dl-456461932686.us-west1.run.app")
	ApiLink = getEnv("API_LINK", "https://qobuz-api.stremio123.duckdns.org")
	ApiKey = getEnv("API_KEY", "")
	QobuzToken = getEnv("QOBUZ_TOKEN", "")

	if QobuzToken != "" {
		fmt.Println("Qobuz token configured, will use official API")
	}

	Debug = getEnv("DEBUG", "false") == "true"
	quality := getEnv("QUALITY", "flac")
	if quality == "mp3-320" {
		QualityId = "5" // LOW in Monochrome API
		FileExtension = ".mp3"
	} else if quality == "flac-lossless" {
		QualityId = "7" // LOSSLESS in Monochrome API
		FileExtension = ".flac"
	} else {
		QualityId = "27" // HI_RES_LOSSLESS in Monochrome API
		FileExtension = ".flac"
	}

	//create folders if they don't exist yet
	os.MkdirAll(DownloadPath, 0775)
	os.MkdirAll(filepath.Join(DownloadPath, "incomplete", Category), 0775)
	os.MkdirAll(filepath.Join(DownloadPath, "complete", Category), 0775)

	//and now clear anything in /incomplete that was created by squidarr. Likely a leftover failed download
	folders, err := os.ReadDir(filepath.Join(DownloadPath, "incomplete", Category))
	if err != nil {
		fmt.Println("Couldn't read incomplete folder: ")
		fmt.Println(err)
	}
	for _, folder := range folders {
		if strings.Contains(folder.Name(), "-SQUIDWTF") {
			fmt.Println("Removing incomplete download " + folder.Name())
			err := os.RemoveAll(filepath.Join(DownloadPath, "incomplete", Category, folder.Name()))
			if err != nil {
				fmt.Println("Failed to remove folder!")
				fmt.Println(err)
			}
		}
	}

	// Generate a basic list of downloads from folders in /complete. likely from completed downloads that weren't imported before reboot.
	// Adding these to the downloads list allows importing/deleting from Lidarr
	folders, _ = os.ReadDir(filepath.Join(DownloadPath, "complete", Category))
	for _, folder := range folders {
		if strings.Contains(folder.Name(), "-SQUIDWTF") {
			fmt.Println("Adding completed download " + folder.Name() + " to history")
			var download Download
			download.FileName = folder.Name()
			//Don't really care about this anymore, but making sure they're equal so they show up in the history, not the queue
			download.numTracks = 1
			download.downloaded = 1
			//Can't know the exact ID anymore, but all it's needed for now is as a NZO_ID so generating a random one...
			b := make([]byte, 13)
			for i := range b {
				b[i] = byte(rand.Intn(27) + 65)
			}
			download.Id = string(b)
			Downloads[download.Id] = &download
		}
	}

	http.HandleFunc("/indexer", corsHandler(handleIndexerRequest))
	http.HandleFunc("/downloader/api", corsHandler(handleDownloaderRequest))
	http.HandleFunc("/api/download", corsHandler(handleSimpleDownload))
	http.HandleFunc("/api/", corsHandler(handleSimpleDownload)) // also serve at /api/ for doc purposes
	fmt.Println("Listening on port " + Port + "...")
	http.ListenAndServe(":"+Port, nil)
}

func corsHandler(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		h(w, r)
	}
}
