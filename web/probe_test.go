package web

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProbeGridUsesOneHourWindowWithoutCacheOrRetry(t *testing.T) {
	useTempGridCache(t)

	attempts := 0
	var requested string
	client := &Client{
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			requested = req.URL.Query().Get("timespan")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"channels":[{"channelId":"1"},{"channelId":"2"}]}`)),
				Header:     make(http.Header),
			}, nil
		})},
		pref: testPreferences(),
	}

	gridTime := time.Date(2026, 7, 25, 6, 0, 0, 0, time.UTC).Unix()
	grid, err := client.ProbeGridContext(t.Context(), gridTime)
	if err != nil {
		t.Fatalf("ProbeGridContext: %v", err)
	}
	if len(grid.Channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(grid.Channels))
	}
	if requested != "1" {
		t.Fatalf("timespan = %q, want \"1\"", requested)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if _, _, err := loadGridCache(gridTime, client.Source()); err == nil {
		t.Fatal("probe wrote to the scraper grid cache")
	}
}

func TestProbeGridDoesNotRetry(t *testing.T) {
	useTempGridCache(t)

	attempts := 0
	client := &Client{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("upstream unavailable")
		})},
		pref: testPreferences(),
	}

	if _, err := client.ProbeGridContext(t.Context(), 0); err == nil {
		t.Fatal("expected probe error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestScraperGridStillUsesSixHourWindow(t *testing.T) {
	useTempGridCache(t)

	var requested string
	client := &Client{
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requested = req.URL.Query().Get("timespan")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"channels":[]}`)),
				Header:     make(http.Header),
			}, nil
		})},
		pref: testPreferences(),
	}
	if _, err := client.GetDataByTime(0); err != nil {
		t.Fatalf("GetDataByTime: %v", err)
	}
	if requested != "6" {
		t.Fatalf("timespan = %q, want \"6\"", requested)
	}
}
