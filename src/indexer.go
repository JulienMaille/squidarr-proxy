package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tidwall/gjson"
)

type Album struct {
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
	Title       string      `xml:"title"`
	Guid        Guid        `xml:"guid"`
	Link        string      `xml:"link"`
	Comments    string      `xml:"comments"`
	PubDate     string      `xml:"pubDate"`
	Category    string      `xml:"category"`
	Description string      `xml:"description"`
	Enclosure   Enclosure   `xml:"enclosure"`
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
	if u.Query().Get("q") == "" {
		if Debug {
			fmt.Println("searching with no query, responding garbage...")
		}
		rss := Rss{
			Version: "2.0",
			Newznab: "http://www.newznab.com/DTD/2010/feeds/attributes/",
			Channel: Channel{
				Title:       "example.com",
				Description: "example.com API results",
				NewznabResponse: NewznabResponse{
					Offset: 0,
					Total:  1234,
				},
				Items: []Item{
					{
						Title: "A.Public.Domain.Album.Name",
						Guid: Guid{
							IsPermaLink: true,
							Value:       "http://servername.com/rss/viewnzb/e9c515e02346086e3a477a5436d7bc8c",
						},
						Link:        "http://servername.com/rss/nzb/e9c515e02346086e3a477a5436d7bc8c&i=1&r=18cf9f0a736041465e3bd521d00a90b9",
						Comments:    "http://servername.com/rss/viewnzb/e9c515e02346086e3a477a5436d7bc8c#comments",
						PubDate:     "Sun, 06 Jun 2010 17:29:23 +0100",
						Category:    "Music > MP3",
						Description: "Some music",
						Enclosure: Enclosure{
							Url:    "http://servername.com/rss/nzb/e9c515e02346086e3a477a5436d7bc8c&i=1&r=18cf9f0a736041465e3bd521d00a90b9",
							Length: 154653309,
							Type:   "application/x-nzb",
						},
						Attrs: []NewznabAttr{
							{Name: "category", Value: "3000"},
							{Name: "category", Value: "3010"},
							{Name: "size", Value: "144967295"},
							{Name: "artist", Value: "Bob Smith"},
							{Name: "album", Value: "Groovy Tunes"},
							{Name: "publisher", Value: "Epic Music"},
							{Name: "year", Value: "2011"},
							{Name: "tracks", Value: "track one|track two|track three"},
							{Name: "duration", Value: "3600"},
							{Name: "coverurl", Value: "http://servername.com/covers/music/12345.jpg"},
							{Name: "review", Value: "This album is great"},
						},
					},
				},
			},
		}

		w.Write([]byte(xml.Header))
		xml.NewEncoder(w).Encode(rss)
		return
	}
}

