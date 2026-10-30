package main

import (
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/crypto/blowfish"
)

var deezerCatalogURL = "https://api.deezer.com"
var deezerStreamURL = "https://deezer.anandserver.cfd"

type deezerByteCounter int64

func (c *deezerByteCounter) Write(p []byte) (int, error) {
	*c += deezerByteCounter(len(p))
	return len(p), nil
}

func deezerJSON(path string) (gjson.Result, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Get(deezerCatalogURL + path)
	if err != nil {
		return gjson.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return gjson.Result{}, fmt.Errorf("deezer catalog HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || !json.Valid(body) {
		return gjson.Result{}, fmt.Errorf("invalid deezer catalog response")
	}
	result := gjson.ParseBytes(body)
	if result.Get("error").Exists() {
		return gjson.Result{}, fmt.Errorf("deezer catalog error: %s", result.Get("error.message").String())
	}
	return result, nil
}

func deezerSearchAlbums(query string, limit, offset int) []Album {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	seen := make(map[string]bool)
	var albums []Album

	appendResult := func(res gjson.Result) {
		for _, item := range res.Get("data").Array() {
			id := item.Get("id").String()
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if detail, err := deezerJSON("/album/" + id); err == nil {
				item = detail
			}
			date, _ := time.Parse("2006-01-02", item.Get("release_date").String())
			duration := item.Get("duration").Int()
			// Estimate FLAC size from Deezer's 16-bit/44.1 kHz stereo stream.
			// The 0.7 factor approximates lossless compression, as in Qobuz sizing.
			size := int64(float64(duration) * 44100 * 16 * 2 / 8 * 0.7)
			albums = append(albums, Album{Source: "deezer", Artist: item.Get("artist.name").String(), Title: item.Get("title").String(), Id: id,
				NumTracks: item.Get("nb_tracks").Int(), Duration: duration, Size: size, Channels: 2, QobuzUrl: item.Get("link").String(),
				CoverUrl: item.Get("cover_medium").String(), ReleaseDate: date.Unix(), BitDepth: 16, SamplingRate: 44100})
		}
	}

	variants := deezerSearchVariants(query)
	for _, variant := range variants {
		if len(albums) >= limit {
			break
		}
		result, err := deezerJSON("/search/album?" + url.Values{"q": {variant}, "limit": {strconv.Itoa(limit)}, "index": {strconv.Itoa(offset)}}.Encode())
		if err != nil {
			continue
		}
		appendResult(result)
	}

	if len(albums) == 0 {
		result, err := deezerJSON("/search/album?" + url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}, "index": {strconv.Itoa(offset)}}.Encode())
		if err == nil {
			appendResult(result)
		}
	}

	if len(albums) > limit {
		albums = albums[:limit]
	}
	return albums
}

// deezerSearchVariants builds a few candidate Deezer search queries so that
// an artist-only change (e.g. Matt -> Matt Houston) or a missing ampersand
// (e.g. "R B 2 Rue" instead of "R&B 2 Rue") can still surface the right album.
func deezerSearchVariants(query string) []string {
	var variants []string

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil
	}

	variants = append(variants, trimmed)

	normalized := deezerNormalizeQuery(trimmed)
	if normalized != trimmed {
		variants = append(variants, normalized)
	}

	variants = deezerDedupe(variants)
	if len(variants) > 8 {
		variants = variants[:8]
	}
	return variants
}

func deezerNormalizeQuery(query string) string {
	s := strings.ToLower(query)

	// Ampersand and its spoken forms map to "and".
	s = strings.ReplaceAll(s, "&", " and ")
	s = strings.ReplaceAll(s, " et ", " and ")
	s = strings.ReplaceAll(s, " n ", " and ")
	s = strings.ReplaceAll(s, " r n b ", " r&b ")
	s = strings.ReplaceAll(s, " r b ", " r&b ")
	s = strings.ReplaceAll(s, "and", "and")
	s = strings.ReplaceAll(s, "  ", " ")
	s = strings.TrimSpace(s)

	// Ampersand variant: keep "&" as well so both "&" and "and" are tried.
	if !strings.Contains(s, "&") && strings.Contains(s, " and ") {
		s = strings.ReplaceAll(s, " and ", " & ")
	}

	return s
}

