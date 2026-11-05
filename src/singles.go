package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// A single track: the unit the client's "Singles" tab searches and the /api/track
// endpoint downloads. Lidarr only understands albums, so this exists for the
// client and for "download this one song", not for the indexer feed as a whole.
type SingleTrack struct {
	Source       string
	Artist       string
	Title        string
	Album        string
	AlbumArtist  string
	CoverUrl     string
	TrackID      string
	Duration     int64
	ReleaseDate  int64
	Size         int64
	Quality      string
	WebUrl       string
	SamplingRate int64
	BitDepth     int64
}

func singleTrackSize(duration int64, lossless bool) int64 {
	if lossless {
		return int64(float64(duration) * 44100 * 16 * 2 / 8 * 0.7)
	}
	return duration * 320 * 1000 / 8
}

func tidalSingleFromSearch(item gjson.Result) SingleTrack {
	album := item.Get("album")
	duration := item.Get("duration").Int()
	released, _ := time.Parse("2006-01-02", firstNonEmpty(item.Get("releaseDate").String(), album.Get("releaseDate").String()))
	return SingleTrack{
		Source:       "tidal",
		Artist:       firstNonEmpty(item.Get("artist.name").String(), tidalArtistName(item)),
		Title:        item.Get("title").String(),
		Album:        album.Get("title").String(),
		CoverUrl:     tidalCoverURL(firstNonEmpty(item.Get("album.cover").String(), album.Get("cover").String())),
		TrackID:      item.Get("id").String(),
		Duration:     duration,
		ReleaseDate:  released.Unix(),
		Size:         singleTrackSize(duration, tidalQuality != "HIGH"),
		Quality:      tidalQualityCode(),
		WebUrl:       item.Get("url").String(),
		SamplingRate: 44100,
		BitDepth:     16,
	}
}