func fetchAlbums(query string, limit int, offset int, qualityParam string) []Album {
	quality := qualityParam
	if quality == "" || quality == "default" {
		quality = QualityId
	} else if quality == "mp3-320" {
		quality = "5"
	} else if quality == "flac" {
		quality = "7"
	}

	albums := fetchAlbumsOfficial(query, limit)
	if len(albums) == 0 {
		albums = fetchAlbumsProxy(query, limit, offset, quality)
	} else if Debug {
		fmt.Printf("Official API search returned %d results\n", len(albums))
	}
	return albums
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
		a.Id = value.Get("id").String()
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

func fetchAlbumsProxy(query string, limit int, offset int, quality string) []Album {
	var Albums []Album

	escapedQuery := url.QueryEscape(query)

	batchSize := 50
	for i := 0; i < limit; i += batchSize {
		var endpoint string = "/search?q=" + escapedQuery + "&offset=" + (strconv.Itoa(offset + i)) + "&limit=" + strconv.Itoa(batchSize)
		resp, err := apiRequest(endpoint)
		if err != nil {
			fmt.Println(err)
			return Albums
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println(err)
			return Albums
		}
		bodyStr := string(bodyBytes)
		total := int(gjson.Get(bodyStr, "albums.total").Int())
		if total == 0 {
			total = int(gjson.Get(bodyStr, "total").Int())
		}
		if total == 0 {
			total = len(gjson.Get(bodyStr, "albums.items").Array())
		}
		if total < limit && total > 0 {
			limit = total
		}
		result := gjson.Get(bodyStr, "albums.items")
		if !result.Exists() {
			result = gjson.Get(bodyStr, "items")
		}
		result.ForEach(func(key, value gjson.Result) bool {
			var album Album
			// DuckDNS API returns items directly (no type/content wrapper)
			var content gjson.Result = value
			if !content.IsObject() {
				return true
			}

			album.Artist = content.Get("artist.name").String()
			album.Title = content.Get("title").String()
			album.Edition = content.Get("version").String()
			album.ReleaseDate = content.Get("released_at").Int()
			album.Publisher = content.Get("label.name").String()
			album.CoverUrl = content.Get("image.small").String()

			samplingRate := content.Get("maximum_sampling_rate").Float()
			if samplingRate < 1000 && samplingRate > 0 {
				samplingRate *= 1000
			}
			album.SamplingRate = int64(samplingRate)

			album.BitDepth = content.Get("maximum_bit_depth").Int()
			album.Id = content.Get("id").String()
			album.NumTracks = content.Get("tracks_count").Int()
			album.Channels = content.Get("maximum_channel_count").Int()
			album.Duration = content.Get("duration").Int()

			album.Genre = content.Get("genre.name").String()
			album.QobuzUrl = content.Get("url").String()

			if quality == "5" {
				album.Size = int64(320 * 1000 * album.Duration / 8)
			} else {
				sr := album.SamplingRate
				if sr == 0 {
					sr = 44100
				}
				bd := album.BitDepth
				if bd == 0 {
					bd = 16
				}
				ch := album.Channels
				if ch == 0 {
					ch = 2
				}
				album.Size = int64(float64((sr*bd*ch*album.Duration)/8) * 0.7)
			}
			Albums = append(Albums, album)
			return true
		})
	}
	return Albums
}

func search(w http.ResponseWriter, u url.URL) {
	//doing the actual querying request
	//getting the query parameters
	limit, err := strconv.Atoi(u.Query().Get("limit"))
	if err != nil {
		limit = 10
	}
	query := u.Query().Get("q")
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
		// Removed regex sanitization of album.Title and album.Artist

		var categoryName string
		var categoryAttrs []NewznabAttr
		release := time.Unix(album.ReleaseDate, 0)
		yearStr := strconv.Itoa(release.Year())

		if quality == "5" {
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
			Title: releaseName(album, quality),
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
				Url:    "/indexer?t=fakenzb&qobuzid=" + album.Id + "&numtracks=" + strconv.FormatInt(album.NumTracks, 10) + "&apikey=" + ApiKey + "&quality=" + quality,
				Type:   "application/x-nzb",
			},
			Attrs: append(categoryAttrs, NewznabAttr{Name: "genre", Value: album.Genre}),
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
	if quality == "5" {
		name = album.Artist + "-" + album.Title + "-WEB-320-MP3-" + strconv.Itoa(release.Year()) + "-SQUIDWTF"
	} else {
		samplingRateKHz := float64(album.SamplingRate) / 1000.0
		samplingRateStr := strconv.FormatFloat(samplingRateKHz, 'f', -1, 64)
		name = album.Artist + "-" + album.Title + "-" + strconv.FormatInt(album.BitDepth, 10) + "BIT-" + samplingRateStr + "-KHZ-WEB-FLAC-" + strconv.Itoa(release.Year()) + "-SQUIDWTF"
	}
	return name
}

func fakenzb(w http.ResponseWriter, u url.URL) {
	QobuzId := u.Query().Get("qobuzid")
	NumTracks := u.Query().Get("numtracks")
	Quality := u.Query().Get("quality")
	w.Header().Set("Content-Type", "application/x-nzb")
	response := "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n" +
		"<!DOCTYPE nzb PUBLIC \"-//newzBin//DTD NZB 1.0//EN\" \"http://www.newzbin.com/DTD/nzb/nzb-1.0.dtd\">\n" +
		"<!-- " + QobuzId + "  -->\n" +
		"<!-- " + NumTracks + " -->\n" +
		"<!-- " + Quality + " -->\n" +
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
