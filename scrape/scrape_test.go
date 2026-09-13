package scrape

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/web"
)

var fixedNow = time.Date(2026, 9, 13, 15, 42, 0, 0, time.UTC)

func testPrefs() web.Preferences {
	return web.Preferences{Country: "USA", ZipCode: "13490", Headend: "lineupId", LineupId: "USA-lineupId-DEFAULT", Device: "-", Language: "en-us"}
}

// fakeFetcher serves canned grids keyed by slot time and records every call.
type fakeFetcher struct {
	mu     sync.Mutex
	grids  map[int64]*web.GridResponse
	errs   map[int64]error
	always *web.GridResponse
	calls  []int64
	block  chan struct{} // when set, the first call blocks until closed
}

func (f *fakeFetcher) GetDataByTimeContext(ctx context.Context, t int64) (*web.GridResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, t)
	block := f.block
	f.block = nil
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err, ok := f.errs[t]; ok {
		return nil, err
	}
	if g, ok := f.grids[t]; ok {
		return g, nil
	}
	if f.always != nil {
		return f.always, nil
	}
	return &web.GridResponse{}, nil
}

func channel(station, number string, events ...web.JSONEvent) web.JSONChannel {
	return web.JSONChannel{ChannelID: station, ID: station + "0", ChannelNo: number, CallSign: "C" + station, AffiliateName: "Net " + station, Events: events}
}

func event(start, end, title string) web.JSONEvent {
	return web.JSONEvent{StartTime: start, EndTime: end, Duration: "60", SeriesID: "SH1", Program: web.JSONProgram{ID: "EP1", Title: title}}
}

func quietOptions(f *fakeFetcher) Options {
	return Options{Days: 1, SlotDelay: NoDelay, Fetcher: f, Now: func() time.Time { return fixedNow }, Logger: log.New(io.Discard, "", 0)}
}

func TestSlotsAreMidnightAlignedSixHourWindows(t *testing.T) {
	slots := Slots(fixedNow, 2)
	if len(slots) != 8 {
		t.Fatalf("slots = %d, want 8", len(slots))
	}
	if !slots[0].Equal(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("first slot = %s", slots[0])
	}
	for i := 1; i < len(slots); i++ {
		if slots[i].Sub(slots[i-1]) != SlotDuration {
			t.Errorf("slot %d not 6h after previous", i)
		}
	}
	if len(Slots(fixedNow, 0)) != DefaultDays*4 {
		t.Errorf("zero days should default to %d days", DefaultDays)
	}
}

func TestFetchRequestsEverySlotAndAssemblesGuide(t *testing.T) {
	f := &fakeFetcher{always: &web.GridResponse{Channels: []web.JSONChannel{
		channel("s1", "2.1", event("2026-09-13T00:00:00Z", "2026-09-13T01:00:00Z", "a")),
		channel("s2", "5", event("2026-09-13T00:00:00Z", "2026-09-13T02:00:00Z", "b")),
		channel("s1", "1002"), // same station at a second number
	}}}
	opts := quietOptions(f)
	opts.Days = 2

	g, err := Fetch(context.Background(), testPrefs(), opts)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(f.calls) != 8 {
		t.Fatalf("calls = %d, want 8", len(f.calls))
	}
	if f.calls[0] != time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("first request time = %d", f.calls[0])
	}
	if len(g.Channels) != 2 {
		t.Errorf("channels = %d, want 2 (deduplicated by station)", len(g.Channels))
	}
	if g.Channels[0].ID != "s1" || g.Channels[1].ID != "s2" {
		t.Errorf("channels should keep first-seen order: %v %v", g.Channels[0].ID, g.Channels[1].ID)
	}
	if len(g.Lineup) != 3 {
		t.Fatalf("lineup = %d, want 3 (never collapsed)", len(g.Lineup))
	}
	if g.Lineup[0].ChannelNo != "2.1" || g.Lineup[1].ChannelNo != "5" || g.Lineup[2].ChannelNo != "1002" {
		t.Errorf("lineup order = %s %s %s", g.Lineup[0].ChannelNo, g.Lineup[1].ChannelNo, g.Lineup[2].ChannelNo)
	}
	if len(g.Programs) != 2 {
		t.Errorf("programs = %d, want 2 (same events across 8 slots deduplicated)", len(g.Programs))
	}
	if g.Source.LineupID != "USA-lineupId-DEFAULT" || g.Source.PostalCode != "13490" || !g.Source.GeneratedAt.Equal(fixedNow) {
		t.Errorf("source = %+v", g.Source)
	}
}

