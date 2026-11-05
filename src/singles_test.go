package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeCover struct {
	Title string `json:"title,omitempty"`
	Cover string `json:"cover,omitempty"`
}

type fakeTidalTrack struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	URL      string `json:"url"`
	Artist   struct {
		Name string `json:"name"`
	} `json:"artist"`
	Album fakeCover `json:"album"`
}

func tidalTrackSearch(tracks []fakeTidalTrack) string {
	items := make([]fakeTidalTrack, 0, len(tracks)+1)
	// The duplicate proves dedupe by id, so it is appended on purpose.
	items = append(items, tracks...)
	items = append(items, fakeTidalTrack{ID: tracks[len(tracks)-1].ID, Title: "duplicate", Duration: 1, Album: fakeCover{Title: "Dup"}})
	items = append(items, fakeTidalTrack{ID: 1, Title: "album shaped entry"})
	body, _ := json.Marshal(map[string]any{
		"version": "2.10",
		"data":    map[string]any{"items": items},
	})
	return string(body)
}

type fakeDeezerTrack struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Artist   struct {
		Name string `json:"name"`
	} `json:"artist"`
	Album struct {
		Title       string `json:"title"`
		CoverXL     string `json:"cover_xl"`
		ReleaseDate string `json:"release_date"`
	} `json:"album"`
}

func TestSearchSingleTracksInterleavesProviders(t *testing.T) {
	deezerTracks := []fakeDeezerTrack{
		{ID: 11, Title: "Nice Again", Duration: 199},
		{ID: 12, Title: "Rise Against Me", Duration: 227},
	}
	deezerTracks[0].Artist.Name = "Jahvinci"
	deezerTracks[0].Album.Title = "Scriptures Riddim"
	deezerTracks[0].Album.CoverXL = "http://x/11.jpg"
	deezerTracks[0].Album.ReleaseDate = "2013-03-12"
	deezerTracks[1].Artist.Name = "Jah Vinci"
	deezerTracks[1].Album.Title = "Good Memories Riddim"

	tidalTracks := []fakeTidalTrack{{ID: 36526921, Title: "Nice Again", Duration: 199, URL: "http://tidal/track/36526921"}}
	tidalTracks[0].Artist.Name = "Jahvinci"
	tidalTracks[0].Album.Title = "Scriptures Riddim"
	tidalTracks[0].Album.Cover = "aaaa-bbbb"

	tidal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(tidalTrackSearch(tidalTracks)))
	}))
	defer tidal.Close()

	deezer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/search/track") {
			t.Errorf("unexpected deezer path %s", r.URL.Path)
		}
		body, _ := json.Marshal(map[string]any{"data": deezerTracks})
		_, _ = w.Write([]byte(body))
	}))
	defer deezer.Close()

	oldDeezer, oldTidal, oldQuality := deezerCatalogURL, tidalEndpoints, tidalQuality
	defer func() { deezerCatalogURL, tidalEndpoints, tidalQuality = oldDeezer, oldTidal, oldQuality }()
	deezerCatalogURL = deezer.URL
	tidalEndpoints = []string{tidal.URL}
	tidalQuality = "LOSSLESS"

	tracks := searchSingleTracks("jah vinci nice again", 10, 0)
	if len(tracks) != 3 {
		t.Fatalf("expected 3 unique tracks, got %d (%+v)", len(tracks), tracks)
	}
	if tracks[0].Source != "deezer" || tracks[1].Source != "tidal" || tracks[2].Source != "deezer" {
		t.Fatalf("providers must interleave: %s, %s, %s", tracks[0].Source, tracks[1].Source, tracks[2].Source)
	}
	first := tracks[0]
	if first.Title != "Nice Again" || first.Artist != "Jahvinci" || first.Album != "Scriptures Riddim" {
		t.Errorf("wrong first track: %+v", first)
	}
	if first.Duration != 199 || first.CoverUrl != "http://x/11.jpg" {
		t.Errorf("duration/cover not carried: %+v", first)
	}
	if first.Quality != "7" {
		t.Errorf("deezer must advertise FLAC, got %q", first.Quality)
	}
	tidalTrack := tracks[1]
	if tidalTrack.TrackID != "36526921" || tidalTrack.WebUrl == "" {
		t.Errorf("tidal track not mapped: %+v", tidalTrack)
	}
	if !strings.Contains(tidalTrack.CoverUrl, "/images/aaaa/bbbb/") {
		t.Errorf("tidal cover uuid not expanded: %q", tidalTrack.CoverUrl)
	}
	if page := searchSingleTracks("jah vinci nice again", 2, 2); len(page) != 1 {
		t.Fatalf("offset window wrong: %+v", page)
	}
}

