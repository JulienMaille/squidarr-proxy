package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// Public hifi-api instances, comma-separated in TIDAL_API_LINK. They can die at
// any time (the backing Tidal account gets banned), so every request walks the
// list until one answers.
var tidalEndpoints []string

// Asking for LOSSLESS yields FLAC when the backing account is HiFi and HIGH
// (AAC-LC, 320 kbit/s) when it is Premium. TIDAL_QUALITY=HIGH asks for AAC
// outright, which is what a smaller library wants; the default prefers FLAC and
// still accepts AAC when an instance demotes the request.
var tidalQuality = "LOSSLESS"

func tidalConfigured() bool { return len(tidalEndpoints) > 0 }

func tidalClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// tidalGet performs GET against each configured instance and returns the first
// successful body. The body cap keeps a hostile or broken instance from
// streaming unbounded data into memory.
func tidalGet(path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	if !tidalConfigured() {
		return nil, errors.New("TIDAL_API_LINK is not configured")
	}
	var lastErr error
	for _, base := range tidalEndpoints {
		req, err := http.NewRequest("GET", strings.TrimRight(base, "/")+path, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "squidarr-proxy")
		resp, err := tidalClient(timeout).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("%s returned HTTP %d: %s", base, resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 160)])))
			continue
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no Tidal endpoint configured")
	}
	return nil, lastErr
}

func tidalJSON(path string) (gjson.Result, error) {
	body, err := tidalGet(path, 8<<20, 60*time.Second)
	if err != nil {
		return gjson.Result{}, err
	}
	if !gjson.ValidBytes(body) {
		return gjson.Result{}, errors.New("invalid JSON from Tidal proxy")
	}
	return gjson.ParseBytes(body), nil
}

// tidalSearchVariants reuses the query-shaping already applied to Deezer: an
// artist-only change or a spoken ampersand must still reach the right album.
func tidalSearchVariants(query string) []string {
	return deezerSearchVariants(query)
}

