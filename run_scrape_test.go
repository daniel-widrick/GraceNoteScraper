package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"sync/atomic"
	"testing"

	"github.com/daniel-widrick/GraceNoteScraper/guide"
	"github.com/daniel-widrick/GraceNoteScraper/scrape"
	"github.com/daniel-widrick/GraceNoteScraper/web"
)

type countingFetcher struct {
	calls atomic.Int32
	grid  *web.GridResponse
	err   error
}

func (f *countingFetcher) GetDataByTimeContext(ctx context.Context, t int64) (*web.GridResponse, error) {
	f.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.grid, nil
}

// useFakeScrape swaps the production scrape options for a fake fetcher with
// one day of slots and no delay. The preferences use a country tvlogo does not
// support so no logo lookups reach the network.
func useFakeScrape(t *testing.T, f *countingFetcher) web.Preferences {
	t.Helper()
	t.Chdir(t.TempDir())
	original := newScrapeOptions
	newScrapeOptions = func(web.Preferences) scrape.Options {
		return scrape.Options{Fetcher: f, Days: 1, SlotDelay: scrape.NoDelay, Logger: log.New(io.Discard, "", 0)}
	}
	t.Cleanup(func() { newScrapeOptions = original })
	return web.Preferences{Country: "ZZZ", ZipCode: "00000", Headend: "h", LineupId: "L", Device: "-", Language: "en-us"}
}

func TestRunScrapeBuildsLineupAndPersists(t *testing.T) {
	f := &countingFetcher{grid: &web.GridResponse{Channels: []web.JSONChannel{
		{ChannelID: "s1", ID: "s10", ChannelNo: "2.1", CallSign: "AAA", AffiliateName: "A Net", StationFilters: []string{"filter-news"},
			Events: []web.JSONEvent{{StartTime: "2026-09-13T00:00:00Z", EndTime: "2026-09-13T01:00:00Z", Duration: "60", SeriesID: "SH1", Program: web.JSONProgram{ID: "EP1", Title: "one"}}}},
		{ChannelID: "s1", ID: "s199", ChannelNo: "1002", CallSign: "AAA", AffiliateName: "A Net"},
	}}}
	pref := useFakeScrape(t, f)

	var persisted *guide.TVGuide
	persister := func(g *guide.TVGuide) (bool, error) {
		persisted = g
		return true, persistGuideFiles(g, "fp")
	}
	var updates []scrapeProgressUpdate
	got, err := runScrape(pref, nil, "http://base:8080", nil, "fp", func() bool { return true }, persister, func(u scrapeProgressUpdate) { updates = append(updates, u) })
	if err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if f.calls.Load() != 4 {
		t.Errorf("grid fetches = %d, want 4 for one day", f.calls.Load())
	}
	if persisted != got {
		t.Error("persister should receive the returned guide")
	}
	if len(got.Channels) != 1 || len(got.Lineup) != 2 || len(got.Programs) != 1 {
		t.Fatalf("channels/lineup/programs = %d/%d/%d", len(got.Channels), len(got.Lineup), len(got.Programs))
	}
	if got.Lineup[0].LogoURL != "" {
		t.Errorf("no logo source available, got %q", got.Lineup[0].LogoURL)
	}
	if got.Source.LineupID != "L" {
		t.Errorf("source = %+v", got.Source)
	}
	if _, err := os.Stat("xmlguide.xmltv"); err != nil {
		t.Errorf("xmlguide.xmltv not written: %v", err)
	}
	stages := map[string]bool{}
	for _, u := range updates {
		stages[u.Stage] = true
	}
	for _, want := range []string{"gracenote", "logos", "saving"} {
		if !stages[want] {
			t.Errorf("missing progress stage %q (got %v)", want, stages)
		}
	}
	if stages["tmdb"] {
		t.Error("no TMDB client configured, so no tmdb stage should be reported")
	}
	var gracenote []scrapeProgressUpdate
	for _, u := range updates {
		if u.Stage == "gracenote" {
			gracenote = append(gracenote, u)
		}
	}
	if len(gracenote) != 8 || gracenote[0].Total != 4 || gracenote[7].Completed != 4 {
		t.Errorf("gracenote progress = %d updates, first %+v, last %+v", len(gracenote), gracenote[0], gracenote[len(gracenote)-1])
	}
}

func TestRunScrapeReportsSourceChange(t *testing.T) {
	f := &countingFetcher{grid: &web.GridResponse{Channels: []web.JSONChannel{{ChannelID: "s1", ChannelNo: "2"}}}}
	pref := useFakeScrape(t, f)

	var seen atomic.Int32
	sourceCurrent := func() bool {
		// Current for the initial check, changed afterwards.
		return seen.Add(1) == 1
	}
	persisterCalled := false
	_, err := runScrape(pref, nil, "", nil, "fp", sourceCurrent, func(*guide.TVGuide) (bool, error) {
		persisterCalled = true
		return true, nil
	})
	if !errors.Is(err, errScrapeSourceChanged) {
		t.Fatalf("err = %v, want errScrapeSourceChanged", err)
	}
	if persisterCalled {
		t.Error("persister must not run after a source change")
	}
}

func TestRunScrapeSurfacesTotalFailure(t *testing.T) {
	f := &countingFetcher{err: errors.New("gracenote down")}
	pref := useFakeScrape(t, f)
	_, err := runScrape(pref, nil, "", nil, "fp", nil, func(*guide.TVGuide) (bool, error) {
		t.Fatal("persister must not run with no data")
		return false, nil
	})
	if !errors.Is(err, scrape.ErrNoData) {
		t.Fatalf("err = %v, want ErrNoData", err)
	}
}
