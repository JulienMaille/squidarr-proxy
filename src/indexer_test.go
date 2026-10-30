package main

import "testing"

func TestReleaseNameIncludesProviderTag(t *testing.T) {
	qobuz := releaseName(Album{Artist: "Artist", Title: "Album", ReleaseDate: 946684800, BitDepth: 16, SamplingRate: 44100, Source: "qobuz"}, "7")
	if qobuz != "Artist-Album-16BIT-44.1-KHZ-WEB-FLAC-2000-QOBUZ" {
		t.Errorf("Qobuz release name = %q", qobuz)
	}
	deezer := releaseName(Album{Artist: "Artist", Title: "Album", ReleaseDate: 946684800, BitDepth: 16, SamplingRate: 44100, Source: "deezer"}, "7")
	if deezer != "Artist-Album-16BIT-44.1-KHZ-WEB-FLAC-2000-DEEZER" {
		t.Errorf("Deezer release name = %q", deezer)
	}
}

func TestReleaseFolderRecognitionKeepsLegacyNames(t *testing.T) {
	for _, name := range []string{"Old-SQUIDWTF", "Album-QOBUZ", "Album-DEEZER"} {
		if !isSquidarrReleaseFolder(name) {
			t.Errorf("expected %q to be recognized", name)
		}
	}
	if isSquidarrReleaseFolder("unrelated-folder") {
		t.Error("unrelated folder should not be recognized")
	}
}

func TestMergeProviderAlbumsInterleavesSources(t *testing.T) {
	qobuz := []Album{{Id: "q1"}, {Id: "q2"}, {Id: "q3"}}
	deezer := []Album{{Id: "d1"}, {Id: "d2"}, {Id: "d3"}}
	got := mergeProviderAlbums(qobuz, deezer)
	want := []string{"q1", "d1", "q2", "d2", "q3", "d3"}
	if len(got) != len(want) {
		t.Fatalf("got %d merged results, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].Id != id {
			t.Errorf("result %d = %q, want %q", i, got[i].Id, id)
		}
	}
}

func TestMergeProviderAlbumsKeepsRemainder(t *testing.T) {
	got := mergeProviderAlbums([]Album{{Id: "q1"}, {Id: "q2"}}, []Album{{Id: "d1"}})
	want := []string{"q1", "d1", "q2"}
	if len(got) != len(want) {
		t.Fatalf("got %d merged results, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].Id != id {
			t.Errorf("result %d = %q, want %q", i, got[i].Id, id)
		}
	}
}