func tidalSearchAlbums(query string, limit, offset int) []Album {
	if !tidalConfigured() {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	seen := make(map[string]bool)
	var albums []Album

	collect := func(result gjson.Result) {
		for _, item := range result.Get("data.albums.items").Array() {
			id := item.Get("id").String()
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			albums = append(albums, tidalAlbumFromSearch(item))
		}
	}

	for _, variant := range tidalSearchVariants(query) {
		if len(albums) >= limit {
			break
		}
		params := url.Values{
			"al":     {variant},
			"limit":  {strconv.Itoa(limit)},
			"offset": {strconv.Itoa(offset)},
		}
		result, err := tidalJSON("/search/?" + params.Encode())
		if err != nil {
			if Debug {
				fmt.Println("Tidal search failed:", err)
			}
			continue
		}
		collect(result)
	}

	if len(albums) > limit {
		albums = albums[:limit]
	}
	return albums
}

func tidalAlbumFromSearch(item gjson.Result) Album {
	duration := item.Get("duration").Int()
	// Tidal LOSSLESS is 16 bit / 44.1 kHz; the 0.7 factor approximates FLAC
	// compression, as the other sources do.
	size := int64(float64(duration) * 44100 * 16 * 2 / 8 * 0.7)
	released, _ := time.Parse("2006-01-02", item.Get("releaseDate").String())
	return Album{
		Source:       "tidal",
		Artist:       tidalArtistName(item),
		Title:        item.Get("title").String(),
		Id:           item.Get("id").String(),
		NumTracks:    item.Get("numberOfTracks").Int(),
		Duration:     duration,
		Size:         size,
		Channels:     2,
		QobuzUrl:     item.Get("url").String(),
		CoverUrl:     tidalCoverURL(item.Get("cover").String()),
		ReleaseDate:  released.Unix(),
		BitDepth:     16,
		SamplingRate: 44100,
	}
}

// tidalArtistName reads the first credited artist. Tidal search results carry
// no scalar "artist" field, only the artists array, while album and track
// objects expose both.
func tidalArtistName(v gjson.Result) string {
	if artists := v.Get("artists").Array(); len(artists) > 0 {
		if name := artists[0].Get("name").String(); name != "" {
			return name
		}
	}
	return v.Get("artist.name").String()
}

// tidalCoverURL turns the cover UUID Tidal returns into the resources host.
func tidalCoverURL(uuid string) string {
	if uuid == "" {
		return ""
	}
	path := strings.ReplaceAll(uuid, "-", "/")
	return "https://resources.tidal.com/images/" + path + "/1280x1280.jpg"
}

func generateTidalDownload(filename, id, _ string) {
	d := &Download{Id: id, Source: "tidal", FileName: filename, Quality: "7", downloaded: -1}
	Downloads[id] = d

	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		fmt.Println("Invalid Tidal album ID")
		return
	}
	result, err := tidalJSON("/album/?id=" + id)
	if err != nil {
		fmt.Println("Tidal album failed:", err)
		return
	}
	data := result.Get("data")
	if !data.Exists() {
		fmt.Println("Tidal album response carried no data")
		return
	}

	d.Artist = tidalArtistName(data)
	d.Album = data.Get("title").String()
	d.CoverUrl = tidalCoverURL(data.Get("cover").String())
	d.Year = strings.Split(data.Get("releaseDate").String(), "-")[0]
	d.UPC = data.Get("upc").String()

	expected := data.Get("numberOfTracks").Int()
	for _, entry := range data.Get("items").Array() {
		if entryType := entry.Get("type").String(); entryType != "" && entryType != "track" {
			continue
		}
		track := entry.Get("item")
		trackID := track.Get("id").Int()
		if trackID == 0 {
			continue
		}
		index := track.Get("trackNumber").String()
		if index == "" || index == "0" {
			index = strconv.Itoa(len(d.Files) + 1)
		}
		d.Files = append(d.Files, File{
			Id:          int(trackID),
			Name:        track.Get("title").String(),
			Index:       index,
			mediaNumber: track.Get("volumeNumber").String(),
			isrc:        track.Get("isrc").String(),
			duration:    track.Get("duration").Int(),
		})
	}

	if len(d.Files) == 0 || int64(len(d.Files)) != expected {
		fmt.Printf("Tidal album track list is incomplete: %d/%d\n", len(d.Files), expected)
		d.downloaded = -1
		d.FailMessage = "the track list is incomplete"
		return
	}
	d.numTracks = len(d.Files)
	// The folder has to name the quality that is actually inside it, because
	// that is what Lidarr reads. One probe answers for the album: the tier is a
	// property of the account behind the instance, not of the track.
	codec := tidalAlbumCodec(d.Files)
	d.Quality = "7"
	d.FileName = sanitizeFilename(tidalReleaseName(d.Artist, d.Album, d.Year, codec))
	if (tidalQuality == "HIGH") != (codec == tidalCodecAAC) {
		fmt.Printf("Tidal served %s although %s was requested; the release is named after what it served\n", codec, tidalQuality)
	}
	d.downloaded = 0
}

// tidalAlbumCodec probes the first tracks that answer and returns the codec the
// instance serves for this album. A few tracks are tried because a per-track 403
// is routine; if none answers, FLAC is assumed and the download reports its own
// failure.
func tidalAlbumCodec(files []File) string {
	for i, track := range files {
		if i >= 3 {
			break
		}
		stream, _, err := tidalTrackManifest(track.Id)
		if err != nil {
			continue
		}
		return stream.codec
	}
	return tidalCodecFLAC
}

func tidalReleaseName(artist, album, year, codec string) string {
	if codec == tidalCodecAAC {
		return fmt.Sprintf("%s-%s-WEB-AAC-320-%s-TIDAL", artist, album, year)
	}
	return fmt.Sprintf("%s-%s-16BIT-44.1-KHZ-WEB-FLAC-%s-TIDAL", artist, album, year)
}

// tidalAACQualityCode is the sentinel the indexer and releaseName use for the
// AAC tier, the way "5" flags MP3 and "7" flags FLAC.
const tidalAACQualityCode = "aac-320"

func tidalQualityCode() string {
	if tidalQuality == "HIGH" {
		return tidalAACQualityCode
	}
	return "7"
}

