package web

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGridFixtureDecodesRawFields(t *testing.T) {
	data, err := os.ReadFile("testdata/grid_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var grid GridResponse
	if err := json.Unmarshal(data, &grid); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(grid.Channels) != 4 {
		t.Fatalf("channels = %d, want 4", len(grid.Channels))
	}

	first := grid.Channels[0]
	if first.ID != "531580" || first.ChannelID != "53158" {
		t.Fatalf("row id/station id = %q/%q", first.ID, first.ChannelID)
	}
	if first.AffiliateCallSign != "null" {
		t.Fatalf("affiliateCallSign should decode verbatim, got %q", first.AffiliateCallSign)
	}
	if len(first.StationFilters) != 2 || first.StationFilters[0] != "filter-sports" {
		t.Fatalf("stationFilters = %v", first.StationFilters)
	}

	// Same station at a second position decodes as its own row.
	last := grid.Channels[3]
	if last.ChannelID != first.ChannelID || last.ChannelNo == first.ChannelNo || last.ID == first.ID {
		t.Fatalf("duplicate position not preserved: %+v", last)
	}

	ev := first.Events[0].Program
	if ev.TmsID == "" {
		t.Fatal("tmsId missing")
	}
	if ev.ReleaseYear != "2019" {
		t.Fatalf("numeric releaseYear = %q, want 2019", ev.ReleaseYear)
	}
	if first.Events[1].Program.ReleaseYear != "2021" {
		t.Fatalf("string releaseYear = %q, want 2021", first.Events[1].Program.ReleaseYear)
	}
	if first.Events[2].Program.ReleaseYear != "" {
		t.Fatalf("null releaseYear = %q, want empty", first.Events[2].Program.ReleaseYear)
	}
	second := grid.Channels[1]
	if !bool(second.Events[0].Program.IsGeneric) {
		t.Fatal(`isGeneric "1" should decode true`)
	}
	if bool(second.Events[1].Program.IsGeneric) {
		t.Fatal("isGeneric 0 should decode false")
	}
}

func TestFlexString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"2019"`, "2019"},
		{`2019`, "2019"},
		{`2019.0`, "2019.0"},
		{`null`, ""},
		{`true`, "true"},
		{`""`, ""},
		{`[1]`, ""},
		{`{"a":1}`, ""},
	}
	for _, c := range cases {
		var doc struct {
			V FlexString `json:"v"`
		}
		if err := json.Unmarshal([]byte(`{"v":`+c.in+`}`), &doc); err != nil {
			t.Fatalf("%s: unexpected error %v", c.in, err)
		}
		if string(doc.V) != c.want {
			t.Errorf("%s: got %q want %q", c.in, doc.V, c.want)
		}
	}
	var doc struct {
		V FlexString `json:"v"`
	}
	if err := json.Unmarshal([]byte(`{"v":"unterminated}`), &doc); err == nil {
		t.Fatal("malformed JSON should still error")
	}
}

func TestFlexBool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`true`, true},
		{`false`, false},
		{`1`, true},
		{`0`, false},
		{`2`, true},
		{`"1"`, true},
		{`"0"`, false},
		{`"true"`, true},
		{`"TRUE"`, true},
		{`"false"`, false},
		{`"yes"`, true},
		{`"maybe"`, false},
		{`null`, false},
		{`[true]`, false},
	}
	for _, c := range cases {
		var doc struct {
			V FlexBool `json:"v"`
		}
		if err := json.Unmarshal([]byte(`{"v":`+c.in+`}`), &doc); err != nil {
			t.Fatalf("%s: unexpected error %v", c.in, err)
		}
		if bool(doc.V) != c.want {
			t.Errorf("%s: got %v want %v", c.in, doc.V, c.want)
		}
	}
}
