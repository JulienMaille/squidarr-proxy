package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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

func fetchAlbums(query string, limit int, offset int) []Album {
	var Albums []Album
	//squid.wtf seems to only be able to output 10 items (albums/tracks each) at once
	//so iterate over 10 items at a time until reaching the limit...
	for i := 0; i < limit; i += 10 {
		var queryUrl string = ApiLink + "/get-music?q=" + query + "&offset=" + (strconv.Itoa(offset + i)) + "&limit=10"
		if Debug {
			fmt.Println("Searching with query:", queryUrl)
		}
		resp, err := http.Get(queryUrl)
		if err != nil {
			fmt.Println(err)
			return Albums
		}
		//making the request body usable
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println(err)
			return Albums
		}
		//check the number of results and modify limit to avoid unnecessary requests
		//lidarr seems to always start with 100
		total := int(gjson.Get(string(bodyBytes), "data.albums.total").Int())
		if total == 0 {
			total = int(gjson.Get(string(bodyBytes), "data.total").Int())
		}
		if total == 0 {
		    // if total is 0, let's just see how many items are returned in data.albums.items
		    total = len(gjson.Get(string(bodyBytes), "data.albums.items").Array())
		}
		if total < limit {
			limit = total
		}
		//iterate over each album and create an Album struct object from it
		result := gjson.Get(string(bodyBytes), "data.albums.items")
		result.ForEach(func(key, value gjson.Result) bool {
			var album Album
			typ := value.Get("type").String()
			var content gjson.Result
			if typ == "albums" {
				content = value.Get("content")
			} else if typ == "tracks" {
				content = value.Get("content.album")
			} else {
				return true
			}

			var resultString string = content.String()
			album.Artist = gjson.Get(resultString, "artist.name").String()
			album.Title = gjson.Get(resultString, "title").String()
			album.Edition = gjson.Get(resultString, "version").String()
			album.ReleaseDate = gjson.Get(resultString, "released_at").Int()
			album.Publisher = gjson.Get(resultString, "label.name").String()
			album.CoverUrl = gjson.Get(resultString, "image.small").String()
			album.SamplingRate = int64(gjson.Get(resultString, "maximum_sampling_rate").Float())
			album.BitDepth = gjson.Get(resultString, "maximum_bit_depth").Int()
			album.Id = gjson.Get(resultString, "id").String()
			album.NumTracks = gjson.Get(resultString, "tracks_count").Int()
			album.Channels = gjson.Get(resultString, "maximum_channel_count").Int()
			album.Duration = gjson.Get(resultString, "duration").Int()
			//guesstimate filesize based on Sampling Rate, Bit Depth, Channel count and duration
			//assuming all tracks of that album have the same specifications and that FLAC is 70% as large as WAV
			// (Sampling Rate in Hz * Bit depth * channels * seconds) / 8 to get it from bits to bytes
			if QualityId == "5" {
				// MP3 320kbps
				album.Size = int64(320 * 1000 * album.Duration / 8)
			} else {
				// FLAC (default)
				album.Size = int64(float64(((album.SamplingRate * 1000) * (album.BitDepth * album.Channels * album.Duration) / 8)) * 0.7)
			}
			Albums = append(Albums, album)
			return true // keep iterating
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
	rawQuery := u.Query().Get("q")
	var query string = strings.Replace(rawQuery, " ", "+", -1)
	offset, err := strconv.Atoi(u.Query().Get("offset"))
	if err != nil {
		offset = 0
	}
	
	Albums := fetchAlbums(query, limit, offset)

	if Debug {
		fmt.Println("Total results returned from search:", len(Albums))
	}

	// Check if we need to fetch more results without year
	re := regexp.MustCompile(`\s\d{4}$`)
	if len(Albums) < limit && re.MatchString(rawQuery) {
		cleanQuery := re.ReplaceAllString(rawQuery, "")
		cleanQueryUrlEncoded := strings.Replace(cleanQuery, " ", "+", -1)
		
		targetTotal := limit - 1
		needed := targetTotal - len(Albums)
		
		if needed > 0 {
			moreAlbums := fetchAlbums(cleanQueryUrlEncoded, needed, 0)
			Albums = append(Albums, moreAlbums...)
		}
	}

	items := []Item{}
	for _, album := range Albums {
		// Removed regex sanitization of album.Title and album.Artist

		var categoryName string
		var categoryAttrs []NewznabAttr

		if QualityId == "5" {
			// MP3 320
			categoryName = "Audio > MP3"
			categoryAttrs = []NewznabAttr{
				{Name: "category", Value: "3000"},
				{Name: "category", Value: "3010"},
				{Name: "size", Value: strconv.FormatInt(album.Size, 10)},
			}
		} else {
			// FLAC / Lossless (default)
			categoryName = "Audio > Lossless"
			categoryAttrs = []NewznabAttr{
				{Name: "category", Value: "3000"},
				{Name: "category", Value: "3040"},
				{Name: "size", Value: strconv.FormatInt(album.Size, 10)},
			}
		}

		items = append(items, Item{
			Title: releaseName(album),
			Guid: Guid{
				IsPermaLink: true,
				Value:       "https://www.qobuz.com/ie-en/album/" + album.Id,
			},
			Link:        "https://www.qobuz.com/ie-en/album/" + album.Id,
			Comments:    "https://www.qobuz.com/ie-en/album/" + album.Id,
			PubDate:     time.Unix(album.ReleaseDate, 0).Format("Mon, 02 Jan 2006 15:04:05 -0700"),
			Category:    categoryName,
			Description: album.Artist + " " + album.Title,
			Enclosure: Enclosure{
				Url:    "/indexer?t=fakenzb&qobuzid=" + album.Id + "&numtracks=" + strconv.FormatInt(album.NumTracks, 10) + "&apikey=" + ApiKey,
				Type:   "application/x-nzb",
			},
			Attrs: categoryAttrs,
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

func releaseName(album Album) (name string) {
	release := time.Unix(album.ReleaseDate, 0)
	if QualityId == "5" {
		name = album.Artist + "-" + album.Title + "-WEB-320-MP3-" + strconv.Itoa(release.Year()) + "-SQUIDWTF"
	} else {
		name = album.Artist + "-" + album.Title + "-" + strconv.FormatInt(album.BitDepth, 10) + "BIT-" + strconv.FormatInt(album.SamplingRate, 10) + "-KHZ-WEB-FLAC-" + strconv.Itoa(release.Year()) + "-SQUIDWTF"
	}
	return name
}

func fakenzb(w http.ResponseWriter, u url.URL) {
	QobuzId := u.Query().Get("qobuzid")
	NumTracks := u.Query().Get("numtracks")
	w.Header().Set("Content-Type", "application/x-nzb")
	response := "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n" +
		"<!DOCTYPE nzb PUBLIC \"-//newzBin//DTD NZB 1.0//EN\" \"http://www.newzbin.com/DTD/nzb/nzb-1.0.dtd\">\n" +
		"<!-- " + QobuzId + "  -->\n" +
		"<!-- " + NumTracks + " -->\n" +
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