// --- DASH manifest -> native audio -----------------------------------------
//
// Tidal returns one signed MPD per track and the proxy exposes it base64-encoded
// in data.manifest. Two representations exist: LOSSLESS is FLAC carried in fMP4,
// HIGH is AAC-LC carried in fMP4. Neither needs a transcoder, only a different
// container:
//
//   - FLAC: the initialization segment holds the STREAMINFO in its stsd/dfLa box
//     and every media segment is moof+mdat whose mdat payload is raw FLAC frames.
//     A native .flac is "fLaC" + that STREAMINFO block + the concatenated mdat
//     payloads.
//   - AAC: the initialization segment is ftyp+moov(+mvex) and each media segment
//     is moof+mdat holding raw AAC access units. Concatenating init and every
//     segment whole yields a fragmented MP4, which is the .m4a the CDN streams.
//
// FLAC is preferred whenever the instance offers it; HIGH is accepted as the
// fallback (a Premium-backed account is demoted to it), never refused.

const (
	tidalCodecFLAC = "flac"
	tidalCodecAAC  = "aac"
)

type dashSegmentTemplate struct {
	Timescale      int64  `xml:"timescale,attr"`
	StartNumber    int64  `xml:"startNumber,attr"`
	Initialization string `xml:"initialization,attr"`
	Media          string `xml:"media,attr"`
	Timeline       []struct {
		D int64 `xml:"d,attr"`
		R int64 `xml:"r,attr"`
	} `xml:"SegmentTimeline>S"`
}

type dashRepresentation struct {
	ID                string               `xml:"id,attr"`
	Codecs            string               `xml:"codecs,attr"`
	Bandwidth         string               `xml:"bandwidth,attr"`
	AudioSamplingRate string               `xml:"audioSamplingRate,attr"`
	SegmentTemplate   *dashSegmentTemplate `xml:"SegmentTemplate"`
}

type dashManifest struct {
	Periods []struct {
		AdaptationSets []struct {
			ContentType     string               `xml:"contentType,attr"`
			Representations []dashRepresentation `xml:"Representation"`
		} `xml:"AdaptationSet"`
	} `xml:"Period"`
}

type tidalStream struct {
	codec        string
	timescale    int64
	initURL      string
	segmentURL   []string
	segmentTicks int64 // duration declared by the MPD timeline, in timescale units
	init         []byte
	streamInfo   []byte
	sampleRate   int
	totalSamples uint64
}

func parseDashManifest(mpd []byte) (*tidalStream, error) {
	var manifest dashManifest
	if err := xml.Unmarshal(mpd, &manifest); err != nil {
		return nil, fmt.Errorf("unreadable DASH manifest: %w", err)
	}
	// Two passes so a FLAC representation always beats an AAC one when an
	// instance offers both.
	for _, want := range []string{"flac", "mp4a"} {
		for _, period := range manifest.Periods {
			for _, set := range period.AdaptationSets {
				if set.ContentType != "" && set.ContentType != "audio" {
					continue
				}
				for _, rep := range set.Representations {
					if !strings.Contains(strings.ToLower(rep.Codecs), want) {
						continue
					}
					stream, err := streamFromRepresentation(rep, want)
					if err != nil {
						return nil, err
					}
					return stream, nil
				}
			}
		}
	}
	return nil, errors.New("Tidal did not return a FLAC or AAC representation")
}

func streamFromRepresentation(rep dashRepresentation, codec string) (*tidalStream, error) {
	tpl := rep.SegmentTemplate
	if tpl == nil || tpl.Initialization == "" || tpl.Media == "" {
		return nil, errors.New("DASH manifest has no segment template")
	}
	number := tpl.StartNumber
	if number == 0 {
		number = 1
	}
	count := 0
	var ticks int64
	for _, s := range tpl.Timeline {
		repeats := s.R
		if repeats < 0 {
			return nil, errors.New("DASH manifest uses an unbounded segment timeline")
		}
		count += int(repeats) + 1
		ticks += s.D * (repeats + 1)
	}
	if count == 0 {
		return nil, errors.New("DASH manifest has no segment timeline")
	}
	if codec == "mp4a" {
		codec = tidalCodecAAC
	}
	stream := &tidalStream{
		codec:        codec,
		timescale:    tpl.Timescale,
		initURL:      expandTemplate(tpl.Initialization, rep, number),
		segmentTicks: ticks,
	}
	for i := 0; i < count; i++ {
		stream.segmentURL = append(stream.segmentURL, expandTemplate(tpl.Media, rep, number+int64(i)))
	}
	return stream, nil
}

