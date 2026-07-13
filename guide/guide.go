package guide

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/daniel-widrick/GraceNoteScraper/web"
)

type TVGuide struct {
	Channels []Channel
	Programs []Program
}

type Channel struct {
	ID           string
	DisplayNames []DisplayName
	IconURL      string
	CallSign     string // internal, not in template
	Affiliate    string // internal, not in template
	ChannelNo    string // internal, not in template
}

type DisplayName struct {
	Name string
}

type Program struct {
	Start           string
	Stop            string
	Channel         string
	Lang            string
	Title           string
	SubTitle        string
	Description     string
	LengthUnits     string
	Length          string
	IconSrc         string
	Images          []Image
	URL             string
	Language        string
	OrigLanguage    string
	Country         string
	EpisodeNumbers  []EpisodeNumber
	Categories      []Category
	New             bool
	Premiere        bool
	PreviouslyShown bool
	Subtitles       []Subtitle
	Rating          string
	RatingSystem    string
	StarRating      string
	Date            string
}

type Image struct {
	URL    string
	Type   string
	Size   string
	Orient string
	System string
}

type EpisodeNumber struct {
	System        string
	EpisodeNumber string
}

type Category struct {
	Name string
	Lang string
}

type Subtitle struct {
	Type string
}

// escapes &, <, > for safe XML text content.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// converts "2025-08-06T02:00:00Z" to "20250806020000 +0000"
func formatXMLTVTime(iso string) string {
	s := iso
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "T", "")
	s = strings.ReplaceAll(s, ":", "")
	s = strings.ReplaceAll(s, "Z", " +0000")
	return s
}

// converts a JSON channel to a template Channel struct.
func ConvertChannel(ch web.JSONChannel) Channel {
	// Build icon URL: strip leading slashes, strip query params, prepend http://
	iconURL := ""
	if ch.Thumbnail != "" {
		raw := ch.Thumbnail
		// Strip query string
		if idx := strings.Index(raw, "?"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimLeft(raw, "/")
		if raw != "" {
			iconURL = "http://" + raw
		}
	}

	return Channel{
		ID: ch.ChannelID,
		DisplayNames: []DisplayName{
			{Name: xmlEscape(ch.ChannelNo + " " + ch.CallSign)},
			{Name: xmlEscape(ch.ChannelNo)},
			{Name: xmlEscape(ch.CallSign)},
			{Name: xmlEscape(titleCase(ch.AffiliateName))},
		},
		IconURL:   iconURL,
		CallSign:  ch.CallSign,
		Affiliate: ch.AffiliateName,
		ChannelNo: ch.ChannelNo,
	}
}

// converts a JSON event to a template Program struct.
func ConvertEvent(ev web.JSONEvent, channelID, lang, country string) Program {
	season := 0
	episode := 0

	if ev.Program.Season != nil {
		if v, err := strconv.Atoi(*ev.Program.Season); err == nil {
			season = v
		}
	}
	if ev.Program.Episode != nil {
		if v, err := strconv.Atoi(*ev.Program.Episode); err == nil {
			episode = v
		}
	}

	// SubTitle
	subTitle := ""
	if ev.Program.EpisodeTitle != nil {
		subTitle = xmlEscape(*ev.Program.EpisodeTitle)
	}

	// Description
	desc := "Unavailable"
	if ev.Program.ShortDesc != nil {
		desc = xmlEscape(*ev.Program.ShortDesc)
	}

	// Icon URL
	iconSrc := ""
	if ev.Thumbnail != "" {
		iconSrc = "http://zap2it.tmsimg.com/assets/" + ev.Thumbnail + ".jpg"
	}

	// URL
	programURL := "https://tvlistings.gracenote.com//overview.html?programSeriesId=" + ev.SeriesID + "&amp;tmsId=" + ev.Program.ID

	// Categories from filter array (strip "filter-" prefix)
	var categories []Category
	for _, f := range ev.Filter {
		name := strings.TrimPrefix(f, "filter-")
		categories = append(categories, Category{Name: name, Lang: lang})
	}

	// Episode numbers
	var episodeNumbers []EpisodeNumber

	if episode != 0 {
		// Add "Series" category
		categories = append(categories, Category{Name: "Series", Lang: lang})

		// onscreen: S01E05
		onscreen := fmt.Sprintf("S%02dE%02d", season, episode)
		episodeNumbers = append(episodeNumbers, EpisodeNumber{
			System:        "onscreen",
			EpisodeNumber: onscreen,
		})

		// xmltv_ns: season-1.episode-1
		seasonStr := ""
		if season != 0 {
			seasonStr = fmt.Sprintf("%d", season-1)
		}
		xmltvNS := fmt.Sprintf("%s.%d", seasonStr, episode-1)
		episodeNumbers = append(episodeNumbers, EpisodeNumber{
			System:        "xmltv_ns",
			EpisodeNumber: xmltvNS,
		})
	}

	// dd_progid
	progID := ev.Program.ID
	suffix := ""
	if len(progID) >= 4 {
		suffix = progID[len(progID)-4:]
	}
	var ddProgID string
	if suffix == "0000" {
		ddProgID = ev.SeriesID + "." + suffix
	} else {
		ddProgID = strings.Replace(ev.SeriesID, "SH", "EP", 1) + "." + suffix
	}
	episodeNumbers = append(episodeNumbers, EpisodeNumber{
		System:        "dd_progid",
		EpisodeNumber: ddProgID,
	})

	// Flags
	isNew := false
	isPremiere := false
	for _, flag := range ev.Flag {
		switch flag {
		case "New":
			isNew = true
		case "Premiere":
			isPremiere = true
		case "Finale":
			// Map Finale to a category since it's not in the XMLTV DTD
			categories = append(categories, Category{Name: "Finale", Lang: lang})
		}
	}

	// Subtitles from tags
	var subtitles []Subtitle
	for _, tag := range ev.Tags {
		if tag == "CC" {
			subtitles = append(subtitles, Subtitle{Type: "teletext"})
		}
	}

	// Rating
	rating := ""
	if ev.Rating != nil {
		rating = *ev.Rating
	}

	// Rating system - detect from value format
	ratingSystem := ""
	if rating != "" {
		if strings.HasPrefix(rating, "TV-") {
			ratingSystem = "USA Parental Rating"
		}
	}

	p := Program{
		Start:           formatXMLTVTime(ev.StartTime),
		Stop:            formatXMLTVTime(ev.EndTime),
		Channel:         channelID,
		Lang:            lang,
		Title:           xmlEscape(ev.Program.Title),
		SubTitle:        subTitle,
		Description:     desc,
		LengthUnits:     "minutes",
		Length:          ev.Duration,
		IconSrc:         iconSrc,
		URL:             programURL,
		Language:        lang,
		Country:         country,
		EpisodeNumbers:  episodeNumbers,
		Categories:      categories,
		New:             isNew,
		Premiere:        isPremiere,
		PreviouslyShown: !isNew,
		Subtitles:       subtitles,
		Rating:          rating,
		RatingSystem:    ratingSystem,
	}

	return p
}

// uppercases the first letter of each word (simple replacement for deprecated strings.Title).
func titleCase(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(rune(prev)) || prev == ' ' {
			prev = r
			return unicode.ToUpper(r)
		}
		prev = r
		return r
	}, s)
}
