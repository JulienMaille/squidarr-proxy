package main

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func withNav(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	previous := NavLink
	NavLink = server.URL
	t.Cleanup(func() {
		NavLink = previous
		server.Close()
	})
}

func TestIsPreviewLength(t *testing.T) {
	cases := []struct {
		name     string
		measured float64
		expected float64
		want     bool
	}{
		{"30s sample of a 4min track", 30, 240, true},
		{"full 4min track", 240, 240, false},
		{"genuine 30s interlude", 30.7, 31, false},
		{"sample with unknown catalog duration", 30, 0, true},
		{"long unknown-duration track", 200, 0, false},
	}
	for _, c := range cases {
		if got := isPreviewLength(c.measured, c.expected); got != c.want {
			t.Errorf("%s: isPreviewLength(%.1f, %.1f) = %v, want %v", c.name, c.measured, c.expected, got, c.want)
		}
	}
}

func TestNavQualityMapping(t *testing.T) {
	cases := map[string]string{
		"5":  "5",
		"6":  "6",
		"7":  "6",
		"27": "27",
		"":   "27",
	}
	for input, want := range cases {
		if got := navQuality(input); got != want {
			t.Errorf("navQuality(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatFromURL(t *testing.T) {
	url := "https://streaming-qobuz-std.akamaized.net/file?uid=468453&eid=1068442&fmt=7&profile=raw&etsp=1792779744"
	if got := formatFromURL(url); got != 7 {
		t.Errorf("formatFromURL = %d, want 7", got)
	}
	if got := formatFromURL("https://example.com/file"); got != 0 {
		t.Errorf("formatFromURL without fmt = %d, want 0", got)
	}
}

func TestNavSearchAlbums(t *testing.T) {
	withNav(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/get-music" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "Daft Punk Discovery" {
			t.Errorf("unexpected query %q", r.URL.Query().Get("q"))
		}
		w.Write([]byte(`{"success":true,"data":{"albums":{"items":[
			{"id":"0724384960650","qobuz_id":3142537,"title":"Discovery","tracks_count":14,
			 "duration":3660,"maximum_bit_depth":16,"maximum_sampling_rate":44.1,
			 "maximum_channel_count":2,"artist":{"name":"Daft Punk"},
			 "image":{"small":"https://img/cover.jpg"},"label":{"name":"Parlophone"},
			 "url":"https://play.qobuz.com/album/0724384960650","released_at":978307200}]}}}`))
	})

	albums := navSearchAlbums("Daft Punk Discovery", 10)
	if len(albums) != 1 {
		t.Fatalf("expected 1 album, got %d", len(albums))
	}
	album := albums[0]
	if album.Id != "0724384960650" {
		t.Errorf("Id = %q, want the alphanumeric catalog id", album.Id)
	}
	if album.Artist != "Daft Punk" || album.Title != "Discovery" {
		t.Errorf("artist/title = %q/%q", album.Artist, album.Title)
	}
	if album.SamplingRate != 44100 {
		t.Errorf("SamplingRate = %d, want 44100 (kHz normalised)", album.SamplingRate)
	}
	if album.BitDepth != 16 || album.NumTracks != 14 {
		t.Errorf("bit depth/tracks = %d/%d", album.BitDepth, album.NumTracks)
	}
	if album.Size <= 0 {
		t.Errorf("Size = %d, want a positive estimate", album.Size)
	}
}

func TestNavGetAlbumByHintsResolvesNumericID(t *testing.T) {
	var albumRequests []string
	withNav(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get-music":
			w.Write([]byte(`{"success":true,"data":{"albums":{"items":[
				{"id":"0724384960650","qobuz_id":3142537,"title":"Discovery","artist":{"name":"Daft Punk"}}]}}}`))
		case "/api/get-album":
			albumRequests = append(albumRequests, r.URL.Query().Get("album_id"))
			w.Write([]byte(`{"success":true,"data":{"title":"Discovery","maximum_bit_depth":16,
				"maximum_sampling_rate":44.1,"tracks":{"items":[
				{"id":1068442,"title":"One More Time","duration":320,"track_number":1,"media_number":1,"isrc":"GBDUW0000053"}]}}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	body, err := navGetAlbumByHints("3142537", "Daft Punk", "Discovery")
	if err != nil {
		t.Fatalf("navGetAlbumByHints: %v", err)
	}
	if len(albumRequests) != 1 || albumRequests[0] != "0724384960650" {
		t.Fatalf("get-album called with %v, want the catalog id", albumRequests)
	}
	tracks := gjson.Get(body, "data.tracks.items").Array()
	if len(tracks) != 1 || tracks[0].Get("title").String() != "One More Time" {
		t.Fatalf("unexpected tracks in body: %s", body)
	}
}

func TestNavGetAlbumByHintsFallsBackToTitleMatch(t *testing.T) {
	withNav(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get-music":
			w.Write([]byte(`{"success":true,"data":{"albums":{"items":[
				{"id":"abc123","qobuz_id":999,"title":"Discovery (Remastered)","artist":{"name":"daft punk"}}]}}}`))
		case "/api/get-album":
			if id := r.URL.Query().Get("album_id"); id != "abc123" {
				t.Errorf("album_id = %q, want abc123", id)
			}
			w.Write([]byte(`{"success":true,"data":{"tracks":{"items":[]}}}`))
		}
	})

	if _, err := navGetAlbumByHints("unknown", "Daft Punk", "Discovery"); err != nil {
		t.Fatalf("expected fuzzy title match, got %v", err)
	}
}

func TestNavGetStreamUrlParsesServedFormat(t *testing.T) {
	withNav(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/download-music" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("track_id") != "1068442" {
			t.Errorf("track_id = %q", r.URL.Query().Get("track_id"))
		}
		if r.URL.Query().Get("quality") != "6" {
			t.Errorf("quality = %q, want 6 for a CD-lossless request", r.URL.Query().Get("quality"))
		}
		w.Write([]byte(`{"success":true,"data":{"url":"https://streaming-qobuz-std.akamaized.net/file?eid=1068442&fmt=7&etsp=1"}}`))
	})

	streamURL, format, err := navGetStreamUrl(1068442, "7")
	if err != nil {
		t.Fatalf("navGetStreamUrl: %v", err)
	}
	if !strings.Contains(streamURL, "fmt=7") {
		t.Errorf("url = %q", streamURL)
	}
	if format != 7 {
		t.Errorf("format = %d, want 7 parsed from the served URL", format)
	}
}

func TestNavGetStreamUrlRejectsPreview(t *testing.T) {
	withNav(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"url":"https://streaming-qobuz-std.akamaized.net/file?fmt=5&range=20-30"}}`))
	})

	if _, _, err := navGetStreamUrl(1, "6"); err == nil {
		t.Fatal("expected preview URLs to be rejected")
	}
}