func expandTemplate(tpl string, rep dashRepresentation, number int64) string {
	out := strings.ReplaceAll(tpl, "$Number$", strconv.FormatInt(number, 10))
	out = strings.ReplaceAll(out, "$RepresentationID$", rep.ID)
	out = strings.ReplaceAll(out, "$Bandwidth$", rep.Bandwidth)
	return out
}

// --- ISO-BMFF box walking --------------------------------------------------

type bmffBox struct {
	typ     string
	payload []byte
}

func readBoxes(b []byte) ([]bmffBox, error) {
	var boxes []bmffBox
	for len(b) >= 8 {
		size := int(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
		typ := string(b[4:8])
		headerLen := 8
		if size == 1 {
			if len(b) < 16 {
				return boxes, errors.New("truncated 64-bit box header")
			}
			large := uint64(0)
			for i := 8; i < 16; i++ {
				large = large<<8 | uint64(b[i])
			}
			size = int(large)
			headerLen = 16
		} else if size == 0 {
			size = len(b)
		}
		if size < headerLen || size > len(b) {
			return boxes, fmt.Errorf("box %q has invalid size %d", typ, size)
		}
		boxes = append(boxes, bmffBox{typ: typ, payload: b[headerLen:size]})
		b = b[size:]
	}
	return boxes, nil
}

func findBox(b []byte, typ string) ([]byte, bool) {
	boxes, err := readBoxes(b)
	if err != nil {
		return nil, false
	}
	for _, box := range boxes {
		if box.typ == typ {
			return box.payload, true
		}
	}
	return nil, false
}

// findBoxPath walks a chain of box types, e.g. moov > trak > mdia > stbl.
func findBoxPath(b []byte, path ...string) ([]byte, bool) {
	cur := b
	for _, typ := range path {
		next, ok := findBox(cur, typ)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// extractFlacStreamInfoBlock returns the FLAC metadata block header plus the
// 34-byte STREAMINFO payload found in the initialization segment's fLaC sample
// entry, ready to be appended to the "fLaC" marker.
func extractFlacStreamInfoBlock(init []byte) ([]byte, error) {
	stsd, ok := findBoxPath(init, "moov", "trak", "mdia", "minf", "stbl", "stsd")
	if !ok {
		return nil, errors.New("initialization segment has no stsd box")
	}
	if len(stsd) < 8 {
		return nil, errors.New("stsd box is truncated")
	}
	// version/flags(4) + entry_count(4), then the sample entries.
	entries, err := readBoxes(stsd[8:])
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.typ != "fLaC" {
			continue
		}
		// AudioSampleEntry: 8 bytes reserved + data_reference_index, then
		// version/revision/vendor, channel count, sample size, compression id,
		// packet size and sample rate (20 bytes).
		const audioEntryHeader = 8 + 20
		if len(entry.payload) < audioEntryHeader {
			return nil, errors.New("fLaC sample entry is truncated")
		}
		children, err := readBoxes(entry.payload[audioEntryHeader:])
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.typ != "dfLa" {
				continue
			}
			if len(child.payload) < 4 {
				return nil, errors.New("dfLa box is truncated")
			}
			blocks := child.payload[4:] // skip version/flags
			for len(blocks) >= 4 {
				header := blocks[0]
				length := int(blocks[1])<<16 | int(blocks[2])<<8 | int(blocks[3])
				if len(blocks) < 4+length {
					return nil, errors.New("dfLa metadata block is truncated")
				}
				if header&0x7F == 0 { // STREAMINFO
					if length != 34 {
						return nil, fmt.Errorf("unexpected STREAMINFO length %d", length)
					}
					return blocks[:4+length], nil
				}
				blocks = blocks[4+length:]
			}
			return nil, errors.New("dfLa box carries no STREAMINFO block")
		}
		return nil, errors.New("fLaC sample entry carries no dfLa box")
	}
	return nil, errors.New("initialization segment is not a FLAC sample entry")
}

// flacStreamInfo reads the sample rate and total sample count packed into the
// 34-byte STREAMINFO payload.
func flacStreamInfo(streamInfo []byte) (sampleRate int, totalSamples uint64, err error) {
	if len(streamInfo) < 34 {
		return 0, 0, errors.New("STREAMINFO is truncated")
	}
	sampleRate = int(streamInfo[10])<<12 | int(streamInfo[11])<<4 | int(streamInfo[12])>>4
	totalSamples = uint64(streamInfo[13]&0x0F)<<32 |
		uint64(streamInfo[14])<<24 |
		uint64(streamInfo[15])<<16 |
		uint64(streamInfo[16])<<8 |
		uint64(streamInfo[17])
	return sampleRate, totalSamples, nil
}

func tidalFetchBytes(rawURL string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "squidarr-proxy")
	resp, err := tidalClient(timeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDN returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// tidalTrackManifest resolves the signed manifest for one track without
// fetching any media, which is enough to learn the codec an instance serves.
func tidalTrackManifest(trackID int) (*tidalStream, gjson.Result, error) {
	params := url.Values{
		"id":      {strconv.Itoa(trackID)},
		"quality": {tidalQuality},
	}
	result, err := tidalJSON("/track/?" + params.Encode())
	if err != nil {
		return nil, gjson.Result{}, err
	}
	data := result.Get("data")
	if !data.Exists() || !data.Get("manifest").Exists() {
		return nil, gjson.Result{}, errors.New("Tidal returned no manifest for this track")
	}
	if presentation := data.Get("assetPresentation").String(); presentation != "" && presentation != "FULL" {
		return nil, gjson.Result{}, fmt.Errorf("Tidal returned %s audio, not a full track", presentation)
	}
	if mime := data.Get("manifestMimeType").String(); mime != "" && mime != "application/dash+xml" {
		return nil, gjson.Result{}, fmt.Errorf("unexpected manifest type %s", mime)
	}
	mpd, err := base64.StdEncoding.DecodeString(data.Get("manifest").String())
	if err != nil {
		return nil, gjson.Result{}, fmt.Errorf("undecodable manifest: %w", err)
	}
	stream, err := parseDashManifest(mpd)
	if err != nil {
		return nil, gjson.Result{}, err
	}
	stream.sampleRate = int(data.Get("sampleRate").Int())
	return stream, data, nil
}

func fetchTidalStream(trackID int) (*tidalStream, error) {
	stream, _, err := tidalTrackManifest(trackID)
	if err != nil {
		return nil, err
	}

	init, err := tidalFetchBytesWithRetry(stream.initURL, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("initialization segment: %w", err)
	}
	stream.init = init

	if stream.codec == tidalCodecFLAC {
		streamInfo, err := extractFlacStreamInfoBlock(init)
		if err != nil {
			return nil, err
		}
		stream.streamInfo = streamInfo
		rate, samples, err := flacStreamInfo(streamInfo[4:])
		if err != nil {
			return nil, err
		}
		if stream.sampleRate == 0 {
			stream.sampleRate = rate
		}
		stream.totalSamples = samples
		return stream, nil
	}
	if _, ok := findBoxPath(init, "moov", "trak", "mdia", "minf", "stbl", "stsd"); !ok {
		return nil, errors.New("initialization segment has no stsd box")
	}
	if len(init) < 8 || !bytes.Contains(init, []byte("mp4a")) {
		return nil, errors.New("initialization segment is not an AAC sample entry")
	}
	return stream, nil
}

// streamDuration is the duration the manifest itself declares, in seconds.
func (s *tidalStream) streamDuration() float64 {
	if s.codec == tidalCodecFLAC {
		if s.sampleRate > 0 && s.totalSamples > 0 {
			return float64(s.totalSamples) / float64(s.sampleRate)
		}
	}
	if s.timescale > 0 && s.segmentTicks > 0 {
		return float64(s.segmentTicks) / float64(s.timescale)
	}
	return 0
}

func tidalFetchBytesWithRetry(rawURL string, maxBytes int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		body, err := tidalFetchBytes(rawURL, maxBytes, 2*time.Minute)
		if err == nil {
			return body, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	return nil, lastErr
}

// writeTidalFlac assembles a native FLAC file from the initialization segment
// and the media segments, verifying the result before the caller accepts it.
func writeTidalFlac(dest string, stream *tidalStream) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	cleanup := func(err error) error {
		f.Close()
		os.Remove(dest)
		return err
	}

	if _, err := f.Write([]byte("fLaC")); err != nil {
		return cleanup(err)
	}
	if _, err := f.Write(stream.streamInfo); err != nil {
		return cleanup(err)
	}

	var frames uint64
	for i, segmentURL := range stream.segmentURL {
		segment, err := tidalFetchBytesWithRetry(segmentURL, 64<<20)
		if err != nil {
			return cleanup(fmt.Errorf("segment %d: %w", i+1, err))
		}
		mdat, ok := findBox(segment, "mdat")
		if !ok {
			return cleanup(fmt.Errorf("segment %d has no mdat box", i+1))
		}
		if len(mdat) > 0 && i == 0 {
			// FLAC frames start with a sync code; anything else means the
			// representation was not FLAC despite the manifest saying so.
			if !bytes.HasPrefix(mdat, []byte{0xFF, 0xF8}) && !bytes.HasPrefix(mdat, []byte{0xFF, 0xF9}) {
				return cleanup(errors.New("segment payload is not FLAC frames"))
			}
		}
		if _, err := f.Write(mdat); err != nil {
			return cleanup(err)
		}
		frames += uint64(len(mdat))
	}

	if err := f.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	if frames == 0 {
		os.Remove(dest)
		return errors.New("Tidal stream carried no audio")
	}
	return nil
}

// writeTidalM4a assembles the fragmented MP4 the CDN streams: the
// initialization segment (ftyp+moov+mvex) followed by every moof+mdat segment
// in order. No sample table has to be rebuilt — fragmented MP4 is what a DASH
// player consumes, and players read it as a normal .m4a.
func writeTidalM4a(dest string, stream *tidalStream) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	cleanup := func(err error) error {
		f.Close()
		os.Remove(dest)
		return err
	}

	init := stream.init
	if _, ok := findBoxPath(init, "moov", "mvex"); !ok {
		// Without mvex a player treats the file as a plain MP4 with empty
		// sample tables and plays nothing.
		return cleanup(errors.New("initialization segment is not fragmented (no mvex box)"))
	}
	if _, err := f.Write(init); err != nil {
		return cleanup(err)
	}

	var audio uint64
	var ticks int64
	for i, segmentURL := range stream.segmentURL {
		segment, err := tidalFetchBytesWithRetry(segmentURL, 64<<20)
		if err != nil {
			return cleanup(fmt.Errorf("segment %d: %w", i+1, err))
		}
		mdat, ok := findBox(segment, "mdat")
		if !ok {
			return cleanup(fmt.Errorf("segment %d has no mdat box", i+1))
		}
		duration, err := fragmentSampleDuration(segment)
		if err != nil {
			return cleanup(fmt.Errorf("segment %d: %w", i+1, err))
		}
		if duration <= 0 {
			return cleanup(fmt.Errorf("segment %d declares no sample duration", i+1))
		}
		ticks += duration
		if _, err := f.Write(segment); err != nil {
			return cleanup(err)
		}
		audio += uint64(len(mdat))
	}

	if err := f.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	if audio == 0 {
		os.Remove(dest)
		return errors.New("Tidal stream carried no audio")
	}
	if stream.timescale > 0 {
		stream.segmentTicks = ticks
	}
	return nil
}

// fragmentSampleDuration sums the sample durations declared by one media
// segment: trun per-sample durations when present, tfhd's default otherwise.
func fragmentSampleDuration(segment []byte) (int64, error) {
	traf, ok := findBoxPath(segment, "moof", "traf")
	if !ok {
		return 0, errors.New("fragment has no traf box")
	}
	boxes, err := readBoxes(traf)
	if err != nil {
		return 0, err
	}
	var defaultDuration int64
	var total int64
	for _, b := range boxes {
		switch b.typ {
		case "tfhd":
			defaultDuration, err = tfhdDefaultDuration(b.payload)
			if err != nil {
				return 0, err
			}
		case "trun":
			duration, err := trunDuration(b.payload, defaultDuration)
			if err != nil {
				return 0, err
			}
			total += duration
		}
	}
	return total, nil
}

func tfhdDefaultDuration(p []byte) (int64, error) {
	if len(p) < 8 {
		return 0, errors.New("truncated tfhd box")
	}
	flags := uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
	pos := 8
	if flags&0x000001 != 0 {
		pos += 8
	}
	if flags&0x000002 != 0 {
		pos += 4
	}
	if flags&0x000008 == 0 {
		return 0, nil
	}
	if len(p) < pos+4 {
		return 0, errors.New("truncated tfhd box")
	}
	return int64(binary.BigEndian.Uint32(p[pos:])), nil
}

func trunDuration(p []byte, defaultDuration int64) (int64, error) {
	if len(p) < 8 {
		return 0, errors.New("truncated trun box")
	}
	flags := uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
	count := int(binary.BigEndian.Uint32(p[4:]))
	pos := 8
	if flags&0x000001 != 0 {
		pos += 4
	}
	if flags&0x000004 != 0 {
		pos += 4
	}
	var total int64
	for i := 0; i < count; i++ {
		duration := defaultDuration
		if flags&0x000100 != 0 {
			if len(p) < pos+4 {
				return 0, errors.New("truncated trun box")
			}
			duration = int64(binary.BigEndian.Uint32(p[pos:]))
			pos += 4
		}
		if flags&0x000200 != 0 {
			pos += 4
		}
		if flags&0x000400 != 0 {
			pos += 4
		}
		if flags&0x000800 != 0 {
			pos += 4
		}
		if pos > len(p) {
			return 0, errors.New("truncated trun box")
		}
		total += duration
	}
	return total, nil
}

// mp4DurationSeconds measures a written .m4a by reading its timescale from the
// initialization segment and summing the durations of every fragment on disk,
// so a truncated download is caught exactly like a truncated FLAC.
func mp4DurationSeconds(path string) float64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0
	}

	var timescale int64
	var ticks int64
	offset := int64(0)
	for offset < info.Size() {
		var header [16]byte
		if _, err := f.ReadAt(header[:8], offset); err != nil {
			break
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		typ := string(header[4:8])
		headerLen := int64(8)
		if size == 1 {
			if _, err := f.ReadAt(header[8:16], offset+8); err != nil {
				break
			}
			size = int64(binary.BigEndian.Uint64(header[8:16]))
			headerLen = 16
		} else if size == 0 {
			size = info.Size() - offset
		}
		if size < headerLen || offset+size > info.Size() {
			break
		}

		switch typ {
		case "moov":
			if timescale == 0 {
				if raw := make([]byte, size-headerLen); func() bool {
					_, err := f.ReadAt(raw, offset+headerLen)
					return err == nil
				}() {
					timescale = mp4Timescale(raw)
				}
			}
		case "moof":
			raw := make([]byte, size-headerLen)
			if _, err := f.ReadAt(raw, offset+headerLen); err != nil {
				break
			}
			duration, err := fragmentSampleDuration(boxBytes(typ, raw))
			if err != nil {
				return 0
			}
			ticks += duration
		}
		offset += size
	}
	if timescale <= 0 || ticks <= 0 {
		return 0
	}
	return float64(ticks) / float64(timescale)
}

