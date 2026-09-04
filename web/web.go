package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_9_3) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/35.0.1916.47 Safari/537.36"

// JSON response structs matching the Gracenote grid API
type GridResponse struct {
	Channels []JSONChannel `json:"channels"`
}

type JSONChannel struct {
	ChannelID     string      `json:"channelId"`
	ChannelNo     string      `json:"channelNo"`
	CallSign      string      `json:"callSign"`
	AffiliateName string      `json:"affiliateName"`
	Thumbnail     string      `json:"thumbnail"`
	Events        []JSONEvent `json:"events"`
}

type JSONEvent struct {
	StartTime string      `json:"startTime"`
	EndTime   string      `json:"endTime"`
	Duration  string      `json:"duration"`
	Thumbnail string      `json:"thumbnail"`
	SeriesID  string      `json:"seriesId"`
	Rating    *string     `json:"rating"`
	Flag      []string    `json:"flag"`
	Tags      []string    `json:"tags"`
	Filter    []string    `json:"filter"`
	Program   JSONProgram `json:"program"`
}

type JSONProgram struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	EpisodeTitle *string `json:"episodeTitle"`
	ShortDesc    *string `json:"shortDesc"`
	Season       *string `json:"season"`
	Episode      *string `json:"episode"`
}

type Preferences struct {
	Country  string
	ZipCode  string
	Headend  string
	LineupId string
	Device   string
	Language string
}

type Client struct {
	*http.Client
	pref Preferences
}

func (c *Client) GetDataByTime(t int64) (*GridResponse, error) {
	return c.GetDataByTimeContext(context.Background(), t)
}

func (c *Client) GetDataByTimeContext(ctx context.Context, t int64) (*GridResponse, error) {
	log.Printf("headendId=%s lineupId=%s zipCode=%s", c.pref.Headend, c.pref.LineupId, c.pref.ZipCode)

	params := url.Values{
		"aid":          {"orbebb"},
		"lineupId":     {c.pref.LineupId},
		"timespan":     {"6"},
		"headendId":    {c.pref.Headend},
		"country":      {c.pref.Country},
		"device":       {c.pref.Device},
		"postalCode":   {c.pref.ZipCode},
		"isOverride":   {"true"},
		"time":         {fmt.Sprintf("%d", t)},
		"timezone":     {""},
		"pref":         {"16,256"},
		"userId":       {"-"},
		"languagecode": {c.pref.Language},
	}
	gridURL := "https://tvlistings.gracenote.com/api/grid?" + params.Encode()
	log.Printf("Fetching: %s", gridURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gridURL, nil)
	if err != nil {
		return nil, fmt.Errorf("GetDataByTime failed to build request: %w", err)
	}
	req.Header.Set("Referer", "https://tvlistings.gracenote.com/grid-affiliates.html?aid=orbebb")
	req.Header.Set("X-Requested-Width", "XMLHttpRequest")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GetDataByTime request failed: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read guide body: %w", err)
	}

	var grid GridResponse
	if err := json.Unmarshal(b, &grid); err != nil {
		return nil, fmt.Errorf("unable to parse guide JSON: %w", err)
	}
	return &grid, nil
}

func NewClient(pref Preferences) *Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatalf("Unable to create cookie storage for http client: %v", err)
		return nil
	}
	return &Client{
		Client: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
			Transport: &headerTransport{
				rt: http.DefaultTransport,
			},
		},
		pref: pref,
	}
}

type headerTransport struct {
	rt http.RoundTripper
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", userAgent)
	return t.rt.RoundTrip(req)
}