func deezerDedupe(variants []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range variants {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func generateDeezerDownload(filename, id, _ string) {
	// Deezer streams are decrypted as FLAC; no external transcoder is required.
	quality := "7"
	d := &Download{Id: id, Source: "deezer", FileName: filename, Quality: quality, downloaded: -1}
	Downloads[id] = d
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		fmt.Println("Invalid Deezer album ID")
		return
	}
	data, err := deezerJSON("/album/" + id)
	if err != nil {
		fmt.Println("Deezer album failed:", err)
		return
	}
	d.Artist = data.Get("artist.name").String()
	d.Album = data.Get("title").String()
	d.CoverUrl = data.Get("cover_xl").String()
	d.Year = strings.Split(data.Get("release_date").String(), "-")[0]
	d.UPC = data.Get("upc").String()
	d.label = data.Get("label").String()
	tracks := data.Get("tracks.data").Array()
	expected := data.Get("nb_tracks").Int()
	if expected > int64(len(tracks)) {
		tracks = nil
		seenPages := make(map[string]bool)
		for int64(len(tracks)) < expected {
			page, err := deezerJSON("/album/" + id + "/tracks?" + url.Values{
				"index": {strconv.Itoa(len(tracks))}, "limit": {"25"},
			}.Encode())
			if err != nil {
				fmt.Println("Deezer album tracks failed:", err)
				return
			}
			items := page.Get("data").Array()
			if len(items) == 0 {
				break
			}
			ids := make([]string, len(items))
			for i, item := range items {
				ids[i] = item.Get("id").String()
			}
			pageKey := strings.Join(ids, ",")
			if seenPages[pageKey] {
				break
			}
			seenPages[pageKey] = true
			tracks = append(tracks, items...)
		}
	}
	for i, t := range tracks {
		trackNumber := t.Get("track_position").String()
		if trackNumber == "" || trackNumber == "0" {
			trackNumber = t.Get("track_number").String()
		}
		if trackNumber == "" || trackNumber == "0" {
			trackNumber = strconv.Itoa(i + 1)
		}
		d.Files = append(d.Files, File{Id: int(t.Get("id").Int()), Name: t.Get("title").String(),
			Index: trackNumber, mediaNumber: t.Get("disk_number").String(), duration: t.Get("duration").Int()})
	}
	if len(d.Files) == 0 || int64(len(d.Files)) != expected {
		fmt.Printf("Deezer album track list is incomplete: %d/%d\n", len(d.Files), expected)
		return
	}
	d.numTracks = len(d.Files)
	d.FileName = sanitizeFilename(d.Artist + "-" + d.Album + "-16BIT-44.1-KHZ-WEB-FLAC-" + d.Year + "-SQUIDWTF")
	d.downloaded = 0
}

func deezerDecrypt(dst io.Writer, src io.Reader, id int) error {
	hash := md5.Sum([]byte(strconv.Itoa(id)))
	hexhash := hex.EncodeToString(hash[:])
	key := make([]byte, 16)
	for i := range key {
		key[i] = hexhash[i] ^ hexhash[i+16] ^ "g4el58wc0zvf9na1"[i]
	}
	block, err := blowfish.NewCipher(key)
	if err != nil {
		return err
	}
	buf := make([]byte, 2048)
	for nblock := 0; ; nblock++ {
		n, readErr := io.ReadFull(src, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n == 0 {
			return nil
		}
		if nblock%3 == 0 && n == len(buf) {
			cipher.NewCBCDecrypter(block, []byte{0, 1, 2, 3, 4, 5, 6, 7}).CryptBlocks(buf[:n], buf[:n])
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return err
		}
		if readErr != nil {
			return nil
		}
	}
}

func deezerDownloadTrack(dest string, track File) error {
	base, err := url.Parse(deezerStreamURL)
	if err != nil || base.Host == "" {
		return fmt.Errorf("invalid Deezer stream URL")
	}
	endpoint := strings.TrimRight(deezerStreamURL, "/") + "/api/stream/" + strconv.Itoa(track.Id)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", DeezerAPIKey)
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if !strings.EqualFold(r.URL.Host, base.Host) || r.URL.Scheme != base.Scheme {
			return fmt.Errorf("refusing Deezer credential redirect")
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deezer stream HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	var received deezerByteCounter
	err = deezerDecrypt(f, io.TeeReader(resp.Body, &received), track.Id)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if resp.ContentLength >= 0 && int64(received) != resp.ContentLength {
		return fmt.Errorf("truncated Deezer stream: received %d of %d bytes", received, resp.ContentLength)
	}
	if closeErr != nil {
		return closeErr
	}
	file, err := os.Open(dest)
	if err != nil {
		return err
	}
	magic := make([]byte, 4)
	_, err = io.ReadFull(file, magic)
	file.Close()
	if err != nil || string(magic) != "fLaC" {
		return fmt.Errorf("decrypted audio is not native FLAC")
	}
	duration := flacDurationSeconds(dest)
	if duration <= 0 || track.duration <= 0 || math.Abs(duration-float64(track.duration)) > math.Max(5, float64(track.duration)*0.05) {
		return fmt.Errorf("invalid or truncated FLAC duration %.1f / %d", duration, track.duration)
	}
	return nil
}

func startDeezerDownload(id string) {
	d := Downloads[id]
	if d == nil || d.downloaded < 0 {
		return
	}
	folder := filepath.Join(DownloadPath, "incomplete", Category, d.FileName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		d.downloaded = -1
		return
	}
	if d.CoverUrl != "" {
		_ = grabFile(filepath.Join(folder, "cover.jpg"), d.CoverUrl)
	}
	for i, track := range d.Files {
		trackNumber := strings.TrimSpace(track.Index)
		if trackNumber == "" || trackNumber == "0" {
			trackNumber = strconv.Itoa(i + 1)
		}
		track.Index = trackNumber
		stem := filepath.Join(folder, sanitizeFilename(trackNumber+" - "+d.Artist+" - "+track.Name))
		path := stem + ".flac"
		if err := deezerDownloadTrack(path, track); err != nil {
			fmt.Println("Deezer download failed:", track.Name, err)
			_ = os.Remove(path)
			d.downloaded = -1
			return
		}
		writeMetaData(*d, track, path)
		if d.downloaded < d.numTracks-1 {
			d.downloaded++
		}
	}
	dest := filepath.Join(DownloadPath, "complete", Category, d.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		d.downloaded = -1
		return
	}
	if _, err := os.Stat(dest); err == nil {
		fmt.Println("Deezer destination already exists:", dest)
		d.downloaded = -1
		return
	}
	if err := os.Rename(folder, dest); err != nil {
		fmt.Println("Deezer import staging failed:", err)
		d.downloaded = -1
		return
	}
	d.downloaded = d.numTracks
	fmt.Println("Deezer download complete:", d.FileName)
}
