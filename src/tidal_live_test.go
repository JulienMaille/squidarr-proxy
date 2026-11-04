package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

// The live tests download one real track through the configured proxy. They are
// opt-in because they need a hifi-api instance that is currently streaming:
//
//	TIDAL_LIVE=1 TIDAL_API_LINK=https://<host> go test -run TestLiveTidal -v
//
// Track 243673459 answers FULL on every public instance; a 403 is per track on
// this ecosystem, so a failing id says nothing about the account's health.
const liveTidalTrackID = 243673459

func liveTidalEndpoint(t *testing.T) {
	t.Helper()
	if os.Getenv("TIDAL_LIVE") == "" {
		t.Skip("set TIDAL_LIVE=1 to run against a live hifi-api instance")
	}
	link := os.Getenv("TIDAL_API_LINK")
	if link == "" {
		t.Skip("set TIDAL_API_LINK to a hifi-api host")
	}
	tidalEndpoints = []string{link}
}

func TestLiveTidalRemuxFlac(t *testing.T) {
	liveTidalEndpoint(t)
	tidalQuality = "LOSSLESS"

	stream, err := fetchTidalStream(liveTidalTrackID)
	if err != nil {
		t.Fatalf("fetchTidalStream: %v", err)
	}
	if stream.codec != tidalCodecFLAC {
		t.Fatalf("codec = %q, want flac for a LOSSLESS request", stream.codec)
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
	duration := tidalDurationSeconds(dest, stream.codec)
	t.Logf("flac: %d bytes, %.1fs, %d segments, manifest %.1fs",
		info.Size(), duration, len(stream.segmentURL), stream.streamDuration())
	if duration <= 0 {
		t.Fatal("the remuxed file is not a readable FLAC")
	}
	if math.Abs(duration-stream.streamDuration()) > 1 {
		t.Fatalf("file duration %.1fs disagrees with the manifest %.1fs", duration, stream.streamDuration())
	}
	if info.Size() < 1<<20 {
		t.Fatalf("suspiciously small output: %d bytes", info.Size())
	}
}

func TestLiveTidalRemuxAac(t *testing.T) {
	liveTidalEndpoint(t)
	tidalQuality = "HIGH"
	defer func() { tidalQuality = "LOSSLESS" }()

	stream, err := fetchTidalStream(liveTidalTrackID)
	if err != nil {
		t.Fatalf("fetchTidalStream: %v", err)
	}
	if stream.codec != tidalCodecAAC {
		t.Fatalf("codec = %q, want aac for a HIGH request", stream.codec)
	}
	if len(stream.segmentURL) == 0 {
		t.Fatal("no segments in the manifest")
	}

	dest := filepath.Join(t.TempDir(), "track.m4a")
	if err := writeTidalM4a(dest, stream); err != nil {
		t.Fatalf("writeTidalM4a: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	duration := mp4DurationSeconds(dest)
	t.Logf("aac: %d bytes, %.1fs, %d segments, manifest %.1fs",
		info.Size(), duration, len(stream.segmentURL), stream.streamDuration())
	if duration <= 0 {
		t.Fatal("the remuxed file declares no duration")
	}
	if math.Abs(duration-stream.streamDuration()) > 1 {
		t.Fatalf("file duration %.1fs disagrees with the manifest %.1fs", duration, stream.streamDuration())
	}

	// Lidarr identifies an import by its tags, so the .m4a has to survive
	// tagging: a fragmented MP4 is playable but is not a plain MP4.
	if err := taglib.WriteTags(dest, map[string][]string{
		taglib.Artist: {"Live Test"},
		taglib.Album:  {"Live Album"},
		taglib.Title:  {"Live Title"},
	}, 0); err != nil {
		t.Fatalf("tagging the fragmented m4a failed: %v", err)
	}
	tags, err := taglib.ReadTags(dest)
	if err != nil {
		t.Fatalf("reading the tags back: %v", err)
	}
	if len(tags[taglib.Title]) == 0 || tags[taglib.Title][0] != "Live Title" {
		t.Fatalf("title did not persist: %v", tags)
	}
}
