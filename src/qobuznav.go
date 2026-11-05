package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// Navidrums-style Qobuz proxy (qobuz-rest-api / Navidrums route names):
//
//	GET /api/get-music?q=&offset=      -> {success, data:{albums:{items:[...]},tracks:{...}}}
//	GET /api/get-album?album_id=       -> {success, data:{...,tracks:{items:[...]}}}
//	GET /api/download-music?track_id=&quality= -> {success, data:{url}}
//
// Unlike the official API, this backend streams FULL-LENGTH audio from its own
// premium pool (no `sample`/`range=20-30` previews), so it is the primary stream
// source when configured.

func navRequest(path string) ([]byte, error) {
	req, err := http.NewRequest("GET", NavLink+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	if Debug {
		fmt.Println("Navidrums request:", NavLink+path)
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("navidrums %s HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 120))
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("navidrums %s returned non-JSON: %s", path, truncate(string(body), 120))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// navQuality maps a Qobuz format id (5 = MP3-320, 6 = CD FLAC, 7 = hi-res,
// 27 = hi-res >96 kHz) to the format the Navidrums backend expects.
func navQuality(quality string) string {
	switch quality {
	case "5":
		return "5"
	case "6":
		return "6"
	case "7":
		return "6"
	default:
		return "27"
	}
}

// navSearchAlbums searches albums through the Navidrums backend. Ids are the
// ALPHANUMERIC catalog ids, which is what /api/get-album accepts (it rejects the
// numeric qobuz_id with HTTP 400).
func navSearchAlbums(query string, limit int) []Album {
	if NavLink == "" {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}

	var albums []Album
	fetched := 0
	for offset := 0; fetched < limit; offset += 10 {
		body, err := navRequest("/api/get-music?q=" + url.QueryEscape(fixEncoding(query)) + "&offset=" + strconv.Itoa(offset))
		if err != nil {
			if Debug {
				fmt.Println("Navidrums search failed:", err)
			}
			break
		}
		items := gjson.GetBytes(body, "data.albums.items").Array()
		if len(items) == 0 {
			break
		}
		for _, value := range items {
			albums = append(albums, navAlbumFromItem(value))
			fetched++
			if fetched >= limit {
				break
			}
		}
		if len(items) < 10 {
			break
		}
	}
	return albums
}

func navAlbumFromItem(value gjson.Result) Album {
	var album Album
	album.Artist = value.Get("artist.name").String()
	album.Title = value.Get("title").String()
	album.Edition = value.Get("version").String()
	album.Publisher = value.Get("label.name").String()
	if album.Publisher == "" {
		if label := value.Get("label"); label.Type == gjson.String {
			album.Publisher = label.String()
		}
	}
	album.CoverUrl = value.Get("image.small").String()
	album.Id = value.Get("id").String()
	if album.Id == "" {
		album.Id = value.Get("qobuz_id").String()
	}

	sr := value.Get("maximum_sampling_rate").Float()
	if sr > 0 && sr < 1000 {
		sr *= 1000
	}
	album.SamplingRate = int64(sr)
	album.BitDepth = value.Get("maximum_bit_depth").Int()
	album.NumTracks = value.Get("tracks_count").Int()
	album.Channels = value.Get("maximum_channel_count").Int()
	album.Duration = value.Get("duration").Int()
	album.Genre = value.Get("genre.name").String()
	album.QobuzUrl = value.Get("url").String()
	album.ReleaseDate = value.Get("released_at").Int()

	sizeSr := album.SamplingRate
	if sizeSr == 0 {
		sizeSr = 44100
	}
	sizeBd := album.BitDepth
	if sizeBd == 0 {
		sizeBd = 16
	}
	ch := album.Channels
	if ch == 0 {
		ch = 2
	}
	album.Size = int64(float64((sizeSr*sizeBd*ch*album.Duration)/8) * 0.7)

	return album
}

// navGetAlbum fetches an album (full track list) by its alphanumeric catalog id.
func navGetAlbum(catalogID string) (string, error) {
	body, err := navRequest("/api/get-album?album_id=" + url.QueryEscape(catalogID))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// navGetAlbumByHints resolves an album when the caller only has the numeric
// qobuz_id (Lidarr passes back the id from the indexer result) or when the id
// is unknown. It searches artist+title, prefers an exact qobuz_id match, then an
// exact catalog-id match, then a normalized title+artist match.
func navGetAlbumByHints(albumID, artist, title string) (string, error) {
	if NavLink == "" {
		return "", fmt.Errorf("no Navidrums backend configured")
	}

	queries := []string{}
	if artist != "" && title != "" {
		queries = append(queries, artist+" "+title)
	}
	if title != "" {
		queries = append(queries, title+" "+artist)
	}
	if artist != "" {
		queries = append(queries, artist)
	}

	norm := func(s string) string {
		return strings.ToLower(strings.TrimSpace(s))
	}

	for _, query := range queries {
		body, err := navRequest("/api/get-music?q=" + url.QueryEscape(fixEncoding(query)) + "&offset=0")
		if err != nil {
			if Debug {
				fmt.Println("Navidrums album search failed for '"+query+"':", err)
			}
			continue
		}

		items := gjson.GetBytes(body, "data.albums.items").Array()
		var fallback string
		for _, item := range items {
			catalogID := item.Get("id").String()
			qobuzID := item.Get("qobuz_id").String()
			if catalogID == "" {
				continue
			}
			if albumID != "" && (qobuzID == albumID || catalogID == albumID) {
				return navGetAlbum(catalogID)
			}
			if fallback == "" && artist != "" &&
				strings.EqualFold(norm(item.Get("artist.name").String()), norm(artist)) &&
				strings.Contains(norm(item.Get("title").String()), norm(title)) {
				fallback = catalogID
			}
		}
		if fallback != "" {
			return navGetAlbum(fallback)
		}
	}

	return "", fmt.Errorf("album not found on Navidrums: %s - %s (id %s)", artist, title, albumID)
}

// navGetStreamUrl resolves a FULL-LENGTH stream URL for a track. The returned
// format id is parsed from the served URL (fmt=6/7/27/5) so callers can label
// the release with the bytes they actually got, not the quality they asked for.
func navGetStreamUrl(trackID int, quality string) (string, int, error) {
	if NavLink == "" {
		return "", 0, fmt.Errorf("no Navidrums backend configured")
	}

	wanted := navQuality(quality)
	body, err := navRequest("/api/download-music?track_id=" + strconv.Itoa(trackID) + "&quality=" + wanted)
	if err != nil {
		return "", 0, err
	}

	streamURL := gjson.GetBytes(body, "data.url").String()
	if streamURL == "" {
		streamURL = gjson.GetBytes(body, "url").String()
	}
	if streamURL == "" {
		if backendErr := gjson.GetBytes(body, "error").String(); backendErr != "" {
			// The backend explains the refusal ("Account is blocked", "token sin
			// licencia ... devuelve preview de 30s"); dropping it leaves the caller
			// with an unactionable "no stream url".
			return "", 0, fmt.Errorf("navidrums refused track %d: %s", trackID, backendErr)
		}
		return "", 0, fmt.Errorf("navidrums returned no stream url for track %d", trackID)
	}
	if strings.Contains(streamURL, "range=") {
		return "", 0, fmt.Errorf("navidrums returned a preview url for track %d", trackID)
	}

	actual := int(gjson.GetBytes(body, "data.format_id").Int())
	if actual == 0 {
		actual = formatFromURL(streamURL)
	}
	if actual == 0 {
		actual, _ = strconv.Atoi(wanted)
	}
	return streamURL, actual, nil
}

// formatFromURL extracts Qobuz's fmt= parameter from a signed stream URL.
func formatFromURL(streamURL string) int {
	parsed, err := url.Parse(streamURL)
	if err != nil {
		return 0
	}
	fmtStr := parsed.Query().Get("fmt")
	if fmtStr == "" {
		return 0
	}
	value, err := strconv.Atoi(fmtStr)
	if err != nil {
		return 0
	}
	return value
}
