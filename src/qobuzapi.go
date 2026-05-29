package main

import (
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

var searchAppId = "712109809"
var searchAppSecret = "589be88e4538daea11f509d29e4a23b1"

func qobuzRequest(method string, endpoint string, params map[string]string) (*http.Response, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	normalized := strings.TrimPrefix(strings.TrimPrefix(endpoint, "/"), "api.json/0.2/")
	normalized = strings.ReplaceAll(normalized, "/", "")

	var sortedArgs string
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		sortedArgs += k + params[k]
	}

	sigInput := normalized + sortedArgs + ts + searchAppSecret
	sig := fmt.Sprintf("%x", md5.Sum([]byte(sigInput)))

	apiUrl := "https://www.qobuz.com/api.json/0.2/" + endpoint
	if len(params) > 0 {
		apiUrl += "?"
		for i, k := range keys {
			if i > 0 {
				apiUrl += "&"
			}
			apiUrl += k + "=" + params[k]
		}
		apiUrl += "&app_id=" + searchAppId + "&request_ts=" + ts + "&request_sig=" + sig
	}

	req, _ := http.NewRequest(method, apiUrl, nil)
	req.Header.Set("X-App-Id", searchAppId)
	if QobuzToken != "" {
		req.Header.Set("X-User-Auth-Token", QobuzToken)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	if Debug {
		fmt.Println("Qobuz API request:", method, apiUrl)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	return client.Do(req)
}

func qobuzGetAlbum(albumId string) (string, error) {
	resp, err := qobuzRequest("GET", "album/get", map[string]string{"album_id": albumId})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("official API album/get HTTP %d", resp.StatusCode)
	}
	return string(body), nil
}

func qobuzGetTrackDownloadUrl(trackId int, quality string) string {
	qs := quality
	if qs == "" {
		qs = QualityId
	}
	resp, err := qobuzRequest("GET", "track/get", map[string]string{"track_id": strconv.Itoa(trackId)})
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return ""
	}
	return gjson.Get(string(body), "sample").String()
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}