func deezerSingleFromSearch(item gjson.Result) SingleTrack {
	album := item.Get("album")
	duration := item.Get("duration").Int()
	released, _ := time.Parse("2006-01-02", album.Get("release_date").String())
	return SingleTrack{
		Source:      "deezer",
		Artist:      item.Get("artist.name").String(),
		Title:       item.Get("title").String(),
		Album:       album.Get("title").String(),
		CoverUrl:    album.Get("cover_xl").String(),
		TrackID:     item.Get("id").String(),
		Duration:    duration,
		ReleaseDate: released.Unix(),
		Size:        singleTrackSize(duration, true),
		Quality:     "7",
		WebUrl:      item.Get("link").String(),
		// Deezer streams decrypted FLAC, so the release is named like a 16/44.1 one.
		SamplingRate: 44100,
		BitDepth:     16,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func tidalSearchTracks(query string, limit, offset int) []SingleTrack {
	if !tidalConfigured() {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	seen := make(map[string]bool)
	var tracks []SingleTrack
	for _, variant := range tidalSearchVariants(query) {
		if len(tracks) >= limit {
			break
		}
		params := url.Values{
			"s":      {variant},
			"limit":  {strconv.Itoa(limit)},
			"offset": {strconv.Itoa(offset)},
		}
		result, err := tidalJSON("/search/?" + params.Encode())
		if err != nil {
			if Debug {
				fmt.Println("Tidal track search failed:", err)
			}
			continue
		}
		// A text search answers with tracks directly under data.items; an
		// album-shaped entry carries no album of its own, so it drops out.
		for _, item := range result.Get("data.items").Array() {
			if !item.Get("album.title").Exists() {
				continue
			}
			id := item.Get("id").String()
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tracks = append(tracks, tidalSingleFromSearch(item))
		}
	}
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}
	return tracks
}

func deezerSearchTracks(query string, limit, offset int) []SingleTrack {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	seen := make(map[string]bool)
	var tracks []SingleTrack
	for _, variant := range deezerSearchVariants(query) {
		if len(tracks) >= limit {
			break
		}
		params := url.Values{
			"q":      {variant},
			"limit":  {strconv.Itoa(limit)},
			"index":  {strconv.Itoa(offset)},
			"output": {"json"},
		}
		result, err := deezerJSON("/search/track?" + params.Encode())
		if err != nil {
			if Debug {
				fmt.Println("Deezer track search failed:", err)
			}
			continue
		}
		for _, item := range result.Get("data").Array() {
			id := item.Get("id").String()
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tracks = append(tracks, deezerSingleFromSearch(item))
		}
	}
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}
	return tracks
}

// searchSingleTracks asks only the sources that can currently stream a single
// track on its own: Deezer always, Tidal when a hifi-api backend is configured.
func searchSingleTracks(query string, limit, offset int) []SingleTrack {
	if limit <= 0 {
		limit = 10
	}
	sources := [][]SingleTrack{deezerSearchTracks(query, limit+offset, 0)}
	if tidalConfigured() {
		sources = append(sources, tidalSearchTracks(query, limit+offset, 0))
	}
	total := 0
	for _, s := range sources {
		total += len(s)
	}
	combined := make([]SingleTrack, 0, total)
	// Interleave so one provider cannot crowd the page out.
	for i := 0; ; i++ {
		appended := false
		for _, s := range sources {
			if i < len(s) {
				combined = append(combined, s[i])
				appended = true
			}
		}
		if !appended {
			break
		}
	}
	if offset > len(combined) {
		return nil
	}
	window := combined[min(offset, len(combined)):]
	if len(window) > limit {
		window = window[:limit]
	}
	fmt.Printf("Track search %q: Deezer=%d Tidal=%d\n", query, len(sources[0]), len(sources[1:]))
	return window
}

func trackItemFromSingle(t SingleTrack) Item {
	// Neither provider's track search carries a release date, so the feed names
	// the track without a year and the folder is named with the real one, once
	// the download has read the track's own metadata.
	yearStr := ""
	if t.ReleaseDate > 0 {
		yearStr = strconv.Itoa(time.Unix(t.ReleaseDate, 0).Year())
	}
	codec := tidalCodecFLAC
	if t.Quality == tidalAACQualityCode {
		codec = tidalCodecAAC
	}
	title := singleTrackReleaseName(t.Artist, t.Title, yearStr, t.Source, codec)
	lossless := t.Quality != "5" && t.Quality != tidalAACQualityCode
	categoryName := "Audio > Lossless"
	categoryAttrs := []NewznabAttr{
		{Name: "category", Value: "3000"},
		{Name: "category", Value: "3040"},
	}
	if !lossless {
		categoryName = "Audio > MP3"
		categoryAttrs = []NewznabAttr{
			{Name: "category", Value: "3000"},
			{Name: "category", Value: "3010"},
		}
	}
	categoryAttrs = append(categoryAttrs,
		NewznabAttr{Name: "size", Value: strconv.FormatInt(t.Size, 10)},
		NewznabAttr{Name: "tracks", Value: "1"},
		NewznabAttr{Name: "files", Value: "1"},
		NewznabAttr{Name: "duration", Value: strconv.FormatInt(t.Duration, 10)},
		NewznabAttr{Name: "year", Value: yearStr},
		NewznabAttr{Name: "coverurl", Value: t.CoverUrl},
		NewznabAttr{Name: "artist", Value: t.Artist},
		NewznabAttr{Name: "album", Value: t.Album},
		NewznabAttr{Name: "track", Value: t.Title},
		NewznabAttr{Name: "source", Value: t.Source},
	)
	enclosure := "/api/track?id=" + url.QueryEscape(t.TrackID) +
		"&source=" + t.Source +
		"&artist=" + url.QueryEscape(t.Artist) +
		"&title=" + url.QueryEscape(t.Title) +
		"&album=" + url.QueryEscape(t.Album) +
		"&apikey=" + ApiKey
	return Item{
		Title: title,
		Guid: Guid{
			IsPermaLink: t.WebUrl != "",
			Value:       t.WebUrl,
		},
		Link:        t.WebUrl,
		Comments:    t.WebUrl,
		PubDate:     time.Unix(t.ReleaseDate, 0).Format("Mon, 02 Jan 2006 15:04:05 -0700"),
		Category:    categoryName,
		Description: t.Artist + " " + t.Title,
		Enclosure: Enclosure{
			Url:  enclosure,
			Type: "application/x-nzb",
		},
		Attrs: categoryAttrs,
	}
}

func renderTrackResults(w http.ResponseWriter, u url.URL, query string) {
	limit, err := strconv.Atoi(u.Query().Get("limit"))
	if err != nil {
		limit = 10
	}
	offset, err := strconv.Atoi(u.Query().Get("offset"))
	if err != nil {
		offset = 0
	}
	tracks := searchSingleTracks(query, limit, offset)
	items := make([]Item, 0, len(tracks))
	for _, t := range tracks {
		items = append(items, trackItemFromSingle(t))
	}
	rss := Rss{
		Version: "2.0",
		Newznab: "http://www.newznab.com/DTD/2010/feeds/attributes/",
		Channel: Channel{
			Title:       "example.com",
			Description: "example.com API results",
			NewznabResponse: NewznabResponse{
				Offset: offset,
				Total:  len(items),
			},
			Items: items,
		},
	}
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(rss)
}

// handleTrackDownload downloads one track under complete/<category>/Artist-Title-...
// and answers immediately, like the album endpoint does.
func handleTrackDownload(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("apikey") != ApiKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"API Key Incorrect"}`))
		return
	}
	id := r.URL.Query().Get("id")
	source := strings.ToLower(r.URL.Query().Get("source"))
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"missing id parameter"}`))
		return
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid track id"}`))
		return
	}
	if source != "tidal" && source != "deezer" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unknown source"}`))
		return
	}
	jobID := "track-" + source + "-" + id
	if d, ok := Downloads[jobID]; ok && d.downloaded >= 0 && d.downloaded < d.numTracks {
		_, _ = w.Write([]byte(`{"status": "already_queued", "id": "` + jobID + `"}`))
		return
	}
	go downloadSingleTrack(source, id,
		r.URL.Query().Get("artist"), r.URL.Query().Get("title"), r.URL.Query().Get("album"))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status": "queued", "id": "` + jobID + `"}`))
}

