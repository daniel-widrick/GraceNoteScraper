package guide

import (
	"reflect"
	"testing"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/web"
)

func sampleChannel() web.JSONChannel {
	return web.JSONChannel{
		ChannelID:         "53158",
		ID:                "531580",
		ChannelNo:         "2.1",
		CallSign:          "WKTVDT",
		AffiliateName:     "NATIONAL BROADCASTING COMPANY",
		AffiliateCallSign: "null",
		StationFilters:    []string{"filter-sports", "filter-news"},
		Thumbnail:         "//images.example.invalid/station/53158.png?w=55",
	}
}

func TestConvertChannelCarriesRawFields(t *testing.T) {
	ch := ConvertChannel(sampleChannel())

	if ch.ID != "53158" || ch.ChannelNo != "2.1" || ch.CallSign != "WKTVDT" {
		t.Fatalf("basic fields wrong: %+v", ch)
	}
	if ch.PlacementID != "531580" {
		t.Errorf("PlacementID = %q", ch.PlacementID)
	}
	if ch.AffiliateCallSign != "" {
		t.Errorf(`"null" affiliate callsign should normalize to empty, got %q`, ch.AffiliateCallSign)
	}
	if !reflect.DeepEqual(ch.Filters, []string{"sports", "news"}) {
		t.Errorf("Filters = %v", ch.Filters)
	}
	if ch.IconURL != "http://images.example.invalid/station/53158.png" {
		t.Errorf("IconURL = %q", ch.IconURL)
	}
	// Existing XMLTV-facing fields must be untouched by the additions.
	wantNames := []DisplayName{{"2.1 WKTVDT"}, {"2.1"}, {"WKTVDT"}, {"NATIONAL BROADCASTING COMPANY"}}
	if !reflect.DeepEqual(ch.DisplayNames, wantNames) {
		t.Errorf("DisplayNames = %v", ch.DisplayNames)
	}
}

func TestConvertChannelKeepsRealAffiliateCallSign(t *testing.T) {
	in := sampleChannel()
	in.AffiliateCallSign = "NBC"
	in.StationFilters = nil
	ch := ConvertChannel(in)
	if ch.AffiliateCallSign != "NBC" {
		t.Errorf("AffiliateCallSign = %q", ch.AffiliateCallSign)
	}
	if ch.Filters != nil {
		t.Errorf("nil station filters should stay nil, got %v", ch.Filters)
	}
}

func sampleEvent() web.JSONEvent {
	season, episode, title := "3", "7", "The One"
	return web.JSONEvent{
		StartTime: "2026-07-25T07:00:00Z",
		EndTime:   "2026-07-25T08:00:00Z",
		Duration:  "60",
		SeriesID:  "SH06270099",
		Flag:      []string{"New", "Finale"},
		Filter:    []string{"filter-sports"},
		Program: web.JSONProgram{
			ID:           "EP062700990343",
			TmsID:        "EP062700990343",
			Title:        "Morning Business",
			EpisodeTitle: &title,
			Season:       &season,
			Episode:      &episode,
			ReleaseYear:  "2019",
			IsGeneric:    true,
		},
	}
}

func TestConvertEventSeparatesRawFiltersFromCategories(t *testing.T) {
	p := ConvertEvent(sampleEvent(), "53158", "en-us", "USA")

	if !reflect.DeepEqual(p.Filters, []string{"sports"}) {
		t.Errorf("Filters = %v", p.Filters)
	}
	// Categories keep the historical shape: raw filters, then Series (from an
	// episode number), then Finale (from the flag).
	want := []Category{{"sports", "en-us"}, {"Series", "en-us"}, {"Finale", "en-us"}}
	if !reflect.DeepEqual(p.Categories, want) {
		t.Errorf("Categories = %v, want %v", p.Categories, want)
	}
	if p.TMSID != "EP062700990343" || p.ReleaseYear != "2019" || !p.Generic {
		t.Errorf("program metadata = tms %q year %q generic %v", p.TMSID, p.ReleaseYear, p.Generic)
	}
	if !p.New || p.PreviouslyShown {
		t.Errorf("flag handling changed: new=%v previouslyShown=%v", p.New, p.PreviouslyShown)
	}
}

func TestConvertEventWithoutFilters(t *testing.T) {
	ev := sampleEvent()
	ev.Filter = nil
	ev.Flag = nil
	ev.Program.Season, ev.Program.Episode = nil, nil
	p := ConvertEvent(ev, "53158", "en", "USA")
	if p.Filters != nil {
		t.Errorf("Filters should be nil, got %v", p.Filters)
	}
	if len(p.Categories) != 0 {
		t.Errorf("Categories should be empty, got %v", p.Categories)
	}
}

func TestConvertLineupPosition(t *testing.T) {
	in := sampleChannel()
	p := ConvertLineupPosition(in)
	want := LineupPosition{
		ChannelNo:         "2.1",
		StationID:         "53158",
		PlacementID:       "531580",
		CallSign:          "WKTVDT",
		Affiliate:         "NATIONAL BROADCASTING COMPANY",
		AffiliateCallSign: "",
		Filters:           []string{"sports", "news"},
		LogoURL:           "http://images.example.invalid/station/53158.png",
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %+v\nwant %+v", p, want)
	}
	if p.Key() != "2.1|53158" {
		t.Errorf("Key = %q", p.Key())
	}
	other := in
	other.ChannelNo = "1002"
	other.ID = "5315899"
	if ConvertLineupPosition(other).Key() == p.Key() {
		t.Error("same station at a different number must be a distinct position")
	}
}

func TestChannelNumberLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.1", "10", true},
		{"10", "2.1", false},
		{"10", "100", true},
		{"9", "10", true},
		{"2.1", "2.10", true}, // equal numerically, string order breaks the tie
		{"2.10", "2.1", false},
		{"5", "5", false},
		{"12", "ABC", true}, // numeric before non-numeric
		{"ABC", "12", false},
		{"ABC", "ABD", true},
	}
	for _, c := range cases {
		if got := ChannelNumberLess(c.a, c.b); got != c.want {
			t.Errorf("ChannelNumberLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSortLineup(t *testing.T) {
	positions := []LineupPosition{
		{ChannelNo: "100", StationID: "b"},
		{ChannelNo: "ZZ", StationID: "z"},
		{ChannelNo: "2.1", StationID: "a"},
		{ChannelNo: "10", StationID: "c"},
		{ChannelNo: "10", StationID: "a"},
		{ChannelNo: "9", StationID: "d"},
	}
	SortLineup(positions)
	var got []string
	for _, p := range positions {
		got = append(got, p.ChannelNo+"/"+p.StationID)
	}
	want := []string{"2.1/a", "9/d", "10/a", "10/c", "100/b", "ZZ/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestSourceFromPreferences(t *testing.T) {
	at := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	src := SourceFromPreferences(web.Preferences{
		Country: "USA", ZipCode: "13490", Headend: "lineupId", LineupId: "USA-lineupId-DEFAULT", Device: "-", Language: "en-us",
	}, at)
	want := Source{Country: "USA", PostalCode: "13490", HeadendID: "lineupId", LineupID: "USA-lineupId-DEFAULT", Device: "-", Language: "en-us", GeneratedAt: at}
	if src != want {
		t.Fatalf("got %+v want %+v", src, want)
	}
}