func TestFetchKeepsDistinctEventsAcrossSlots(t *testing.T) {
	slots := Slots(fixedNow, 1)
	f := &fakeFetcher{grids: map[int64]*web.GridResponse{
		slots[0].Unix(): {Channels: []web.JSONChannel{channel("s1", "2", event("2026-09-13T00:00:00Z", "2026-09-13T06:00:00Z", "morning"))}},
		slots[1].Unix(): {Channels: []web.JSONChannel{channel("s1", "2", event("2026-09-13T06:00:00Z", "2026-09-13T12:00:00Z", "midday"))}},
	}}
	g, err := Fetch(context.Background(), testPrefs(), quietOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Programs) != 2 {
		t.Fatalf("programs = %d, want 2", len(g.Programs))
	}
}

func TestFetchSkipsFailedSlotAndReportsIt(t *testing.T) {
	slots := Slots(fixedNow, 1)
	boom := errors.New("upstream timeout")
	f := &fakeFetcher{
		always: &web.GridResponse{Channels: []web.JSONChannel{channel("s1", "2")}},
		errs:   map[int64]error{slots[1].Unix(): boom},
	}
	var reports []Progress
	opts := quietOptions(f)
	opts.Progress = func(p Progress) { reports = append(reports, p) }

	g, err := Fetch(context.Background(), testPrefs(), opts)
	if err != nil {
		t.Fatalf("partial failure must not be an error: %v", err)
	}
	if len(g.Channels) != 1 {
		t.Fatalf("channels = %d", len(g.Channels))
	}
	if len(reports) != 8 {
		t.Fatalf("progress reports = %d, want 2 per slot", len(reports))
	}
	failed := reports[3] // slot 2, PhaseFetched
	if failed.Phase != PhaseFetched || failed.Slot != 2 || !errors.Is(failed.Err, boom) {
		t.Errorf("failed slot report = %+v", failed)
	}
	for i, r := range reports {
		if r.TotalSlots != 4 {
			t.Errorf("report %d TotalSlots = %d", i, r.TotalSlots)
		}
		if i > 0 && r.Completed < reports[i-1].Completed {
			t.Errorf("Completed went backwards at report %d", i)
		}
	}
	last := reports[len(reports)-1]
	if last.Completed != 4 || last.Slot != 4 || last.Phase != PhaseFetched {
		t.Errorf("last report = %+v", last)
	}
}

func TestFetchReturnsErrNoDataWhenEverySlotFails(t *testing.T) {
	f := &fakeFetcher{errs: map[int64]error{}}
	for _, s := range Slots(fixedNow, 1) {
		f.errs[s.Unix()] = errors.New("down")
	}
	_, err := Fetch(context.Background(), testPrefs(), quietOptions(f))
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("err = %v, want ErrNoData", err)
	}
}

func TestFetchStopsPromptlyWhenContextCancelledDuringDelay(t *testing.T) {
	f := &fakeFetcher{always: &web.GridResponse{Channels: []web.JSONChannel{channel("s1", "2")}}}
	opts := quietOptions(f)
	opts.SlotDelay = time.Minute
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := Fetch(ctx, testPrefs(), opts)
		done <- err
	}()
	// Let the first slot complete and the delay begin, then cancel.
	deadline := time.After(2 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.calls)
		f.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first slot never requested")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fetch did not return after cancellation")
	}
	if len(f.calls) != 1 {
		t.Errorf("calls after cancel = %d, want 1", len(f.calls))
	}
}

func TestFetchStopsWhenContextCancelledDuringRequest(t *testing.T) {
	f := &fakeFetcher{always: &web.GridResponse{}, block: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Fetch(ctx, testPrefs(), quietOptions(f))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Fetch did not return")
	}
}

func TestFetchHonoursAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeFetcher{}
	if _, err := Fetch(ctx, testPrefs(), quietOptions(f)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("no requests should be made, got %d", len(f.calls))
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults(testPrefs())
	if o.Days != DefaultDays || o.SlotDelay != DefaultSlotDelay || o.Fetcher == nil || o.Now == nil || o.Logger == nil {
		t.Fatalf("defaults not applied: %+v", o)
	}
	if _, ok := o.Fetcher.(*web.Client); !ok {
		t.Fatalf("default fetcher should be *web.Client, got %T", o.Fetcher)
	}
}
