package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var ArcodLink string

func init() {
	if v := os.Getenv("ARCOD_LINK"); v != "" {
		ArcodLink = strings.TrimRight(v, "/")
	}
}

type arcodStreamResponse struct {
	URL      string `json:"url"`
	MimeType string `json:"mimeType"`
	Quality  int    `json:"quality"`
	TrackId  string `json:"trackId"`
}

// arcodGetStreamUrl calls https://arcod.xyz/api/player/stream/{trackId}?quality={5|27}
// and returns the signed stream URL plus the format code actually served (5 = MP3 320, 27 = FLAC).
func arcodGetStreamUrl(trackId int, quality string) (string, int, error) {
	if ArcodLink == "" {
		return "", 0, fmt.Errorf("no Arcod backend configured")
	}

	arcodQuality := "5"
	if quality != "5" {
		arcodQuality = "27"
	}

	apiUrl := ArcodLink + "/api/player/stream/" + strconv.Itoa(trackId) + "?quality=" + arcodQuality

	req, _ := http.NewRequest("GET", apiUrl, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", 0, fmt.Errorf("arcod HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var res arcodStreamResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return "", 0, fmt.Errorf("arcod invalid JSON: %s", err)
	}
	if res.URL == "" {
		return "", 0, fmt.Errorf("arcod returned no url")
	}

	proxyFmt := 5
	if res.Quality == 27 || arcodQuality == "27" {
		proxyFmt = 27
	}

	return res.URL, proxyFmt, nil
}