func downloadSingleTrack(source, id, artist, title, album string) {
	jobID := "track-" + source + "-" + id
	d := &Download{Id: jobID, Source: source, FileName: id, Quality: "7", numTracks: 1, downloaded: -1}
	Downloads[jobID] = d

	var track File
	cover := ""
	codec := tidalCodecFLAC
	year := ""

	switch source {
	case "tidal":
		if !tidalConfigured() {
			fmt.Println("Tidal track download refused: TIDAL_API_LINK is not configured")
			return
		}
		// /track/ answers with the manifest only; /info/ carries the metadata.
		result, err := tidalJSON("/info/?id=" + id)
		if err != nil {
			fmt.Println("Tidal track failed:", err)
			return
		}
		data := result.Get("data")
		if !data.Exists() {
			fmt.Println("Tidal track response carried no data")
			return
		}
		if artist == "" {
			artist = firstNonEmpty(data.Get("artist.name").String(), tidalArtistName(data))
		}
		if title == "" {
			title = data.Get("title").String()
		}
		if album == "" {
			album = data.Get("album.title").String()
		}
		cover = tidalCoverURL(data.Get("album.cover").String())
		year = strings.Split(firstNonEmpty(data.Get("album.releaseDate").String(), data.Get("releaseDate").String()), "-")[0]
		track = File{Id: int(data.Get("id").Int()), Name: title, Index: "01", duration: data.Get("duration").Int(),
			isrc: data.Get("isrc").String(), mediaNumber: data.Get("volumeNumber").String()}
		// The folder names the tier the instance really serves, like an album does.
		if stream, _, err := tidalTrackManifest(track.Id); err == nil {
			codec = stream.codec
		}
	case "deezer":
		result, err := deezerJSON("/track/" + id)
		if err != nil {
			fmt.Println("Deezer track failed:", err)
			return
		}
		if artist == "" {
			artist = result.Get("artist.name").String()
		}
		if title == "" {
			title = result.Get("title").String()
		}
		if album == "" {
			album = result.Get("album.title").String()
		}
		cover = result.Get("album.cover_xl").String()
		year = strings.Split(result.Get("album.release_date").String(), "-")[0]
		track = File{Id: int(result.Get("id").Int()), Name: title, Index: "01", duration: result.Get("duration").Int(),
			isrc: result.Get("isrc").String()}
	}
	if track.Id == 0 {
		fmt.Println("Track download refused: the source returned no track id")
		return
	}

	d.Artist = artist
	d.Album = firstNonEmpty(album, title)
	d.Year = year
	d.UPC = ""
	d.CoverUrl = cover
	d.Files = []File{track}
	d.FileName = sanitizeFilename(singleTrackReleaseName(artist, title, year, source, codec))
	d.downloaded = 0
	startSingleTrackDownload(jobID)
}

func singleTrackReleaseName(artist, title, year, source, codec string) string {
	provider := releaseProviderTag(source)
	tier := "16BIT-44.1-KHZ-WEB-FLAC"
	if codec == tidalCodecAAC {
		tier = "WEB-AAC-320"
	}
	if year == "" {
		return artist + "-" + title + "-" + tier + "-" + provider
	}
	return artist + "-" + title + "-" + tier + "-" + year + "-" + provider
}

func startSingleTrackDownload(jobID string) {
	d := Downloads[jobID]
	if d == nil || d.downloaded < 0 || len(d.Files) == 0 {
		return
	}
	folder := filepath.Join(DownloadPath, "incomplete", Category, d.FileName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		fmt.Println("Couldn't create folder:", folder, err)
		d.downloaded = -1
		return
	}
	if d.CoverUrl != "" {
		_ = grabFile(filepath.Join(folder, "cover.jpg"), d.CoverUrl)
	}
	track := d.Files[0]
	stem := filepath.Join(folder, sanitizeFilename(d.Artist+" - "+track.Name))
	switch d.Source {
	case "tidal":
		path, err := tidalDownloadTrack(stem, track)
		if err != nil {
			fmt.Println("Tidal track download failed:", track.Name, err)
			d.downloaded = -1
			return
		}
		writeMetaData(*d, track, path)
	case "deezer":
		path := stem + ".flac"
		if err := deezerDownloadTrack(path, track); err != nil {
			fmt.Println("Deezer track download failed:", track.Name, err)
			_ = os.Remove(path)
			d.downloaded = -1
			return
		}
		writeMetaData(*d, track, path)
	default:
		fmt.Println("Track download refused: unknown source", d.Source)
		d.downloaded = -1
		return
	}
	dest := filepath.Join(DownloadPath, "complete", Category, d.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		fmt.Printf("Error creating destination category folder: %v\n", err)
		d.downloaded = -1
		return
	}
	if _, err := os.Stat(dest); err == nil {
		fmt.Println("Track download skipped, already complete:", dest)
		d.downloaded = 1
		return
	}
	if err := os.Rename(folder, dest); err != nil {
		fmt.Println("Track import staging failed:", err)
		d.downloaded = -1
		return
	}
	d.downloaded = 1
	fmt.Println("Track download complete:", d.FileName)
}
