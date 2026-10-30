package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type Album struct {
	Source       string
	Artist       string
	Title        string
	Edition      string
	ReleaseDate  int64
	Publisher    string
	CoverUrl     string
	SamplingRate int64
	BitDepth     int64
	Id           string
	NumTracks    int64
	Channels     int64
	Duration     int64
	Size         int64
	Genre        string
	QobuzUrl     string
}

func handleIndexerRequest(w http.ResponseWriter, r *http.Request) {
	var queryApiKey string = r.URL.Query().Get("apikey")
	if queryApiKey != ApiKey {
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
		<error code="100" description="Incorrect user credentials"/>`))
		return
	}
	switch query := r.URL.Query().Get("t"); query {
	case "caps":
		caps(w, *r.URL)
	case "music":
		music(w, *r.URL)
	case "search":
		search(w, *r.URL)
	case "fakenzb":
		fakenzb(w, *r.URL)
	default:
		fmt.Println("Indexer unknown request:")
		fmt.Println(r.Method)
		fmt.Println(r.URL.String())
		fmt.Println(r.Header)
		buffer := make([]byte, 100)
		for {
			n, err := r.Body.Read(buffer)
			fmt.Printf("%q\n", buffer[:n])
			if err == io.EOF {
				break
			}
		}
		w.Write([]byte("Request received!"))
	}
}

func caps(w http.ResponseWriter, u url.URL) {
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<caps>
    <limits max="5000" default="5000"/>
    <registration available="no" open="no"/>
    <searching>
        <search available="yes" supportedParams="q"/>
        <tv-search available="no" supportedParams=""/>
        <movie-search available="no" supportedParams=""/>
        <audio-search available="yes" supportedParams="q" />
    </searching>
    <categories>
        <category id="3000" name="Audio">
            <subcat id="3010" name="Audio/MP3"/>
            <subcat id="3020" name="Audio/Video"/>
            <subcat id="3030" name="Audio/Audiobook"/>
            <subcat id="3040" name="Audio/Lossless"/>
            <subcat id="3050" name="Audio/Podcast"/>
        </category>
    </categories>
</caps>
	`))
}

type Rss struct {
	XMLName string  `xml:"rss"`
	Version string  `xml:"version,attr"`
	Newznab string  `xml:"xmlns:newznab,attr"`
	Channel Channel `xml:"channel"`
}

type Channel struct {
	Title           string          `xml:"title"`
	Description     string          `xml:"description"`
	NewznabResponse NewznabResponse `xml:"newznab:response"`
	Items           []Item          `xml:"item"`
}

type NewznabResponse struct {
	Offset int `xml:"offset,attr"`
	Total  int `xml:"total,attr"`
}

type Item struct {
	Title       string        `xml:"title"`
	Guid        Guid          `xml:"guid"`
	Link        string        `xml:"link"`
	Comments    string        `xml:"comments"`
	PubDate     string        `xml:"pubDate"`
	Category    string        `xml:"category"`
	Description string        `xml:"description"`
	Enclosure   Enclosure     `xml:"enclosure"`
	Attrs       []NewznabAttr `xml:"newznab:attr"`
}

