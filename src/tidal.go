package main

import (
	"bytes"
	"encoding/base64"
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

const tidalFLACQuality = "LOSSLESS"

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
		return
	}
	d.numTracks = len(d.Files)
	d.FileName = sanitizeFilename(fmt.Sprintf("%s-%s-16BIT-44.1-KHZ-WEB-FLAC-%s-TIDAL", d.Artist, d.Album, d.Year))
	d.downloaded = 0
}

// --- DASH manifest → native FLAC -------------------------------------------
//
// Tidal returns one signed MPD per track and the proxy exposes it base64-encoded
// in data.manifest. For LOSSLESS the representation is FLAC carried in fMP4:
// the initialization segment holds the FLAC STREAMINFO in its stsd/dfLa box and
// every media segment is moof+mdat whose mdat payload is raw FLAC frames. So a
// valid .flac is "fLaC" + that STREAMINFO block + the concatenated mdat payloads,
// with no transcoder involved. HIGH is AAC in fMP4 and is refused instead: it
// cannot be converted without an external encoder this project does not ship.

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
	initURL      string
	segmentURL   []string
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
	for _, period := range manifest.Periods {
		for _, set := range period.AdaptationSets {
			if set.ContentType != "" && set.ContentType != "audio" {
				continue
			}
			for _, rep := range set.Representations {
				if !strings.Contains(strings.ToLower(rep.Codecs), "flac") {
					continue
				}
				tpl := rep.SegmentTemplate
				if tpl == nil || tpl.Initialization == "" || tpl.Media == "" {
					return nil, errors.New("DASH manifest has no segment template")
				}
				number := tpl.StartNumber
				if number == 0 {
					number = 1
				}
				count := 0
				for _, s := range tpl.Timeline {
					count += int(s.R) + 1
				}
				if count == 0 {
					return nil, errors.New("DASH manifest has no segment timeline")
				}
				stream := &tidalStream{
					initURL: expandTemplate(tpl.Initialization, rep, number),
				}
				for i := 0; i < count; i++ {
					stream.segmentURL = append(stream.segmentURL, expandTemplate(tpl.Media, rep, number+int64(i)))
				}
				return stream, nil
			}
		}
	}
	return nil, errors.New("Tidal did not return a FLAC representation")
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

func fetchTidalStream(trackID int) (*tidalStream, error) {
	params := url.Values{
		"id":      {strconv.Itoa(trackID)},
		"quality": {tidalFLACQuality},
	}
	result, err := tidalJSON("/track/?" + params.Encode())
	if err != nil {
		return nil, err
	}
	data := result.Get("data")
	if !data.Exists() || !data.Get("manifest").Exists() {
		return nil, errors.New("Tidal returned no manifest for this track")
	}
	if presentation := data.Get("assetPresentation").String(); presentation != "" && presentation != "FULL" {
		return nil, fmt.Errorf("Tidal returned %s audio, not a full track", presentation)
	}
	if mime := data.Get("manifestMimeType").String(); mime != "" && mime != "application/dash+xml" {
		return nil, fmt.Errorf("unexpected manifest type %s", mime)
	}
	mpd, err := base64.StdEncoding.DecodeString(data.Get("manifest").String())
	if err != nil {
		return nil, fmt.Errorf("undecodable manifest: %w", err)
	}
	stream, err := parseDashManifest(mpd)
	if err != nil {
		return nil, err
	}
	stream.sampleRate = int(data.Get("sampleRate").Int())

	init, err := tidalFetchBytesWithRetry(stream.initURL, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("initialization segment: %w", err)
	}
	stream.init = init
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

func tidalDurationSeconds(path string) float64 {
	return flacDurationSeconds(path)
}

func tidalDownloadTrack(dest string, track File) error {
	stream, err := fetchTidalStream(track.Id)
	if err != nil {
		return err
	}
	if stream.sampleRate > 0 && stream.totalSamples > 0 {
		encoded := float64(stream.totalSamples) / float64(stream.sampleRate)
		if track.duration > 0 {
			tolerance := float64(track.duration) * 0.05
			if tolerance < 5 {
				tolerance = 5
			}
			if diff := encoded - float64(track.duration); diff > tolerance || diff < -tolerance {
				return fmt.Errorf("manifest duration %.1fs does not match the track's %ds", encoded, track.duration)
			}
		}
	}
	if err := writeTidalFlac(dest, stream); err != nil {
		return err
	}
	duration := tidalDurationSeconds(dest)
	if duration <= 0 || track.duration <= 0 {
		return fmt.Errorf("invalid FLAC duration %.1f / %d", duration, track.duration)
	}
	tolerance := float64(track.duration) * 0.05
	if tolerance < 5 {
		tolerance = 5
	}
	if diff := duration - float64(track.duration); diff > tolerance || diff < -tolerance {
		return fmt.Errorf("invalid or truncated FLAC duration %.1f / %d", duration, track.duration)
	}
	return nil
}

func startTidalDownload(id string) {
	d := Downloads[id]
	if d == nil || d.downloaded < 0 {
		return
	}
	folder := filepath.Join(DownloadPath, "incomplete", Category, d.FileName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		d.downloaded = -1
		return
	}
	if d.CoverUrl != "" {
		_ = grabFile(filepath.Join(folder, "cover.jpg"), d.CoverUrl)
	}
	for i, track := range d.Files {
		trackNumber := strings.TrimSpace(track.Index)
		if trackNumber == "" || trackNumber == "0" {
			trackNumber = strconv.Itoa(i + 1)
		}
		track.Index = trackNumber
		stem := filepath.Join(folder, sanitizeFilename(trackNumber+" - "+d.Artist+" - "+track.Name))
		path := stem + ".flac"
		if err := tidalDownloadTrack(path, track); err != nil {
			fmt.Println("Tidal download failed:", track.Name, err)
			_ = os.Remove(path)
			d.downloaded = -1
			return
		}
		writeMetaData(*d, track, path)
		if d.downloaded < d.numTracks-1 {
			d.downloaded++
		}
	}
	dest := filepath.Join(DownloadPath, "complete", Category, d.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		d.downloaded = -1
		return
	}
	if _, err := os.Stat(dest); err == nil {
		fmt.Println("Tidal destination already exists:", dest)
		d.downloaded = -1
		return
	}
	if err := os.Rename(folder, dest); err != nil {
		fmt.Println("Tidal import staging failed:", err)
		d.downloaded = -1
		return
	}
	d.downloaded = d.numTracks
	fmt.Println("Tidal download complete:", d.FileName)
}
