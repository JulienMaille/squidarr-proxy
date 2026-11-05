package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type poolAlbum struct {
	ID      string `json:"id"`
	QobuzID string `json:"qobuz_id"`
	Title   string `json:"title"`
	Artist  struct {
		Name string `json:"name"`
	} `json:"artist"`
}

func poolServer(t *testing.T, stream func(id string) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/get-music"):
			album := poolAlbum{ID: "cat01", QobuzID: "9100001", Title: "Fake Album"}
			album.Artist.Name = "Fake Artist"
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"albums": map[string]any{"items": []poolAlbum{album}},
			}})
		case strings.HasPrefix(r.URL.Path, "/api/get-album"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"title": "Fake Album",
				"tracks": map[string]any{"items": []map[string]any{
					{"id": 9101001, "title": "One", "duration": 30},
					{"id": 9101002, "title": "Two", "duration": 30},
				}},
			}})
		case strings.HasPrefix(r.URL.Path, "/api/download-music"):
			id := r.URL.Query().Get("track_id")
			code, body := stream(id)
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestQobuzStreamUsableWhenABackendStreams(t *testing.T) {
	server := poolServer(t, func(string) (int, string) {
		return http.StatusOK, `{"success":true,"data":{"url":"http://x/track.flac","format_id":6}}`
	})
	defer server.Close()

	oldLink := NavLink
	defer func() { NavLink = oldLink }()
	NavLink = server.URL
	resetQobuzPoolForTest(t)

	if !qobuzStreamUsable() {
		t.Fatal("a backend that answers a stream must stay offered")
	}
}

func TestQobuzStreamUnusableWhenEveryTrackIsRefused(t *testing.T) {
	server := poolServer(t, func(string) (int, string) {
		return http.StatusBadRequest, `{"success":false,"error":"Qobuz API 403: Account is blocked"}`
	})
	defer server.Close()

	oldLink := NavLink
	defer func() { NavLink = oldLink }()
	NavLink = server.URL
	resetQobuzPoolForTest(t)

	if qobuzStreamUsable() {
		t.Fatal("a backend whose pool is blocked must not be offered")
	}
	if albums := fetchQobuzAlbums("anything", 10, 0, "7"); len(albums) != 0 {
		t.Errorf("albums must not be listed while the pool is dead: %+v", albums)
	}
}

func TestQobuzStreamUsableWhenOneTrackAnswers(t *testing.T) {
	// A per-track 403 is routine in this ecosystem: one track refusing must not
	// take the whole provider down.
	seen := 0
	server := poolServer(t, func(string) (int, string) {
		seen++
		if seen == 1 {
			return http.StatusBadRequest, `{"success":false,"error":"Qobuz API 403: Account is blocked"}`
		}
		return http.StatusOK, `{"success":true,"data":{"url":"http://x/track.flac","format_id":6}}`
	})
	defer server.Close()

	oldLink := NavLink
	defer func() { NavLink = oldLink }()
	NavLink = server.URL
	resetQobuzPoolForTest(t)

	if !qobuzStreamUsable() {
		t.Fatal("a single refused track is not a dead pool")
	}
}

func resetQobuzPoolForTest(t *testing.T) {
	t.Helper()
	qobuzPool.Lock()
	defer qobuzPool.Unlock()
	qobuzPool.checked = time.Time{}
	qobuzPool.usable = true
	qobuzPool.reason = ""
}
