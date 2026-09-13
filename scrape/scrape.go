// Package scrape fetches a Gracenote lineup's listings and converts them into
// a guide.TVGuide. It performs no enrichment, file I/O, or environment reads,
// so it can be imported by other programs.
package scrape

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/guide"
	"github.com/daniel-widrick/GraceNoteScraper/web"
)

const (
	// SlotDuration is the window Gracenote serves per grid request.
	SlotDuration = 6 * time.Hour
	// DefaultDays is how far ahead a scrape reaches when Options.Days is zero.
	DefaultDays = 14
	// DefaultSlotDelay is the pause between grid requests when
	// Options.SlotDelay is zero. Gracenote is an unofficial API; be polite.
	DefaultSlotDelay = 5 * time.Second
)

// ErrNoData is returned when every grid slot failed, so there is nothing to
// build a guide from. A partial failure is not an error; failed slots are
// skipped and reported through Options.Progress.
var ErrNoData = errors.New("scrape: no grid slot returned data")

// GridFetcher retrieves one six-hour grid. *web.Client satisfies it.
type GridFetcher interface {
	GetDataByTimeContext(ctx context.Context, t int64) (*web.GridResponse, error)
}

// Phase says whether a Progress report precedes or follows a slot fetch.
type Phase int

const (
	// PhaseFetching is reported just before a slot is requested.
	PhaseFetching Phase = iota
	// PhaseFetched is reported after a slot succeeded or failed.
	PhaseFetched
)

// Progress describes one step of a scrape.
type Progress struct {
	Phase      Phase
	Slot       int       // 1-based index of the slot being reported
	TotalSlots int       // total slots for this scrape
	SlotTime   time.Time // start of the six-hour window
	Completed  int       // slots finished so far, successful or not
	Channels   int       // distinct stations collected so far
	Programs   int       // programs collected so far
	Err        error     // non-nil on PhaseFetched when the slot failed and was skipped
}

// Options tunes a Fetch. The zero value is the production configuration.
type Options struct {
	Days      int              // days of listings to fetch; default DefaultDays
	SlotDelay time.Duration    // pause between slots; default DefaultSlotDelay (tests use a negative value for none)
	Fetcher   GridFetcher      // grid source; default web.NewClient(prefs)
	Now       func() time.Time // clock; default time.Now
	Progress  func(Progress)   // optional per-slot callback
	Logger    *log.Logger      // default log.Default()
}

// NoDelay is a SlotDelay value that disables the inter-slot pause.
const NoDelay = -1

func (o Options) withDefaults(prefs web.Preferences) Options {
	if o.Days <= 0 {
		o.Days = DefaultDays
	}
	if o.SlotDelay == 0 {
		o.SlotDelay = DefaultSlotDelay
	}
	if o.Fetcher == nil {
		o.Fetcher = web.NewClient(prefs)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	return o
}

// Slots returns the six-hour window start times a Fetch will request for the
// given clock and day count, beginning at UTC midnight of the current day.
func Slots(now time.Time, days int) []time.Time {
	if days <= 0 {
		days = DefaultDays
	}
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Duration(days) * 24 * time.Hour)
	slots := make([]time.Time, 0, days*int(24*time.Hour/SlotDuration))
	for t := start; t.Before(end); t = t.Add(SlotDuration) {
		slots = append(slots, t)
	}
	return slots
}

// Fetch downloads every grid slot for prefs and returns the assembled guide.
// Channels are deduplicated by station in first-seen order, Lineup keeps every
// position, and Programs are deduplicated by station, start, and end.
func Fetch(ctx context.Context, prefs web.Preferences, opts Options) (*guide.TVGuide, error) {
	opts = opts.withDefaults(prefs)
	slots := Slots(opts.Now(), opts.Days)

	channelIndex := make(map[string]int)
	lineupIndex := make(map[string]struct{})
	eventIndex := make(map[string]struct{})
	var channels []guide.Channel
	var lineup []guide.LineupPosition
	var programs []guide.Program
	succeeded := 0

	report := func(p Progress) {
		if opts.Progress != nil {
			opts.Progress(p)
		}
	}

	for i, slotTime := range slots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slot := i + 1
		report(Progress{Phase: PhaseFetching, Slot: slot, TotalSlots: len(slots), SlotTime: slotTime, Completed: i, Channels: len(channels), Programs: len(programs)})
		opts.Logger.Printf("Fetching grid %d/%d for time=%d (%s)", slot, len(slots), slotTime.Unix(), slotTime.Format(time.RFC3339))

		grid, err := opts.Fetcher.GetDataByTimeContext(ctx, slotTime.Unix())
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			opts.Logger.Printf("Error fetching grid at %d: %v", slotTime.Unix(), err)
			report(Progress{Phase: PhaseFetched, Slot: slot, TotalSlots: len(slots), SlotTime: slotTime, Completed: slot, Channels: len(channels), Programs: len(programs), Err: err})
		} else {
			succeeded++
			for _, ch := range grid.Channels {
				if _, seen := channelIndex[ch.ChannelID]; !seen {
					channelIndex[ch.ChannelID] = len(channels)
					channels = append(channels, guide.ConvertChannel(ch))
				}
				position := guide.ConvertLineupPosition(ch)
				if _, seen := lineupIndex[position.Key()]; !seen {
					lineupIndex[position.Key()] = struct{}{}
					lineup = append(lineup, position)
				}
				for _, ev := range ch.Events {
					key := ch.ChannelID + "|" + ev.StartTime + "|" + ev.EndTime
					if _, seen := eventIndex[key]; seen {
						continue
					}
					eventIndex[key] = struct{}{}
					programs = append(programs, guide.ConvertEvent(ev, ch.ChannelID, prefs.Language, prefs.Country))
				}
			}
			opts.Logger.Printf("Channels so far: %d, Events so far: %d", len(channels), len(programs))
			report(Progress{Phase: PhaseFetched, Slot: slot, TotalSlots: len(slots), SlotTime: slotTime, Completed: slot, Channels: len(channels), Programs: len(programs)})
		}

		if slot < len(slots) && opts.SlotDelay > 0 {
			select {
			case <-time.After(opts.SlotDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	if succeeded == 0 {
		return nil, fmt.Errorf("%w (%d slots attempted)", ErrNoData, len(slots))
	}

	guide.SortLineup(lineup)
	return &guide.TVGuide{
		Channels: channels,
		Programs: programs,
		Lineup:   lineup,
		Source:   guide.SourceFromPreferences(prefs, opts.Now().UTC()),
	}, nil
}
