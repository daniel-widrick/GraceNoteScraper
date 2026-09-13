package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/appconfig"
	"github.com/daniel-widrick/GraceNoteScraper/guide"
)

func lineupTestConfig() appconfig.Config {
	return appconfig.Config{
		Version: appconfig.CurrentVersion,
		Gracenote: appconfig.GracenoteConfig{
			Country: "USA", PostalCode: "13490", Language: "en-us", ProviderType: "OTA", Device: "-",
			LineupID: "USA-lineupId-DEFAULT", ProviderName: "Local Over the Air Broadcast", Location: "Utica", HeadendID: "lineupId",
		},
	}
}

func lineupTestGuide() *guide.TVGuide {
	return &guide.TVGuide{
		Lineup: []guide.LineupPosition{
			{ChannelNo: "10", StationID: "s10", PlacementID: "s100", CallSign: "TEN", Affiliate: "Ten Net", Filters: []string{"news"}, LogoURL: "http://logo/ten.png"},
			{ChannelNo: "2.1", StationID: "s2", PlacementID: "s20", CallSign: "TWO", Affiliate: "Two Net", AffiliateCallSign: "TW"},
			{ChannelNo: "1002", StationID: "s2", PlacementID: "s299", CallSign: "TWO", Affiliate: "Two Net", AffiliateCallSign: "TW"},
		},
		Source: guide.Source{
			Country: "USA", PostalCode: "13490", HeadendID: "lineupId", LineupID: "USA-lineupId-DEFAULT", Device: "-", Language: "en-us",
			GeneratedAt: time.Date(2026, 9, 13, 4, 10, 22, 0, time.UTC),
		},
	}
}

func newLineupServer(t *testing.T, g *guide.TVGuide, save bool) http.Handler {
	t.Helper()
	store, err := appconfig.LoadStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if save {
		if err := store.Save(lineupTestConfig()); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	state := &GuideState{}
	state.Update(g)
	return handleLineupJSON(state, store)
}

func TestLineupJSONUnavailableBeforeFirstGuide(t *testing.T) {
	rec := httptest.NewRecorder()
	newLineupServer(t, nil, true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lineup.json", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestLineupJSONResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	newLineupServer(t, lineupTestGuide(), true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lineup.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS header missing")
	}

	var got APILineup
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Generated != "2026-09-13T04:10:22Z" {
		t.Errorf("generated = %q", got.Generated)
	}
	wantSource := APILineupSource{
		ProviderName: "Local Over the Air Broadcast", ProviderType: "OTA", Location: "Utica",
		LineupID: "USA-lineupId-DEFAULT", HeadendID: "lineupId", PostalCode: "13490", Country: "USA", Device: "-", Language: "en-us",
	}
	if got.Source != wantSource {
		t.Errorf("source = %+v\nwant %+v", got.Source, wantSource)
	}

	var numbers []string
	for _, p := range got.Positions {
		numbers = append(numbers, p.Number)
	}
	if !reflect.DeepEqual(numbers, []string{"2.1", "10", "1002"}) {
		t.Errorf("positions not sorted by number: %v", numbers)
	}
	ten := got.Positions[1]
	want := APILineupPosition{Number: "10", StationID: "s10", PlacementID: "s100", CallSign: "TEN", Affiliate: "Ten Net", Filters: []string{"news"}, LogoURL: "http://logo/ten.png"}
	if !reflect.DeepEqual(ten, want) {
		t.Errorf("position = %+v\nwant %+v", ten, want)
	}
	if got.Positions[0].StationID != "s2" || got.Positions[2].StationID != "s2" {
		t.Error("the same station at two numbers must appear twice")
	}
}

func TestLineupJSONKeySetIsStable(t *testing.T) {
	rec := httptest.NewRecorder()
	newLineupServer(t, lineupTestGuide(), true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lineup.json", nil))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if keys := sortedKeys(raw); !reflect.DeepEqual(keys, []string{"generated", "positions", "source"}) {
		t.Errorf("top-level keys = %v", keys)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw["source"], &source); err != nil {
		t.Fatal(err)
	}
	if keys := sortedKeys(source); !reflect.DeepEqual(keys, []string{"country", "device", "headendId", "language", "lineupId", "location", "postalCode", "providerName", "providerType"}) {
		t.Errorf("source keys = %v", keys)
	}
	var positions []map[string]json.RawMessage
	if err := json.Unmarshal(raw["positions"], &positions); err != nil {
		t.Fatal(err)
	}
	// Position with filters carries the filters key; one without omits it.
	withFilters := sortedKeys(positions[1])
	if !reflect.DeepEqual(withFilters, []string{"affiliate", "affiliateCallSign", "callSign", "filters", "logoUrl", "number", "placementId", "stationId"}) {
		t.Errorf("position keys = %v", withFilters)
	}
	if _, ok := positions[0]["filters"]; ok {
		t.Error("filters should be omitted when empty")
	}
	if _, ok := positions[0]["logoUrl"]; !ok {
		t.Error("logoUrl must always be present, even when empty")
	}
}

func TestLineupJSONSourceNamingRequiresMatchingConfig(t *testing.T) {
	// No saved configuration at all.
	rec := httptest.NewRecorder()
	newLineupServer(t, lineupTestGuide(), false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lineup.json", nil))
	var got APILineup
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Source.ProviderName != "" || got.Source.ProviderType != "" || got.Source.Location != "" {
		t.Errorf("provider naming should be blank without config: %+v", got.Source)
	}
	if got.Source.LineupID != "USA-lineupId-DEFAULT" {
		t.Errorf("guide source fields must still be present: %+v", got.Source)
	}

	// Saved configuration describes a different lineup than the guide.
	g := lineupTestGuide()
	g.Source.LineupID = "USA-OTHER-DEFAULT"
	rec = httptest.NewRecorder()
	newLineupServer(t, g, true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lineup.json", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Source.ProviderName != "" {
		t.Errorf("provider name leaked across lineups: %+v", got.Source)
	}
	if got.Source.LineupID != "USA-OTHER-DEFAULT" {
		t.Errorf("lineup id should come from the guide: %+v", got.Source)
	}
}

func TestLineupJSONDoesNotMutateGuideOrder(t *testing.T) {
	g := lineupTestGuide()
	before := append([]guide.LineupPosition(nil), g.Lineup...)
	lineupToJSON(g, lineupTestConfig(), true)
	if !reflect.DeepEqual(before, g.Lineup) {
		t.Fatal("lineupToJSON must sort a copy, not the live guide")
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
