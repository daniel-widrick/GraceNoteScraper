package main

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/daniel-widrick/GraceNoteScraper/guide"
)

func sampleLineupGuide() *guide.TVGuide {
	return &guide.TVGuide{
		Channels: []guide.Channel{
			{ID: "s1", ChannelNo: "2", IconURL: "http://logos.example/s1.png", DisplayNames: []guide.DisplayName{{Name: "2 AAA"}, {Name: "2"}, {Name: "AAA"}}},
			{ID: "s2", ChannelNo: "5", IconURL: "", DisplayNames: []guide.DisplayName{{Name: "5 BBB"}, {Name: "5"}, {Name: "BBB"}}},
		},
		Programs: []guide.Program{
			{Channel: "s1", Title: "one", IconSrc: "http://img.example/a.jpg", Images: []guide.Image{{URL: "http://img.example/b.jpg"}}},
			{Channel: "s2", Title: "two"},
		},
		Lineup: []guide.LineupPosition{
			{ChannelNo: "2", StationID: "s1"},
			{ChannelNo: "5", StationID: "s2"},
			{ChannelNo: "1002", StationID: "s1"}, // same station, second position
		},
		Source: guide.Source{LineupID: "L"},
	}
}

func TestPropagateChannelLogosCopiesByStation(t *testing.T) {
	g := sampleLineupGuide()
	propagateChannelLogos(g.Channels, g.Lineup)

	if g.Lineup[0].LogoURL != "http://logos.example/s1.png" || g.Lineup[2].LogoURL != "http://logos.example/s1.png" {
		t.Fatalf("both positions of s1 should carry its logo: %+v", g.Lineup)
	}
	if g.Lineup[1].LogoURL != "" {
		t.Fatalf("s2 has no logo, got %q", g.Lineup[1].LogoURL)
	}
}

func TestRewriteImageURLsCoversLineup(t *testing.T) {
	g := sampleLineupGuide()
	propagateChannelLogos(g.Channels, g.Lineup)
	rewriteImageURLs("http://host:8080/", g.Channels, g.Lineup, g.Programs)

	want := "http://host:8080/img?url=" + url.QueryEscape("http://logos.example/s1.png")
	if g.Channels[0].IconURL != want {
		t.Errorf("channel icon = %q, want %q", g.Channels[0].IconURL, want)
	}
	if g.Lineup[0].LogoURL != want || g.Lineup[2].LogoURL != want {
		t.Errorf("lineup logos = %q / %q, want %q", g.Lineup[0].LogoURL, g.Lineup[2].LogoURL, want)
	}
	if g.Lineup[1].LogoURL != "" || g.Channels[1].IconURL != "" {
		t.Error("empty URLs must stay empty")
	}
	if g.Programs[0].IconSrc != "http://host:8080/img?url="+url.QueryEscape("http://img.example/a.jpg") {
		t.Errorf("program icon = %q", g.Programs[0].IconSrc)
	}
	if g.Programs[0].Images[0].URL != "http://host:8080/img?url="+url.QueryEscape("http://img.example/b.jpg") {
		t.Errorf("program image = %q", g.Programs[0].Images[0].URL)
	}
}

func TestFilterGuideChannelsFiltersLineup(t *testing.T) {
	g := sampleLineupGuide()
	filtered := filterGuideChannels(g, map[string]bool{"2": true, "1002": true})

	if len(filtered.Channels) != 1 || filtered.Channels[0].ID != "s1" {
		t.Fatalf("channels = %+v", filtered.Channels)
	}
	if len(filtered.Programs) != 1 || filtered.Programs[0].Title != "one" {
		t.Fatalf("programs = %+v", filtered.Programs)
	}
	var numbers []string
	for _, p := range filtered.Lineup {
		numbers = append(numbers, p.ChannelNo)
	}
	if !reflect.DeepEqual(numbers, []string{"2", "1002"}) {
		t.Fatalf("lineup numbers = %v", numbers)
	}
	if filtered.Source != g.Source {
		t.Fatal("source not carried through filter")
	}
}
