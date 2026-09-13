package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniel-widrick/GraceNoteScraper/guide"
	"github.com/daniel-widrick/GraceNoteScraper/web"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/xmlguide_golden.xmltv from the current conversion path")

const (
	gridFixturePath = "web/testdata/grid_sample.json"
	xmltvGoldenPath = "testdata/xmlguide_golden.xmltv"
	fixtureLanguage = "en-us"
	fixtureCountry  = "USA"
)

// loadGridFixture decodes the shared grid sample used by the golden and
// conversion tests.
func loadGridFixture(t *testing.T) web.GridResponse {
	t.Helper()
	data, err := os.ReadFile(gridFixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var grid web.GridResponse
	if err := json.Unmarshal(data, &grid); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return grid
}

// guideFromGrid mirrors the scrape loop's conversion: channels deduplicated by
// station ID in first-seen order, events deduplicated by station, start, and end.
func guideFromGrid(grid web.GridResponse) *guide.TVGuide {
	seenChannel := make(map[string]bool)
	seenEvent := make(map[string]bool)
	g := &guide.TVGuide{}
	for _, ch := range grid.Channels {
		if !seenChannel[ch.ChannelID] {
			seenChannel[ch.ChannelID] = true
			g.Channels = append(g.Channels, guide.ConvertChannel(ch))
		}
		for _, ev := range ch.Events {
			key := ch.ChannelID + "|" + ev.StartTime + "|" + ev.EndTime
			if seenEvent[key] {
				continue
			}
			seenEvent[key] = true
			g.Programs = append(g.Programs, guide.ConvertEvent(ev, ch.ChannelID, fixtureLanguage, fixtureCountry))
		}
	}
	return g
}

// TestXMLTVGolden guards the promise that model changes never alter XMLTV
// output. Regenerate deliberately with: go test -run TestXMLTVGolden -update
func TestXMLTVGolden(t *testing.T) {
	g := guideFromGrid(loadGridFixture(t))

	var rendered bytes.Buffer
	if err := renderXMLTV(&rendered, g); err != nil {
		t.Fatalf("render: %v", err)
	}

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(xmltvGoldenPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(xmltvGoldenPath, rendered.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", xmltvGoldenPath, rendered.Len())
		return
	}

	want, err := os.ReadFile(xmltvGoldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if !bytes.Equal(want, rendered.Bytes()) {
		t.Fatalf("XMLTV output changed from golden.\nIf this is intended, run: go test -run TestXMLTVGolden -update\n--- got ---\n%s", rendered.String())
	}
}
