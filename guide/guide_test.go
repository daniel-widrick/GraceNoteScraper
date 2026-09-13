package guide

import (
	"reflect"
	"testing"

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
