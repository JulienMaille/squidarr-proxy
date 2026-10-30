package main

import (
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

var searchAppId = "798273057"
var searchAppSecret = "abb21364945c0583309667d13ca3d93a"

func init() {
	// Credential rotation: the web-player bundle IDs get flagged/banned over
	// time; allow overriding both without a rebuild (see config.env).
	if v := os.Getenv("APP_ID"); v != "" {
		searchAppId = v
	}
	if v := os.Getenv("APP_SECRET"); v != "" {
		searchAppSecret = v
	}
}

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
			apiUrl += k + "=" + url.QueryEscape(params[k])
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

func qobuzDoRequest(endpoint string, params map[string]string) (string, error) {
	resp, err := qobuzRequest("GET", endpoint, params)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("official API %s HTTP %d", endpoint, resp.StatusCode)
	}
	return string(body), nil
}

// qobuzGetAlbum fetches the authoritative album record (full track list) via album/get.
// It uses the SAME app credential (searchAppId/searchAppSecret/token) as track/search —
// the credential is not endpoint-specific. The call itself mirrors the proven pattern
// from monochrome / qobuz-tidal-eclipse: UNSIGNED, with user_auth_token in the URL
// (those projects only sign getFileUrl, not album/get). A signed attempt is kept as
// a fallback in case an unsigned album/get is ever rejected.
func qobuzGetAlbum(albumId string) (string, error) {
	if QobuzToken == "" {
		return "", fmt.Errorf("no Qobuz token configured")
	}

	// Primary: unsigned call, token in URL (same as monochrome/eclipse).
	unsignedURL := "https://www.qobuz.com/api.json/0.2/album/get?app_id=" + searchAppId +
		"&user_auth_token=" + QobuzToken +
		"&album_id=" + albumId +
		"&limit=100&offset=0"
	req, _ := http.NewRequest("GET", unsignedURL, nil)
	req.Header.Set("X-App-Id", searchAppId)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	if Debug {
		fmt.Println("Qobuz album/get (unsigned) request:", unsignedURL[:min(len(unsignedURL), 150)])
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 200 {
			return string(body), nil
		}
		if Debug {
			fmt.Println("Qobuz album/get unsigned HTTP", resp.StatusCode, string(body)[:min(len(string(body)), 120)])
		}
	}

	// Fallback: signed request through qobuzRequest (same as other endpoints).
	resp2, err2 := qobuzRequest("GET", "album/get", map[string]string{"album_id": albumId, "limit": "100", "offset": "0"})
	if err2 != nil {
		return "", fmt.Errorf("album/get failed (unsigned+%v; signed+%v)", err, err2)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != 200 {
		return "", fmt.Errorf("official API album/get HTTP %d", resp2.StatusCode)
	}
	return string(body2), nil
}

// qobuzSearchAlbum searches for an album by id or title+artist and returns album metadata JSON.
// The returned JSON uses the same shape as the old album/get response.
func qobuzSearchAlbum(albumId, artist, title string) (string, error) {
	query := artist + " " + title
	query = fixEncoding(query)
	body, err := qobuzDoRequest("catalog/search", map[string]string{"query": query, "limit": "20", "offset": "0"})
	if err != nil {
		return "", err
	}

	items := gjson.Get(body, "albums.items").Array()

	// First, exact album ID match (most reliable)
	for _, item := range items {
		if gjson.Get(item.String(), "qobuz_id").String() == albumId {
			return item.String(), nil
		}
	}

	// Then fuzzy title+artist match (normalized)
	norm := func(s string) string {
		return strings.ToLower(strings.TrimSpace(s))
	}
	for _, item := range items {
		itemArtist := norm(gjson.Get(item.String(), "artist.name").String())
		itemTitle := norm(gjson.Get(item.String(), "title").String())
		if strings.EqualFold(itemArtist, artist) &&
			(strings.Contains(itemTitle, norm(title)) || strings.Contains(norm(title), itemTitle)) {
			return item.String(), nil
		}
	}

	return "", fmt.Errorf("album not found: %s - %s (id %s)", artist, title, albumId)
}

// qobuzGetTracksByAlbum searches for tracks matching an album by title+artist
// and filters by album ID. Returns the tracks.items JSON array.
// Tries multiple queries and merges results to maximize coverage.
func qobuzGetTracksByAlbum(albumId, artist, title string) (string, error) {
	artist = fixEncoding(artist)
	title = fixEncoding(title)
	queries := []string{}
	if title != "" && artist != "" {
		queries = append(queries, title+" "+artist)
	}
	if artist != "" {
		queries = append(queries, artist)
	}
	if title != "" {
		queries = append(queries, title)
	}
	if len(queries) == 0 {
		return "", fmt.Errorf("no search hints for album: %s", albumId)
	}

	seen := make(map[string]bool)
	var matched []string
	for _, query := range queries {
		body, err := qobuzDoRequest("track/search", map[string]string{"query": query, "limit": "100"})
		if err != nil {
			if Debug {
				fmt.Println("Qobuz track/search failed for query '"+query+"':", err)
			}
			continue
		}

		items := gjson.Get(body, "tracks.items").Array()
		for _, item := range items {
			itemAlbumId := gjson.Get(item.String(), "album.qobuz_id").String()
			if itemAlbumId == albumId {
				trackId := gjson.Get(item.String(), "id").String()
				if !seen[trackId] {
					seen[trackId] = true
					matched = append(matched, item.String())
				}
			}
		}

		if Debug {
			fmt.Println("Qobuz track/search query '"+query+"': found", len(matched), "unique tracks total so far")
		}
	}

	if len(matched) == 0 {
		return "", fmt.Errorf("no tracks found for album: %s", albumId)
	}

	return "[" + strings.Join(matched, ",") + "]", nil
}

func qobuzGetTrackStreamUrl(trackId int, quality string) string {
	if QobuzToken == "" {
		return ""
	}

	fmtId := quality
	if quality == "" {
		fmtId = "5"
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)

	monoSecret := searchAppSecret
	sigInput := "trackgetFileUrlformat_id" + fmtId + "intentstreamtrack_id" + strconv.Itoa(trackId) + ts + monoSecret
	sig := fmt.Sprintf("%x", md5.Sum([]byte(sigInput)))

	apiUrl := "https://www.qobuz.com/api.json/0.2/track/getFileUrl" +
		"?app_id=" + searchAppId +
		"&user_auth_token=" + QobuzToken +
		"&track_id=" + strconv.Itoa(trackId) +
		"&format_id=" + fmtId +
		"&intent=stream" +
		"&request_ts=" + ts +
		"&request_sig=" + sig

	req, _ := http.NewRequest("GET", apiUrl, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	if Debug {
		fmt.Println("Qobuz stream URL request:", apiUrl)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return ""
	}

	return gjson.Get(string(body), "url").String()
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
