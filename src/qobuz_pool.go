package main

import (
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// A Qobuz backend that answers search but refuses every stream is the normal way
// this ecosystem dies, and it costs the user a download that can never finish. The
// indexer can ask whether the backend can actually stream before offering its
// albums, so the answer is cached for a few minutes: a pool that recovers is
// picked up on the next probe, and a healthy backend is not probed on every search.
var qobuzPool = struct {
	sync.Mutex
	checked time.Time
	usable  bool
	reason  string
}{usable: true}

const qobuzPoolProbeTTL = 10 * time.Minute

// qobuzPoolRefusal distinguishes "the pool is dead" from "this one track was
// refused". A per-track 403 is routine, so it must not hide a whole provider.
func qobuzPoolRefusal(reason string) bool {
	lower := strings.ToLower(reason)
	for _, marker := range []string{"account is blocked", "user_blocked", "sin licencia", "preview", "token", "401", "4033", "unlicensed"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func qobuzPoolProbe() bool {
	if NavLink == "" {
		return false
	}
	albums := navSearchAlbums(qobuzPoolProbeQuery, 3)
	if len(albums) == 0 {
		// Search itself failing says nothing about streaming; keep offering what we
		// have rather than hiding the provider on a transient search error.
		return true
	}
	body, err := navGetAlbumByHints(albums[0].Id, albums[0].Artist, albums[0].Title)
	if err != nil {
		return true
	}
	items := gjson.Get(body, "data.tracks.items")
	if !items.Exists() {
		return true
	}
	var reasons []string
	for _, track := range items.Array() {
		id := track.Get("id").Int()
		if id == 0 {
			continue
		}
		if _, _, err := navGetStreamUrl(int(id), "7"); err == nil {
			return true
		} else {
			reasons = append(reasons, err.Error())
		}
		// Two different ids are enough: a refusal that repeats across tracks is the
		// pool, not the track.
		if len(reasons) >= 2 {
			break
		}
	}
	if len(reasons) < 2 {
		return true
	}
	systemic := false
	for _, reason := range reasons {
		if !qobuzPoolRefusal(reason) {
			return true
		}
		systemic = true
	}
	if systemic {
		return false
	}
	return true
}

const qobuzPoolProbeQuery = "Daft Punk Random Access Memories"

func qobuzStreamUsable() bool {
	qobuzPool.Lock()
	defer qobuzPool.Unlock()
	if time.Since(qobuzPool.checked) < qobuzPoolProbeTTL {
		return qobuzPool.usable
	}
	qobuzPool.checked = time.Now()
	usable := qobuzPoolProbe()
	if !usable {
		qobuzPool.reason = "the backend answered search but refused two tracks in a row"
	}
	qobuzPool.usable = usable
	return usable
}

func qobuzUnavailableReason() string {
	qobuzPool.Lock()
	defer qobuzPool.Unlock()
	return qobuzPool.reason
}