func writeStreamInfo(t *testing.T, prefix []byte, rate uint64, samples uint64) string {
	t.Helper()
	packed := rate<<44 | uint64(1)<<41 | uint64(15)<<36 | samples
	data := append([]byte{}, prefix...)
	data = append(data, 0x80, 0x00, 0x00, 0x22)
	data = append(data, make([]byte, 10)...)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, packed)
	data = append(data, buf...)
	path := filepath.Join(t.TempDir(), "track.flac")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp flac: %v", err)
	}
	return path
}

func TestFlacDurationSecondsPlain(t *testing.T) {
	path := writeStreamInfo(t, []byte("fLaC"), 44100, 44100*200)
	if got := flacDurationSeconds(path); got < 199 || got > 201 {
		t.Errorf("duration = %.2f, want ~200", got)
	}
}

func TestFlacDurationSecondsMP4Container(t *testing.T) {
	prefix := append([]byte("fLaC\x00\x00\x00\x00"), make([]byte, 34)...)
	prefix = append(prefix, []byte("dfLa\x00\x00\x00\x00")...)
	path := writeStreamInfo(t, prefix, 96000, 96000*301)
	if got := flacDurationSeconds(path); got < 300 || got > 302 {
		t.Errorf("duration = %.2f, want ~301 (dfLa container)", got)
	}
}

func TestFlacDurationSecondsRejectsSample(t *testing.T) {
	path := writeStreamInfo(t, []byte("fLaC"), 44100, 44100*30)
	if got := flacDurationSeconds(path); got < 29 || got > 31 {
		t.Errorf("duration = %.2f, want ~30 so the sample guard can flag it", got)
	}
}
