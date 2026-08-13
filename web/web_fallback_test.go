package web

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func useTempGridCache(t *testing.T) {
	original := gridCacheRoot
	gridCacheRoot = t.TempDir()
	t.Cleanup(func() { gridCacheRoot = original })
}

func testPreferences() Preferences {
	return Preferences{Country: "USA", ZipCode: "11743", Headend: "NY67791", LineupId: "NY67791-X", Device: "X", Language: "en"}
}

func TestGetDataByTimeRetriesTransientFailure(t *testing.T) {
	useTempGridCache(t)
	originalDelays := gridRetryDelays
	gridRetryDelays = []time.Duration{0, 0, 0}
	defer func() { gridRetryDelays = originalDelays }()

	attempts := 0
	client := &Client{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("temporary timeout")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("{\"channels\":[]}")),
				Header:     make(http.Header),
			}, nil
		})},
		pref: testPreferences(),
	}

	gridTime := time.Date(2026, 7, 25, 6, 0, 0, 0, time.UTC).Unix()
	if _, err := client.GetDataByTime(gridTime); err != nil {
		t.Fatalf("GetDataByTime: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if _, _, err := loadGridCache(gridTime, client.Source()); err != nil {
		t.Fatalf("cache missing: %v", err)
	}
}

func TestGetDataByTimeFallsBackToCachedRawGrid(t *testing.T) {
	useTempGridCache(t)
	originalDelays := gridRetryDelays
	gridRetryDelays = []time.Duration{0, 0, 0}
	defer func() { gridRetryDelays = originalDelays }()

	prefs := testPreferences()
	gridTime := time.Date(2026, 7, 25, 6, 0, 0, 0, time.UTC).Unix()
	cached := &GridResponse{Channels: []JSONChannel{{
		ChannelID: "10139",
		ChannelNo: "102",
		CallSign:  "CNBC",
		Events: []JSONEvent{{
			StartTime: "2026-07-25T07:00:00Z",
			EndTime:   "2026-07-25T08:00:00Z",
			Program:   JSONProgram{ID: "EP123", Title: "Morning Business"},
		}},
	}}}
	if err := saveGridCache(gridTime, prefs.Source(), cached); err != nil {
		t.Fatalf("saveGridCache: %v", err)
	}

	attempts := 0
	client := &Client{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("upstream unavailable")
		})},
		pref: prefs,
	}

	grid, err := client.GetDataByTime(gridTime)
	if err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if attempts != gridMaxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, gridMaxAttempts)
	}
	if len(grid.Channels) != 1 || grid.Channels[0].Events[0].Program.Title != "Morning Business" {
		t.Fatalf("unexpected cached grid: %#v", grid)
	}
}

func TestGridCacheIsScopedByGuideSource(t *testing.T) {
	useTempGridCache(t)
	gridTime := time.Date(2026, 7, 25, 6, 0, 0, 0, time.UTC).Unix()
	source := testPreferences().Source()
	other := source
	other.LineupID = "OTHER-LINEUP"

	if gridCachePath(source, gridTime) == gridCachePath(other, gridTime) {
		t.Fatal("different lineups share cache path")
	}
	if err := saveGridCache(gridTime, source, &GridResponse{}); err != nil {
		t.Fatalf("saveGridCache: %v", err)
	}
	if _, _, err := loadGridCache(gridTime, other); err == nil {
		t.Fatal("different lineup reused cached grid")
	}
}

func TestPruneGridCacheRemovesExpiredSlots(t *testing.T) {
	useTempGridCache(t)
	source := testPreferences().Source()
	oldTime := time.Now().UTC().Add(-72 * time.Hour).Unix()
	newTime := time.Now().UTC().Add(24 * time.Hour).Unix()

	if err := saveGridCache(oldTime, source, &GridResponse{}); err != nil {
		t.Fatalf("save old grid: %v", err)
	}
	if err := saveGridCache(newTime, source, &GridResponse{}); err != nil {
		t.Fatalf("save new grid: %v", err)
	}
	if err := pruneGridCache(source, time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatalf("pruneGridCache: %v", err)
	}
	if _, err := os.Stat(gridCachePath(source, oldTime)); !os.IsNotExist(err) {
		t.Fatalf("old grid not pruned: %v", err)
	}
	if _, err := os.Stat(gridCachePath(source, newTime)); err != nil {
		t.Fatalf("new grid incorrectly pruned: %v", err)
	}
}
