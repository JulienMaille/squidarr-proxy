package main

import (
	"bytes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/crypto/blowfish"
)

func encryptDeezerTest(data []byte, id int) []byte {
	out := append([]byte(nil), data...)
	h := md5.Sum([]byte(strconv.Itoa(id)))
	s := hex.EncodeToString(h[:])
	key := make([]byte, 16)
	for i := range key {
		key[i] = s[i] ^ s[i+16] ^ "g4el58wc0zvf9na1"[i]
	}
	b, _ := blowfish.NewCipher(key)
	for off := 0; off+2048 <= len(out); off += 6144 {
		cipher.NewCBCEncrypter(b, []byte{0, 1, 2, 3, 4, 5, 6, 7}).CryptBlocks(out[off:off+2048], out[off:off+2048])
	}
	return out
}

func TestDeezerDecryptStripe(t *testing.T) {
	data := make([]byte, 13000)
	for i := range data {
		data[i] = byte(i * 37)
	}
	var out bytes.Buffer
	if err := deezerDecrypt(&out, bytes.NewReader(encryptDeezerTest(data, 3135556)), 3135556); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("decrypted bytes do not match")
	}
}

func TestDeezerRejectsInvalidAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(encryptDeezerTest(make([]byte, 8192), 123)) }))
	defer server.Close()
	oldURL, oldKey := deezerStreamURL, DeezerAPIKey
	defer func() { deezerStreamURL, DeezerAPIKey = oldURL, oldKey }()
	deezerStreamURL, DeezerAPIKey = server.URL, "test-secret"
	if err := deezerDownloadTrack(filepath.Join(t.TempDir(), "audio.flac"), File{Id: 123, duration: 180}); err == nil {
		t.Fatal("invalid audio accepted")
	}
}

func TestDeezerBlocksCrossHostRedirect(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer server.Close()
	oldURL, oldKey := deezerStreamURL, DeezerAPIKey
	defer func() { deezerStreamURL, DeezerAPIKey = oldURL, oldKey }()
	deezerStreamURL, DeezerAPIKey = server.URL, "test-secret"
	if err := deezerDownloadTrack(filepath.Join(t.TempDir(), "audio.flac"), File{Id: 123, duration: 180}); err == nil {
		t.Fatal("redirect accepted")
	}
	if called {
		t.Fatal("request reached redirect destination")
	}
}

func TestDeezerFailureNeverPromotesAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	oldURL, oldKey, oldPath, oldCategory := deezerStreamURL, DeezerAPIKey, DownloadPath, Category
	defer func() {
		deezerStreamURL, DeezerAPIKey, DownloadPath, Category = oldURL, oldKey, oldPath, oldCategory
		delete(Downloads, "test-failure")
	}()
	deezerStreamURL, DeezerAPIKey, DownloadPath, Category = server.URL, "invalid", t.TempDir(), "music"
	job := &Download{Id: "test-failure", FileName: "test-SQUIDWTF", numTracks: 1, Files: []File{{Id: 123, Name: "test", Index: "1", duration: 180}}}
	Downloads[job.Id] = job
	startDeezerDownload(job.Id)
	if job.downloaded != -1 {
		t.Fatal("failed job not marked failed")
	}
	if _, err := os.Stat(filepath.Join(DownloadPath, "complete", Category, job.FileName)); !os.IsNotExist(err) {
		t.Fatal("failed album promoted")
	}
}

func TestDeezerAlbumPagination(t *testing.T) {
	for _, scenario := range []string{"complete", "empty", "repeated", "error"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/album/123" {
					fmt.Fprint(w, `{"nb_tracks":3,"tracks":{"data":[{"id":1}]}}`)
					return
				}
				if r.URL.Path != "/album/123/tracks" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Query().Get("index") == "0" {
					fmt.Fprint(w, `{"data":[{"id":1,"title":"one"},{"id":2,"title":"two"}]}`)
					return
				}
				switch scenario {
				case "complete":
					fmt.Fprint(w, `{"data":[{"id":3,"title":"three"}]}`)
				case "empty":
					fmt.Fprint(w, `{"data":[]}`)
				case "repeated":
					fmt.Fprint(w, `{"data":[{"id":1,"title":"one"},{"id":2,"title":"two"}]}`)
				case "error":
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			oldURL := deezerCatalogURL
			defer func() { deezerCatalogURL = oldURL; delete(Downloads, "123") }()
			deezerCatalogURL = server.URL
			generateDeezerDownload("test", "123", "7")
			job := Downloads["123"]
			if scenario == "complete" {
				if job.downloaded != 0 || job.numTracks != 3 || job.Files[2].Id != 3 {
					t.Fatalf("pagination failed: %+v", job)
				}
				for i, track := range job.Files {
					if track.Index != strconv.Itoa(i+1) {
						t.Errorf("track %d number = %q, want %d", i, track.Index, i+1)
					}
				}
			} else if job.downloaded != -1 {
				t.Fatalf("incomplete album accepted: %+v", job)
			}
		})
	}
}

func TestDeezerLivePaginatedAlbum(t *testing.T) {
	if os.Getenv("DEEZER_LIVE_CATALOG_TEST") == "" {
		t.Skip("set DEEZER_LIVE_CATALOG_TEST for live probe")
	}
	defer delete(Downloads, "180792142")
	generateDeezerDownload("test", "180792142", "5")
	job := Downloads["180792142"]
	if job == nil || job.downloaded != 0 || job.numTracks != 30 {
		t.Fatalf("album pagination failed: %+v", job)
	}
}

