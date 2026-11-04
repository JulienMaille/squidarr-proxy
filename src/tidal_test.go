package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func parseJSONForTest(b []byte) gjson.Result { return gjson.ParseBytes(b) }

func ids(albums []Album) []string {
	out := make([]string, len(albums))
	for i, a := range albums {
		out[i] = a.Id
	}
	return out
}

// box builds an ISO-BMFF box exactly as the CDN does: 4-byte size, 4-byte type.
func box(typ string, payload []byte) []byte {
	size := 8 + len(payload)
	out := make([]byte, 8, size)
	out[0] = byte(size >> 24)
	out[1] = byte(size >> 16)
	out[2] = byte(size >> 8)
	out[3] = byte(size)
	copy(out[4:8], typ)
	return append(out, payload...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// streamInfoBlock builds a real 38-byte FLAC STREAMINFO metadata block (4-byte
// header + 34-byte payload) for the given sample rate and sample count.
func streamInfoBlock(sampleRate int, totalSamples uint64) []byte {
	p := make([]byte, 34)
	p[10] = byte(sampleRate >> 12)
	p[11] = byte(sampleRate >> 4)
	p[12] = byte((sampleRate&0x0F)<<4 | 1<<1) // 2 channels, upper bit of depth 16
	p[13] = byte(0x0F<<4) | byte((totalSamples>>32)&0x0F)
	p[14] = byte(totalSamples >> 24)
	p[15] = byte(totalSamples >> 16)
	p[16] = byte(totalSamples >> 8)
	p[17] = byte(totalSamples)
	header := []byte{0x80, 0x00, 0x00, byte(len(p))} // last block, type 0, length 34
	return concat(header, p)
}

// initSegment mimics Tidal's initialization segment: the FLAC sample entry
// carries the STREAMINFO in a dfLa box under stsd.
func initSegment(info []byte) []byte {
	dfLa := box("dfLa", concat([]byte{0, 0, 0, 0}, info))
	// 8 bytes of reserved + data_reference_index, then 20 bytes of
	// AudioSampleEntry fields, then child boxes.
	entryPayload := concat(make([]byte, 28), dfLa)
	sampleEntry := box("fLaC", entryPayload)
	stsd := box("stsd", concat(make([]byte, 8), sampleEntry))
	stbl := box("stbl", stsd)
	minf := box("minf", stbl)
	mdia := box("mdia", minf)
	trak := box("trak", mdia)
	moov := box("moov", trak)
	return concat(box("ftyp", []byte("iso8")), moov)
}

func mediaSegment(payload []byte) []byte {
	return concat(box("moof", box("mfhd", make([]byte, 8))), box("mdat", payload))
}

func mpdWith(serverURL, codecs string, startNumber, segments int) []byte {
	return mpdWithRepresentations(serverURL, []string{codecs}, startNumber, segments)
}

// mpdWithRepresentations emits one Representation per entry, which is how both
// Tidal tiers can appear in a single manifest.
func mpdWithRepresentations(serverURL string, codecs []string, startNumber, segments int) []byte {
	init := serverURL + "/init.mp4?x=1&amp;y=2"
	media := serverURL + "/seg.$Number$.m4s?x=1&amp;y=2"
	if segments < 1 {
		segments = 1
	}
	timeline := ""
	for i := 0; i < segments-1; i++ {
		timeline += `<S d="176128" r="0"/>`
	}
	timeline += `<S d="95342"/>`

	reps := ""
	for _, codecs := range codecs {
		reps += fmt.Sprintf(
			`<Representation id="FLAC,44100,16" codecs="%s" bandwidth="950306" audioSamplingRate="44100">`+
				`<SegmentTemplate timescale="44100" startNumber="%d" initialization="%s" media="%s">`+
				`<SegmentTimeline>%s</SegmentTimeline></SegmentTemplate>`+
				`</Representation>`, codecs, startNumber, init, media, timeline)
	}
	return []byte(fmt.Sprintf(
		`<?xml version='1.0'?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011"><Period id="0">`+
			`<AdaptationSet id="0" contentType="audio" mimeType="audio/mp4">%s</AdaptationSet>`+
			`</Period></MPD>`, reps))
}

func TestParseDashManifestExpandsEverySegment(t *testing.T) {
	stream, err := parseDashManifest(mpdWith("https://cdn.test", "flac", 1, 4))
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	if len(stream.segmentURL) != 4 {
		t.Fatalf("segments = %d, want 4", len(stream.segmentURL))
	}
	if got, want := stream.initURL, "https://cdn.test/init.mp4?x=1&y=2"; got != want {
		t.Errorf("initURL = %q, want %q", got, want)
	}
	if got, want := stream.segmentURL[0], "https://cdn.test/seg.1.m4s?x=1&y=2"; got != want {
		t.Errorf("first segment = %q, want %q", got, want)
	}
	if got, want := stream.segmentURL[3], "https://cdn.test/seg.4.m4s?x=1&y=2"; got != want {
		t.Errorf("last segment = %q, want %q", got, want)
	}
}

func TestParseDashManifestHonoursStartNumber(t *testing.T) {
	stream, err := parseDashManifest(mpdWith("https://cdn.test", "flac", 7, 2))
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	if got, want := stream.segmentURL[0], "https://cdn.test/seg.7.m4s?x=1&y=2"; got != want {
		t.Errorf("first segment = %q, want %q", got, want)
	}
}

func TestParseDashManifestAcceptsAacAsTheFallbackTier(t *testing.T) {
	stream, err := parseDashManifest(mpdWith("https://cdn.test", "mp4a.40.2", 1, 4))
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	if stream.codec != tidalCodecAAC {
		t.Fatalf("codec = %q, want %q", stream.codec, tidalCodecAAC)
	}
	if len(stream.segmentURL) != 4 {
		t.Fatalf("segments = %d, want 4", len(stream.segmentURL))
	}
	if got, want := stream.streamDuration(), (176128.0*3+95342)/44100; math.Abs(got-want) > 0.001 {
		t.Errorf("streamDuration = %.3f, want %.3f", got, want)
	}
}

func TestParseDashManifestPrefersFlacOverAac(t *testing.T) {
	manifest := mpdWithRepresentations("https://cdn.test", []string{"mp4a.40.2", "flac"}, 1, 2)
	stream, err := parseDashManifest(manifest)
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	if stream.codec != tidalCodecFLAC {
		t.Fatalf("codec = %q, want FLAC to win over a listed AAC tier", stream.codec)
	}
}

func TestExtractFlacStreamInfoBlockFindsTheStreamInfo(t *testing.T) {
	info := streamInfoBlock(44100, 7_849_800)
	block, err := extractFlacStreamInfoBlock(initSegment(info))
	if err != nil {
		t.Fatalf("extractFlacStreamInfoBlock: %v", err)
	}
	if len(block) != 38 {
		t.Fatalf("block length = %d, want 38", len(block))
	}
	if block[0]&0x7F != 0 {
		t.Errorf("block type = %d, want STREAMINFO (0)", block[0]&0x7F)
	}

	rate, samples, err := flacStreamInfo(block[4:])
	if err != nil {
		t.Fatalf("flacStreamInfo: %v", err)
	}
	if rate != 44100 {
		t.Errorf("sample rate = %d, want 44100", rate)
	}
	if samples != 7_849_800 {
		t.Errorf("total samples = %d, want 7849800", samples)
	}
}

func TestExtractFlacStreamInfoBlockRejectsNonFlacEntry(t *testing.T) {
	// A valid stsd whose sample entry is mp4a rather than fLaC.
	stsd := box("stsd", concat(make([]byte, 8), box("mp4a", concat(make([]byte, 28), box("esds", make([]byte, 4))))))
	init := box("moov", box("trak", box("mdia", box("minf", box("stbl", stsd)))))
	if _, err := extractFlacStreamInfoBlock(init); err == nil {
		t.Fatal("expected an error for an mp4a sample entry")
	}
}

func TestReadBoxesHandlesLargeSize(t *testing.T) {
	payload := []byte("payload")
	large := make([]byte, 16, 16+len(payload))
	large[3] = 1 // size == 1 means the real size is a 64-bit value below
	total := uint64(16 + len(payload))
	for i := 0; i < 8; i++ {
		large[8+i] = byte(total >> (8 * (7 - i)))
	}
	copy(large[4:8], "mdat")
	boxes, err := readBoxes(append(large, payload...))
	if err != nil {
		t.Fatalf("readBoxes: %v", err)
	}
	if len(boxes) != 1 || boxes[0].typ != "mdat" {
		t.Fatalf("boxes = %+v, want one mdat", boxes)
	}
	if string(boxes[0].payload) != "payload" {
		t.Errorf("payload = %q", boxes[0].payload)
	}
}

func TestWriteTidalFlacBuildsNativeFlacFromSegments(t *testing.T) {
	info := streamInfoBlock(44100, 44100*3)
	init := initSegment(info)
	first := []byte{0xFF, 0xF8, 0x11, 0x22}
	second := []byte{0xFF, 0xF9, 0x33}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/init"):
			w.Write(init)
		case r.URL.Path == "/seg.1.m4s":
			w.Write(mediaSegment(first))
		case r.URL.Path == "/seg.2.m4s":
			w.Write(mediaSegment(second))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	stream, err := parseDashManifest(mpdWith(server.URL, "flac", 1, 2))
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	if stream.init, err = tidalFetchBytesWithRetry(stream.initURL, 1<<20); err != nil {
		t.Fatalf("fetching init: %v", err)
	}
	if stream.streamInfo, err = extractFlacStreamInfoBlock(stream.init); err != nil {
		t.Fatalf("extractFlacStreamInfoBlock: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "track.flac")
	if err := writeTidalFlac(dest, stream); err != nil {
		t.Fatalf("writeTidalFlac: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	want := concat([]byte("fLaC"), stream.streamInfo, first, second)
	if string(got) != string(want) {
		t.Fatalf("file = %x, want %x", got, want)
	}
}

func TestWriteTidalFlacRejectsNonFlacPayload(t *testing.T) {
	info := streamInfoBlock(44100, 44100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/init") {
			w.Write(initSegment(info))
			return
		}
		w.Write(mediaSegment([]byte{0x00, 0x01, 0x02}))
	}))
	defer server.Close()

	stream, err := parseDashManifest(mpdWith(server.URL, "flac", 1, 1))
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	stream.init, _ = tidalFetchBytesWithRetry(stream.initURL, 1<<20)
	stream.streamInfo, _ = extractFlacStreamInfoBlock(stream.init)

	dest := filepath.Join(t.TempDir(), "track.flac")
	if err := writeTidalFlac(dest, stream); err == nil {
		t.Fatal("expected an error for a payload without FLAC frames")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed remux must not leave a file behind")
	}
}

func TestFetchTidalStreamRefusesPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"assetPresentation":"PREVIEW","manifestMimeType":"application/dash+xml","manifest":""}}`)
	}))
	defer server.Close()

	old := tidalEndpoints
	tidalEndpoints = []string{server.URL}
	defer func() { tidalEndpoints = old }()

	if _, err := fetchTidalStream(1); err == nil || !strings.Contains(err.Error(), "PREVIEW") {
		t.Fatalf("err = %v, want a preview rejection", err)
	}
}

func TestFetchTidalStreamAcceptsHighQualityAsAac(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/init") {
			w.Write(aacInitSegment(44100))
			return
		}
		encoded := base64.StdEncoding.EncodeToString(mpdWith(server.URL, "mp4a.40.2", 1, 2))
		fmt.Fprintf(w, `{"data":{"assetPresentation":"FULL","audioQuality":"HIGH","sampleRate":44100,"manifestMimeType":"application/dash+xml","manifest":%q}}`, encoded)
	}))
	defer server.Close()

	old := tidalEndpoints
	tidalEndpoints = []string{server.URL}
	defer func() { tidalEndpoints = old }()

	stream, err := fetchTidalStream(1)
	if err != nil {
		t.Fatalf("HIGH (AAC-320) must be accepted, got: %v", err)
	}
	if stream.codec != tidalCodecAAC {
		t.Fatalf("codec = %q, want aac", stream.codec)
	}
	if got := tidalTrackExtension(stream); got != ".m4a" {
		t.Errorf("extension = %q, want .m4a", got)
	}
}

func TestTidalAlbumFromSearchMapsFields(t *testing.T) {
	item := []byte(`{
		"id": 471424981,
		"title": "Memories of Holbrooks Bowl",
		"numberOfTracks": 13,
		"duration": 2608,
		"releaseDate": "2025-11-26",
		"cover": "ca0d2fa7-d6a0-41e2-849b-5505742fd4e1",
		"upc": "199900022929",
		"artists": [{"id": 3737707, "name": "Random Access"}]
	}`)
	album := tidalAlbumFromSearch(parseJSONForTest(item))

	if album.Source != "tidal" {
		t.Errorf("Source = %q, want tidal", album.Source)
	}
	if album.Artist != "Random Access" {
		t.Errorf("Artist = %q", album.Artist)
	}
	if album.Id != "471424981" || album.NumTracks != 13 || album.Duration != 2608 {
		t.Errorf("unexpected identifiers: %+v", album)
	}
	if album.BitDepth != 16 || album.SamplingRate != 44100 {
		t.Errorf("quality = %d/%d, want 16/44100", album.BitDepth, album.SamplingRate)
	}
	if want := "https://resources.tidal.com/images/ca0d2fa7/d6a0/41e2/849b/5505742fd4e1/1280x1280.jpg"; album.CoverUrl != want {
		t.Errorf("CoverUrl = %q, want %q", album.CoverUrl, want)
	}
	if album.Size <= 0 {
		t.Error("Size should be estimated from the duration")
	}
	if album.ReleaseDate == 0 {
		t.Error("ReleaseDate should be parsed from releaseDate")
	}
}

func TestTidalCoverURLWithoutUUID(t *testing.T) {
	if got := tidalCoverURL(""); got != "" {
		t.Errorf("tidalCoverURL(\"\") = %q, want empty", got)
	}
}

func TestTidalArtistNameFallsBackToScalarArtist(t *testing.T) {
	item := []byte(`{"artist":{"name":"Solo"},"artists":[{"name":""}]}`)
	if got := tidalArtistName(parseJSONForTest(item)); got != "Solo" {
		t.Errorf("tidalArtistName = %q, want Solo", got)
	}
}

func TestMergeProviderAlbumsInterleavesThreeSources(t *testing.T) {
	got := mergeProviderAlbums(
		[]Album{{Id: "q1"}, {Id: "q2"}},
		[]Album{{Id: "d1"}},
		[]Album{{Id: "t1"}, {Id: "t2"}, {Id: "t3"}},
	)
	want := []string{"q1", "d1", "t1", "q2", "t2", "t3"}
	if len(got) != len(want) {
		t.Fatalf("got %d albums, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Id != want[i] {
			t.Fatalf("order = %v, want %v", ids(got), want)
		}
	}
}

func TestReleaseProviderTagRecognisesTidal(t *testing.T) {
	if got := releaseProviderTag("tidal"); got != "TIDAL" {
		t.Errorf("releaseProviderTag(tidal) = %q", got)
	}
	if got := releaseProviderTag("Tidal"); got != "TIDAL" {
		t.Errorf("releaseProviderTag is case-insensitive, got %q", got)
	}
}

func TestIsSquidarrReleaseFolderRecognisesTidal(t *testing.T) {
	if !isSquidarrReleaseFolder("Artist-Album-16BIT-44.1-KHZ-WEB-FLAC-2025-TIDAL") {
		t.Error("a TIDAL release folder must be recognised")
	}
}

func u32(v uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, v)
	return out
}

func fullBox(typ string, flags uint32, payload []byte) []byte {
	head := []byte{0, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	return box(typ, append(head, payload...))
}

// aacInitSegment mirrors what the CDN sends for the HIGH tier: an mp4a sample
// entry, an mdhd timescale, and mvex/trex so the file is fragmented.
func aacInitSegment(timescale uint32) []byte {
	mp4a := box("mp4a", concat(make([]byte, 28), fullBox("esds", 0, make([]byte, 20))))
	stsd := fullBox("stsd", 0, concat(u32(1), mp4a))
	stbl := box("stbl", stsd)
	minf := box("minf", concat(box("smhd", make([]byte, 8)), stbl))
	mdhd := fullBox("mdhd", 0, concat(u32(0), u32(0), u32(timescale), u32(0)))
	hdlr := fullBox("hdlr", 0, concat(make([]byte, 4), []byte("soun"), make([]byte, 13)))
	mdia := box("mdia", concat(mdhd, hdlr, minf))
	trak := box("trak", concat(fullBox("tkhd", 3, make([]byte, 80)), mdia))
	mvhd := fullBox("mvhd", 0, make([]byte, 96))
	mvex := box("mvex", fullBox("mehd", 0, u32(44100)))
	return concat(box("ftyp", []byte("iso8")), box("moov", concat(mvhd, trak, mvex)))
}

// aacMediaSegment builds moof+mdat exactly like the CDN: sample durations live
// in trun, and the mdat payload is raw AAC access units.
func aacMediaSegment(payload []byte, durations ...uint32) []byte {
	trunBody := u32(uint32(len(durations)))
	for _, d := range durations {
		trunBody = append(trunBody, u32(d)...)
	}
	trun := fullBox("trun", 0x000100, trunBody)
	tfhd := fullBox("tfhd", 0x000000, u32(1))
	traf := box("traf", concat(fullBox("tfdt", 0, u32(0)), tfhd, trun))
	return concat(box("moof", concat(fullBox("mfhd", 0, u32(1)), traf)), box("mdat", payload))
}

func TestFragmentSampleDurationReadsTrunDurations(t *testing.T) {
	got, err := fragmentSampleDuration(aacMediaSegment([]byte{1, 2, 3}, 1024, 2048))
	if err != nil {
		t.Fatalf("fragmentSampleDuration: %v", err)
	}
	if got != 3072 {
		t.Errorf("duration = %d, want 3072", got)
	}
}

func TestFragmentSampleDurationFallsBackToTfhdDefault(t *testing.T) {
	trun := fullBox("trun", 0x000000, u32(3))
	tfhd := fullBox("tfhd", 0x000008, concat(u32(1), u32(1024)))
	traf := box("traf", concat(tfhd, trun))
	segment := box("moof", traf)
	got, err := fragmentSampleDuration(segment)
	if err != nil {
		t.Fatalf("fragmentSampleDuration: %v", err)
	}
	if got != 3072 {
		t.Errorf("duration = %d, want 3072 (3 samples x tfhd default)", got)
	}
}

func TestWriteTidalM4aConcatenatesInitAndFragments(t *testing.T) {
	init := aacInitSegment(44100)
	first := aacMediaSegment([]byte{0x21, 0x10, 0x05}, 2048, 2048)
	second := aacMediaSegment([]byte{0x21, 0x10, 0x07}, 1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/init"):
			w.Write(init)
		case r.URL.Path == "/seg.1.m4s":
			w.Write(first)
		case r.URL.Path == "/seg.2.m4s":
			w.Write(second)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	stream, err := parseDashManifest(mpdWith(server.URL, "mp4a.40.2", 1, 2))
	if err != nil {
		t.Fatalf("parseDashManifest: %v", err)
	}
	if stream.init, err = tidalFetchBytesWithRetry(stream.initURL, 1<<20); err != nil {
		t.Fatalf("fetching init: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "track.m4a")
	if err := writeTidalM4a(dest, stream); err != nil {
		t.Fatalf("writeTidalM4a: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, concat(init, first, second)) {
		t.Fatalf("file is not init + fragments: %d bytes", len(got))
	}

	// 2048 + 2048 + 1024 ticks over a 44100 timescale.
	if duration := mp4DurationSeconds(dest); math.Abs(duration-5120.0/44100) > 0.001 {
		t.Errorf("mp4DurationSeconds = %.4f, want %.4f", duration, 5120.0/44100)
	}
}

func TestWriteTidalM4aRejectsPlainMp4Init(t *testing.T) {
	init := aacInitSegment(44100)
	// Drop mvex: without it a player would read empty sample tables.
	moov, _ := findBox(init, "moov")
	plain := concat(box("ftyp", []byte("iso8")), box("moov", moov[:len(moov)-24]))
	stream := &tidalStream{codec: tidalCodecAAC, init: plain}

	dest := filepath.Join(t.TempDir(), "track.m4a")
	if err := writeTidalM4a(dest, stream); err == nil {
		t.Fatal("expected a non-fragmented initialization segment to be refused")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a refused mux must not leave a file behind")
	}
}

func TestTidalReleaseNameReflectsCodec(t *testing.T) {
	if got, want := tidalReleaseName("Artist", "Album", "2025", tidalCodecFLAC), "Artist-Album-16BIT-44.1-KHZ-WEB-FLAC-2025-TIDAL"; got != want {
		t.Errorf("flac name = %q, want %q", got, want)
	}
	if got, want := tidalReleaseName("Artist", "Album", "2025", tidalCodecAAC), "Artist-Album-WEB-AAC-320-2025-TIDAL"; got != want {
		t.Errorf("aac name = %q, want %q", got, want)
	}
}

func TestDurationMatchesUsesFivePercentFloor(t *testing.T) {
	if !durationMatches(178.4, 178) {
		t.Error("a value inside the tolerance must match")
	}
	if durationMatches(200, 178) {
		t.Error("a 12 % gap must not match")
	}
	if durationMatches(0, 178) {
		t.Error("an unreadable duration must not match")
	}
}

func TestStartTidalDownloadSkipsARefusedTrack(t *testing.T) {
	info := streamInfoBlock(44100, 44100*3)
	init := initSegment(info)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/track/"):
			if strings.Contains(r.URL.RawQuery, "id=1") {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"detail":"Upstream API error"}`)
				return
			}
			encoded := base64.StdEncoding.EncodeToString(mpdWith(server.URL, "flac", 1, 2))
			fmt.Fprintf(w, `{"data":{"assetPresentation":"FULL","manifestMimeType":"application/dash+xml","manifest":%q}}`, encoded)
		case strings.HasPrefix(r.URL.Path, "/init"):
			w.Write(init)
		case strings.HasPrefix(r.URL.Path, "/seg."):
			w.Write(mediaSegment([]byte{0xFF, 0xF8, 0x11, 0x22}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldEndpoints, oldPath, oldCategory := tidalEndpoints, DownloadPath, Category
	tidalEndpoints = []string{server.URL}
	DownloadPath = t.TempDir()
	Category = "music"
	defer func() { tidalEndpoints, DownloadPath, Category = oldEndpoints, oldPath, oldCategory }()

	const id = "album-skip"
	d := &Download{
		Id: id, Source: "tidal", Artist: "Artist", Album: "Album",
		FileName: "Artist-Album-WEB-FLAC-2025-TIDAL", numTracks: 2, downloaded: 0,
		Files: []File{
			{Id: 1, Name: "Refused", Index: "1", duration: 10},
			{Id: 2, Name: "Playable", Index: "2", duration: 3},
		},
	}
	Downloads[id] = d
	startTidalDownload(id)

	if d.skippedTracks != 1 {
		t.Errorf("skippedTracks = %d, want 1", d.skippedTracks)
	}
	if d.downloaded != 1 {
		t.Fatalf("downloaded = %d, want the one playable track", d.downloaded)
	}
	folder := filepath.Join(DownloadPath, "complete", Category, d.FileName)
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("the release should still be imported: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".flac") {
		t.Fatalf("expected exactly the playable track, got %v", entries)
	}
}

func TestStartTidalDownloadFailsWhenNoTrackStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	oldEndpoints, oldPath := tidalEndpoints, DownloadPath
	tidalEndpoints = []string{server.URL}
	DownloadPath = t.TempDir()
	defer func() { tidalEndpoints, DownloadPath = oldEndpoints, oldPath }()

	const id = "album-dead"
	d := &Download{
		Id: id, Source: "tidal", Artist: "Artist", Album: "Album",
		FileName: "Artist-Album-WEB-FLAC-2025-TIDAL", numTracks: 1, downloaded: 0,
		Files: []File{{Id: 1, Name: "Dead", Index: "1", duration: 3}},
	}
	Downloads[id] = d
	startTidalDownload(id)

	if d.downloaded >= 0 {
		t.Fatalf("downloaded = %d, want a failed release", d.downloaded)
	}
	if _, err := os.Stat(filepath.Join(DownloadPath, "complete", Category, d.FileName)); !os.IsNotExist(err) {
		t.Error("a release with no readable track must not be imported")
	}
}
