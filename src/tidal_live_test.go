package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLiveTidalTrackRemux downloads one real track through the configured proxy
// and checks the result is a playable FLAC. Opt-in, because it needs the public
// hifi-api instance to be alive:
//
//	TIDAL_LIVE=1 TIDAL_API_LINK=https://<host> go test -run TestLiveTidalTrackRemux -v
func TestLiveTidalTrackRemux(t *testing.T) {
	if os.Getenv("TIDAL_LIVE") == "" {
		t.Skip("set TIDAL_LIVE=1 to run against a live hifi-api instance")
	}
	link := os.Getenv("TIDAL_API_LINK")
	if link == "" {
		t.Skip("set TIDAL_API_LINK to a hifi-api host")
	}
	tidalEndpoints = []string{link}

	// 2 Minutes to Midnight (2024 remaster), 5:59.
	stream, err := fetchTidalStream(203237923)
	if err != nil {
		t.Fatalf("fetchTidalStream: %v", err)
	}
	if len(stream.segmentURL) == 0 {
		t.Fatal("no segments in the manifest")
	}

	dest := filepath.Join(t.TempDir(), "track.flac")
	if err := writeTidalFlac(dest, stream); err != nil {
		t.Fatalf("writeTidalFlac: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	duration := flacDurationSeconds(dest)
	t.Logf("remuxed %d bytes, %.1fs, %d segments", info.Size(), duration, len(stream.segmentURL))
	if duration <= 0 {
		t.Fatal("the remuxed file is not a readable FLAC")
	}
	if info.Size() < 1<<20 {
		t.Fatalf("suspiciously small output: %d bytes", info.Size())
	}
}
