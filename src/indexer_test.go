package main

import "testing"

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