type Guid struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type Enclosure struct {
	Url    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type NewznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func music(w http.ResponseWriter, u url.URL) {
	artist := u.Query().Get("artist")
	album := u.Query().Get("album")
	q := u.Query().Get("q")

	query := q
	if query == "" {
		query = artist
		if album != "" {
			query = artist + " " + album
		}
	}

	if query == "" {
		if Debug {
			fmt.Println("searching with no query, responding empty...")
		}
		rss := Rss{
			Version: "2.0",
			Newznab: "http://www.newznab.com/DTD/2010/feeds/attributes/",
			Channel: Channel{
				Title:       "example.com",
				Description: "example.com API results",
				NewznabResponse: NewznabResponse{
					Offset: 0,
					Total:  0,
				},
				Items: []Item{},
			},
		}
		w.Write([]byte(xml.Header))
		xml.NewEncoder(w).Encode(rss)
		return
	}

	renderSearchResults(w, u, query)
}

func fetchAlbums(query string, limit int, offset int, qualityParam string) []Album {
	// Backward compatibility for callers/tests that explicitly select one source.
	if Provider == "deezer" {
		return deezerSearchAlbums(query, limit, offset)
	}
	if Provider == "qobuz" {
		return fetchQobuzAlbums(query, limit, offset, qualityParam)
	}
	// Fetch from both catalogs before applying the combined pagination window.
	qobuz := fetchQobuzAlbums(query, limit+offset, 0, qualityParam)
	deezer := deezerSearchAlbums(query, limit+offset, 0)
	for i := range qobuz {
		qobuz[i].Source = "qobuz"
	}
	for i := range deezer {
		deezer[i].Source = "deezer"
	}
	combined := mergeProviderAlbums(qobuz, deezer)
	fmt.Printf("Combined search %q: Qobuz=%d Deezer=%d\n", query, len(qobuz), len(deezer))
	if offset >= len(combined) {
		return nil
	}
	combined = combined[offset:]
	if len(combined) > limit {
		combined = combined[:limit]
	}
	return combined
}

// mergeProviderAlbums interleaves providers so one source cannot crowd out the
// other when the caller requests a short page (typically the first 10 results).
func mergeProviderAlbums(qobuz, deezer []Album) []Album {
	combined := make([]Album, 0, len(qobuz)+len(deezer))
	for i := 0; i < len(qobuz) || i < len(deezer); i++ {
		if i < len(qobuz) {
			combined = append(combined, qobuz[i])
		}
		if i < len(deezer) {
			combined = append(combined, deezer[i])
		}
	}
	return combined
}

func fetchQobuzAlbums(query string, limit int, offset int, qualityParam string) []Album {
	quality := qualityParam
	if quality == "" || quality == "default" {
		quality = QualityId
	} else if quality == "mp3-320" {
		quality = "5"
	} else if quality == "flac" {
		quality = "7"
	}
	if Provider == "deezer" && quality != "5" {
		quality = "7"
	}

	albums := fetchAlbumsOfficial(query, limit)
	if len(albums) == 0 {
		// Retry with punctuation restored between single-letter tokens.
		// Lidarr strips dots (e.g. "B.I.G." -> "B I G") which breaks Qobuz search.
		if fixed := restorePunctuation(query); fixed != query {
			if Debug {
				fmt.Printf("Official search returned 0, retrying with '%s'\n", fixed)
			}
			albums = fetchAlbumsOfficial(fixed, limit)
		}
	}
	if len(albums) == 0 {
		albums = fetchAlbumsNav(query, limit)
	}
	if len(albums) > 0 && Debug {
		fmt.Printf("Search returned %d results (id quality: %s)\n", len(albums), albums[0].Id)
	}
	return albums
}

// fetchAlbumsNav queries the Navidrums-style backend, which serves full-length
// audio and therefore also makes a reliable metadata source when the official
// API is token-limited.
func fetchAlbumsNav(query string, limit int) []Album {
	if NavLink == "" {
		return nil
	}
	albums := navSearchAlbums(query, limit)
	if len(albums) == 0 {
		if fixed := restorePunctuation(query); fixed != query {
			albums = navSearchAlbums(fixed, limit)
		}
	}
	if Debug {
		fmt.Printf("Navidrums search returned %d results\n", len(albums))
	}
	return albums
}

func restorePunctuation(query string) string {
	words := strings.Split(query, " ")
	single := make([]bool, len(words))
	for i, w := range words {
		if w == "" {
			continue
		}
		if isSingleLetter(w) {
			single[i] = true
		}
	}
	// join runs of single letters with dots: "B I G" -> "B.I.G"
	// but keep normal spaces between a word and a single letter: "Jay Z" stays "Jay Z"
	var sb strings.Builder
	for i, w := range words {
		// separate words unless they're consecutive single letters (already joined with ".")
		if i > 0 && !(single[i-1] && single[i]) {
			sb.WriteString(" ")
		}
		sb.WriteString(w)
		if single[i] {
			// dot after, unless end of a run at end of string
			if i+1 < len(words) && single[i+1] {
				sb.WriteString(".")
			}
		}
	}
	return sb.String()
}

func isSingleLetter(w string) bool {
	if len(w) != 1 {
		return false
	}
	c := w[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func fetchAlbumsOfficial(query string, limit int) []Album {
	if QobuzToken == "" {
		return nil
	}
	limitStr := strconv.Itoa(limit)
	resp, err := qobuzRequest("GET", "catalog/search", map[string]string{"query": query, "limit": limitStr, "offset": "0"})
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil
	}
	bodyStr := string(body)

	var albums []Album
	result := gjson.Get(bodyStr, "albums.items")
	result.ForEach(func(key, value gjson.Result) bool {
		var a Album
		a.Source = "qobuz"
		a.Artist = value.Get("artist.name").String()
		a.Title = value.Get("title").String()
		a.Edition = value.Get("version").String()
		a.Publisher = value.Get("label.name").String()
		a.CoverUrl = value.Get("image.small").String()

		sr := value.Get("maximum_sampling_rate").Float()
		if sr < 1000 && sr > 0 {
			sr *= 1000
		}
		a.SamplingRate = int64(sr)
		a.BitDepth = value.Get("maximum_bit_depth").Int()
		a.Id = value.Get("qobuz_id").String()
		if a.Id == "" {
			a.Id = value.Get("id").String()
		}
		a.NumTracks = value.Get("tracks_count").Int()
		a.Channels = value.Get("maximum_channel_count").Int()
		a.Duration = value.Get("duration").Int()
		a.Genre = value.Get("genre.name").String()
		a.QobuzUrl = value.Get("url").String()

		rd := value.Get("release_date_original").String()
		if rd != "" {
			if t, err := time.Parse("2006-01-02", rd); err == nil {
				a.ReleaseDate = t.Unix()
			}
		}
		if a.ReleaseDate == 0 {
			a.ReleaseDate = value.Get("released_at").Int()
		}

		sr2 := a.SamplingRate
		if sr2 == 0 {
			sr2 = 44100
		}
		bd := a.BitDepth
		if bd == 0 {
			bd = 16
		}
		ch := a.Channels
		if ch == 0 {
			ch = 2
		}
		a.Size = int64(float64((sr2*bd*ch*a.Duration)/8) * 0.7)

		albums = append(albums, a)
		return true
	})
	return albums
}

func search(w http.ResponseWriter, u url.URL) {
	query := u.Query().Get("q")
	renderSearchResults(w, u, query)
}

func renderSearchResults(w http.ResponseWriter, u url.URL, query string) {
	limit, err := strconv.Atoi(u.Query().Get("limit"))
	if err != nil {
		limit = 10
	}
	offset, err := strconv.Atoi(u.Query().Get("offset"))
	if err != nil {
		offset = 0
	}

	quality := u.Query().Get("quality")
	if quality == "" || quality == "default" {
		quality = QualityId
	} else if quality == "mp3-320" {
		quality = "5"
	} else if quality == "flac" {
		quality = "7"
	}
	Albums := fetchAlbums(query, limit, offset, quality)

	if Debug {
		fmt.Println("Total results returned from search:", len(Albums), "Quality:", quality)
	}

	items := []Item{}
	for _, album := range Albums {
		resultQuality := quality
		if album.Source == "deezer" {
			resultQuality = "7" // Deezer downloads remain FLAC without transcoding.
		}
		if resultQuality == "5" {
			album.Size = int64(320 * 1000 * album.Duration / 8)
		}
		// Removed regex sanitization of album.Title and album.Artist

		var categoryName string
		var categoryAttrs []NewznabAttr
		release := time.Unix(album.ReleaseDate, 0)
		yearStr := strconv.Itoa(release.Year())

		if resultQuality == "5" {
			// MP3 320
			categoryName = "Audio > MP3"
			categoryAttrs = []NewznabAttr{
				{Name: "category", Value: "3000"},
				{Name: "category", Value: "3010"},
				{Name: "size", Value: strconv.FormatInt(album.Size, 10)},
				{Name: "tracks", Value: strconv.FormatInt(album.NumTracks, 10)},
				{Name: "files", Value: strconv.FormatInt(album.NumTracks, 10)},
				{Name: "duration", Value: strconv.FormatInt(album.Duration, 10)},
				{Name: "year", Value: yearStr},
				{Name: "publisher", Value: album.Publisher},
				{Name: "coverurl", Value: album.CoverUrl},
				{Name: "artist", Value: album.Artist},
				{Name: "album", Value: album.Title},
			}
		} else {
			// FLAC / Lossless (default)
			categoryName = "Audio > Lossless"
			categoryAttrs = []NewznabAttr{
				{Name: "category", Value: "3000"},
				{Name: "category", Value: "3040"},
				{Name: "size", Value: strconv.FormatInt(album.Size, 10)},
				{Name: "tracks", Value: strconv.FormatInt(album.NumTracks, 10)},
				{Name: "files", Value: strconv.FormatInt(album.NumTracks, 10)},
				{Name: "duration", Value: strconv.FormatInt(album.Duration, 10)},
				{Name: "year", Value: yearStr},
				{Name: "publisher", Value: album.Publisher},
				{Name: "coverurl", Value: album.CoverUrl},
				{Name: "artist", Value: album.Artist},
				{Name: "album", Value: album.Title},
			}
		}

		items = append(items, Item{
			Title: releaseName(album, resultQuality),
			Guid: Guid{
				IsPermaLink: true,
				Value:       album.QobuzUrl,
			},
			Link:        album.QobuzUrl,
			Comments:    album.QobuzUrl,
			PubDate:     time.Unix(album.ReleaseDate, 0).Format("Mon, 02 Jan 2006 15:04:05 -0700"),
			Category:    categoryName,
			Description: album.Artist + " " + album.Title,
			Enclosure: Enclosure{
				Url:  "/indexer?t=fakenzb&qobuzid=" + url.QueryEscape(album.Id) + "&numtracks=" + strconv.FormatInt(album.NumTracks, 10) + "&apikey=" + ApiKey + "&quality=" + resultQuality + "&source=" + album.Source + "&artist=" + url.QueryEscape(album.Artist) + "&album=" + url.QueryEscape(album.Title),
				Type: "application/x-nzb",
			},
			Attrs: append(categoryAttrs, NewznabAttr{Name: "genre", Value: album.Genre}, NewznabAttr{Name: "source", Value: album.Source}),
		})
	}

	rss := Rss{
		Version: "2.0",
		Newznab: "http://www.newznab.com/DTD/2010/feeds/attributes/",
		Channel: Channel{
			Title:       "example.com",
			Description: "example.com API results",
			NewznabResponse: NewznabResponse{
				Offset: 0,
				Total:  len(Albums),
			},
			Items: items,
		},
	}

	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(rss)
}

func releaseName(album Album, quality string) (name string) {
	release := time.Unix(album.ReleaseDate, 0)
	provider := releaseProviderTag(album.Source)
	if quality == "5" {
		name = album.Artist + "-" + album.Title + "-WEB-320-MP3-" + strconv.Itoa(release.Year()) + "-" + provider
	} else {
		samplingRateKHz := float64(album.SamplingRate) / 1000.0
		samplingRateStr := strconv.FormatFloat(samplingRateKHz, 'f', -1, 64)
		name = album.Artist + "-" + album.Title + "-" + strconv.FormatInt(album.BitDepth, 10) + "BIT-" + samplingRateStr + "-KHZ-WEB-FLAC-" + strconv.Itoa(release.Year()) + "-" + provider
	}
	return name
}

func releaseProviderTag(source string) string {
	if strings.EqualFold(source, "deezer") {
		return "DEEZER"
	}
	return "QOBUZ"
}

func fakenzb(w http.ResponseWriter, u url.URL) {
	QobuzId := u.Query().Get("qobuzid")
	NumTracks := u.Query().Get("numtracks")
	Quality := u.Query().Get("quality")
	Artist := u.Query().Get("artist")
	Album := u.Query().Get("album")
	Source := u.Query().Get("source")
	w.Header().Set("Content-Type", "application/x-nzb")
	response := "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n" +
		"<!DOCTYPE nzb PUBLIC \"-//newzBin//DTD NZB 1.0//EN\" \"http://www.newzbin.com/DTD/nzb/nzb-1.0.dtd\">\n" +
		"<!-- " + QobuzId + "  -->\n" +
		"<!-- " + NumTracks + " -->\n" +
		"<!-- " + Quality + " -->\n" +
		"<!-- " + Artist + " -->\n" +
		"<!-- " + Album + " -->\n" +
		"<!-- " + Source + " -->\n" +
		"<nzb>\n" +
		"    <file post_id=\"1\">\n" +
		"        <groups>\n" +
		"            <group>squidwtf</group>\n" +
		"        </groups>\n" +
		"        <segments>\n" +
		"            <segment number=\"1\">ExampleSegmentID@news.example.com</segment>\n" +
		"        </segments>\n" +
		"    </file>\n" +
		"</nzb>"
	w.Write([]byte(response))
}