func TestDeezerLiveTrack(t *testing.T) {
	key := os.Getenv("DEEZER_LIVE_TEST_KEY")
	if key == "" {
		t.Skip("set DEEZER_LIVE_TEST_KEY for live probe")
	}
	oldKey := DeezerAPIKey
	defer func() { DeezerAPIKey = oldKey }()
	DeezerAPIKey = key
	track, err := deezerJSON("/track/3135556")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "track.flac")
	if err := deezerDownloadTrack(path, File{Id: 3135556, duration: track.Get("duration").Int()}); err != nil {
		t.Fatal(err)
	}
	t.Logf("validated duration %.1f seconds", flacDurationSeconds(path))
}

func TestDeezerLiveAlbum(t *testing.T) {
	key := os.Getenv("DEEZER_LIVE_TEST_KEY")
	if key == "" {
		t.Skip("set DEEZER_LIVE_TEST_KEY for live probe")
	}
	oldKey, oldPath, oldCategory := DeezerAPIKey, DownloadPath, Category
	defer func() {
		DeezerAPIKey, DownloadPath, Category = oldKey, oldPath, oldCategory
		delete(Downloads, "302127")
	}()
	DeezerAPIKey, DownloadPath, Category = key, t.TempDir(), "music"
	generateDeezerDownload("test", "302127", "7")
	job := Downloads["302127"]
	if job == nil || job.downloaded < 0 || job.numTracks != 14 {
		t.Fatalf("album resolution failed: %+v", job)
	}
	startDeezerDownload("302127")
	if job.downloaded != 14 {
		t.Fatalf("album failed: %d/%d", job.downloaded, job.numTracks)
	}
	entries, err := os.ReadDir(filepath.Join(DownloadPath, "complete", Category, job.FileName))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".flac" {
			count++
		}
	}
	if count != 14 {
		t.Fatalf("got %d FLAC files", count)
	}
}

func TestDeezerLiveIndexer(t *testing.T) {
	if os.Getenv("DEEZER_LIVE_TEST_KEY") == "" {
		t.Skip("set DEEZER_LIVE_TEST_KEY for live probe")
	}
	oldProvider, oldQuality, oldKey := Provider, QualityId, ApiKey
	defer func() { Provider, QualityId, ApiKey = oldProvider, oldQuality, oldKey }()
	Provider, QualityId, ApiKey = "deezer", "7", "test"
	w := httptest.NewRecorder()
	u := url.URL{RawQuery: url.Values{"q": {"Daft Punk Discovery"}, "limit": {"2"}}.Encode()}
	renderSearchResults(w, u, "Daft Punk Discovery")
	if !bytes.Contains(w.Body.Bytes(), []byte("Daft Punk-Discovery-16BIT-44.1-KHZ-WEB-FLAC-2001-DEEZER")) {
		t.Fatalf("missing valid Discovery release: %s", w.Body.String())
	}
}

func TestDeezerFLACIndexerLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/album" {
			w.Write([]byte(`{"data":[{"id":1,"title":"Album","artist":{"name":"Artist"}}]}`))
			return
		}
		w.Write([]byte(`{"id":1,"title":"Album","artist":{"name":"Artist"},"nb_tracks":1,"duration":120,"release_date":"2001-01-01"}`))
	}))
	defer server.Close()
	oldURL, oldProvider, oldQuality := deezerCatalogURL, Provider, QualityId
	defer func() { deezerCatalogURL, Provider, QualityId = oldURL, oldProvider, oldQuality }()
	deezerCatalogURL, Provider, QualityId = server.URL, "deezer", "5"
	w := httptest.NewRecorder()
	u := url.URL{RawQuery: url.Values{"q": {"Artist Album"}}.Encode()}
	renderSearchResults(w, u, "Artist Album")
	if !bytes.Contains(w.Body.Bytes(), []byte("Artist-Album-16BIT-44.1-KHZ-WEB-FLAC-2001-DEEZER")) {
		t.Fatalf("missing FLAC label: %s", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`name="size" value="0"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`name="size" value="`)) {
		t.Fatalf("missing positive Deezer size estimate: %s", w.Body.String())
	}
}

func TestDeezerSearchVariants(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Matt Houston R&B 2 Rue", []string{"Matt Houston R&B 2 Rue", "matt houston r & b 2 rue"}},
		{"Matt Houston R B 2 rue", []string{"Matt Houston R B 2 rue", "matt houston r&b 2 rue"}},
		{"R&B 2 Rue", []string{"R&B 2 Rue", "r & b 2 rue"}},
		{"", nil},
	}
	for _, c := range cases {
		got := deezerSearchVariants(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("variants(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("variants(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestDeezerSearchVariantsDedup(t *testing.T) {
	got := deezerSearchVariants("R&B 2 Rue")
	if len(got) != 2 {
		t.Fatalf("expected 2 deduped variants, got %v", got)
	}
	if got[0] != "R&B 2 Rue" {
		t.Fatalf("unexpected first variant: %q", got[0])
	}
}

func TestDeezerLiveMattHoustonSearch(t *testing.T) {
	if os.Getenv("DEEZER_LIVE_CATALOG_TEST") == "" {
		t.Skip("set DEEZER_LIVE_CATALOG_TEST for live probe")
	}
	oldProvider, oldQuality := Provider, QualityId
	defer func() { Provider, QualityId = oldProvider, oldQuality }()
	Provider, QualityId = "deezer", "7"
	albums := deezerSearchAlbums("Matt Houston R&B 2 rue", 10, 0)
	if len(albums) == 0 {
		t.Fatal("no results for Matt Houston R&B 2 rue")
	}
	found := false
	for _, a := range albums {
		if a.Id == "433643" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Matt Houston R&B 2 Rue (433643) not found among results: %+v", albums)
	}
	t.Logf("found %d results", len(albums))
}