// mp4Timescale reads the movie timescale from mdia/mdhd.
func mp4Timescale(moov []byte) int64 {
	mdhd, ok := findBoxPath(moov, "trak", "mdia", "mdhd")
	if !ok || len(mdhd) < 16 {
		return 0
	}
	if mdhd[0] == 1 {
		if len(mdhd) < 28 {
			return 0
		}
		return int64(binary.BigEndian.Uint32(mdhd[20:24]))
	}
	return int64(binary.BigEndian.Uint32(mdhd[12:16]))
}

// boxBytes rebuilds a box so the parsers can work on a fragment read from disk.
func boxBytes(typ string, payload []byte) []byte {
	out := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(out[:4], uint32(8+len(payload)))
	copy(out[4:8], typ)
	return append(out, payload...)
}

// tidalDurationSeconds reads the duration of a finished file, whichever
// container the stream produced.
func tidalDurationSeconds(path, codec string) float64 {
	if codec == tidalCodecAAC {
		return mp4DurationSeconds(path)
	}
	return flacDurationSeconds(path)
}

func tidalTrackExtension(stream *tidalStream) string {
	if stream.codec == tidalCodecAAC {
		return ".m4a"
	}
	return ".flac"
}

// durationMatches applies the same ±5 % (minimum 5 s) rule everywhere.
func durationMatches(value float64, expectedSeconds int64) bool {
	if value <= 0 || expectedSeconds <= 0 {
		return false
	}
	tolerance := float64(expectedSeconds) * 0.05
	if tolerance < 5 {
		tolerance = 5
	}
	diff := value - float64(expectedSeconds)
	return diff <= tolerance && diff >= -tolerance
}