func TestSearchSingleTracksWithoutTidalBackend(t *testing.T) {
	oldDeezer, oldTidal := deezerCatalogURL, tidalEndpoints
	defer func() { deezerCatalogURL, tidalEndpoints = oldDeezer, oldTidal }()
	deezerCatalogURL = "https://api.deezer.com"
	tidalEndpoints = nil
	if tracks := searchSingleTracks("zzzznothingzzzz", 5, 0); len(tracks) != 0 {
		t.Errorf("expected no tracks, got %+v", tracks)
	}
}

func TestTrackItemFromSinglePointsAtTheTrackEndpoint(t *testing.T) {
	item := trackItemFromSingle(SingleTrack{
		Source: "deezer", Artist: "Jahvinci", Title: "Nice Again", Album: "Scriptures Riddim",
		TrackID: "11", Duration: 199, Size: 555, Quality: "7", WebUrl: "http://deezer/track/11",
	})
	if item.Title != "Jahvinci-Nice Again-16BIT-44.1-KHZ-WEB-FLAC-DEEZER" {
		t.Errorf("wrong release name: %q", item.Title)
	}
	if item.Category != "Audio > Lossless" {
		t.Errorf("FLAC track belongs in the lossless bucket, got %q", item.Category)
	}
	if !strings.HasPrefix(item.Enclosure.Url, "/api/track?id=11&source=deezer") {
		t.Errorf("enclosure must point at the track endpoint: %q", item.Enclosure.Url)
	}
	if !strings.Contains(item.Enclosure.Url, "title=Nice+Again") {
		t.Errorf("enclosure must carry the title: %q", item.Enclosure.Url)
	}
	lossy := trackItemFromSingle(SingleTrack{Source: "tidal", Artist: "A", Title: "B", Quality: tidalAACQualityCode})
	if lossy.Category != "Audio > MP3" {
		t.Errorf("AAC track belongs in the lossy bucket, got %q", lossy.Category)
	}
}

func TestSingleTrackReleaseNameCarriesTheTierServed(t *testing.T) {
	if name := singleTrackReleaseName("Run-DMC", "Down With The King", "1993", "tidal", tidalCodecFLAC); name != "Run-DMC-Down With The King-16BIT-44.1-KHZ-WEB-FLAC-1993-TIDAL" {
		t.Errorf("unexpected FLAC single name: %q", name)
	}
	if name := singleTrackReleaseName("Run-DMC", "Down With The King", "1993", "tidal", tidalCodecAAC); name != "Run-DMC-Down With The King-WEB-AAC-320-1993-TIDAL" {
		t.Errorf("unexpected AAC single name: %q", name)
	}
	if name := singleTrackReleaseName("Jah Vinci", "Rise Against Me", "2014", "deezer", tidalCodecFLAC); !strings.Contains(name, "16BIT-44.1-KHZ-WEB-FLAC-2014-DEEZER") {
		t.Errorf("unexpected Deezer single name: %q", name)
	}
	for _, name := range []string{
		singleTrackReleaseName("A", "B", "1999", "deezer", tidalCodecFLAC),
		singleTrackReleaseName("A", "B", "1999", "tidal", tidalCodecAAC),
	} {
		if !isSquidarrReleaseFolder(name) {
			t.Errorf("a single must be recognised as a squidarr release at startup: %q", name)
		}
	}
}