// tidalDownloadTrack writes one track and returns the file it produced, whose
// extension follows the codec the instance actually served.
func tidalDownloadTrack(stem string, track File) (string, error) {
	stream, err := fetchTidalStream(track.Id)
	if err != nil {
		return "", err
	}
	dest := stem + tidalTrackExtension(stream)

	if encoded := stream.streamDuration(); encoded > 0 && track.duration > 0 {
		if !durationMatches(encoded, track.duration) {
			return "", fmt.Errorf("manifest duration %.1fs does not match the track's %ds", encoded, track.duration)
		}
	}

	var writeErr error
	if stream.codec == tidalCodecAAC {
		writeErr = writeTidalM4a(dest, stream)
	} else {
		writeErr = writeTidalFlac(dest, stream)
	}
	if writeErr != nil {
		return "", writeErr
	}

	duration := tidalDurationSeconds(dest, stream.codec)
	if !durationMatches(duration, track.duration) {
		return "", fmt.Errorf("invalid or truncated %s duration %.1f / %d", strings.ToUpper(stream.codec), duration, track.duration)
	}
	return dest, nil
}

func startTidalDownload(id string) {
	d := Downloads[id]
	if d == nil || d.downloaded < 0 {
		return
	}
	folder := filepath.Join(DownloadPath, "incomplete", Category, d.FileName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		d.downloaded = -1
		d.FailMessage = "the destination folder could not be created"
		return
	}
	if d.CoverUrl != "" {
		_ = grabFile(filepath.Join(folder, "cover.jpg"), d.CoverUrl)
	}
	written := 0
	for i, track := range d.Files {
		trackNumber := strings.TrimSpace(track.Index)
		if trackNumber == "" || trackNumber == "0" {
			trackNumber = strconv.Itoa(i + 1)
		}
		track.Index = trackNumber
		stem := filepath.Join(folder, sanitizeFilename(trackNumber+" - "+d.Artist+" - "+track.Name))
		path, err := tidalDownloadTrack(stem, track)
		if err != nil {
			// A 403 is per track on this ecosystem: the album may still be
			// almost entirely streamable, so one dead track is skipped like a
			// preview rather than sinking the whole release.
			fmt.Println("Tidal track skipped:", track.Name, err)
			d.skippedTracks++
			continue
		}
		writeMetaData(*d, track, path)
		written++
		if d.downloaded < d.numTracks-1 {
			d.downloaded++
		}
	}
	if written == 0 {
		fmt.Println("Tidal download failed: no track could be streamed")
		d.downloaded = -1
		d.FailMessage = "no track could be streamed"
		return
	}
	if d.skippedTracks > 0 {
		fmt.Printf("Tidal download incomplete: %d/%d tracks skipped\n", d.skippedTracks, d.numTracks)
	}
	dest := filepath.Join(DownloadPath, "complete", Category, d.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		d.downloaded = -1
		d.FailMessage = "the destination folder could not be created"
		return
	}
	if _, err := os.Stat(dest); err == nil {
		fmt.Println("Tidal destination already exists:", dest)
		d.downloaded = -1
		d.FailMessage = "this release is already in the complete folder"
		return
	}
	if err := os.Rename(folder, dest); err != nil {
		fmt.Println("Tidal import staging failed:", err)
		d.downloaded = -1
		d.FailMessage = "staging the download failed"
		return
	}
	d.downloaded = written
	fmt.Println("Tidal download complete:", d.FileName)
}
