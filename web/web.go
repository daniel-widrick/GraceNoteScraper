package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/util"
)

const (
	userAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_9_3) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/35.0.1916.47 Safari/537.36"
	gridMaxAttempts = 4
)

var gridRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

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

type GuideSource struct {
	Country  string `json:"country"`
	ZipCode  string `json:"zip_code"`
	Headend  string `json:"headend"`
	LineupID string `json:"lineup_id"`
	Device   string `json:"device"`
	Language string `json:"language"`
}

func currentPreferences() Preferences {
	return Preferences{
		Country:  util.GetEnv("GN_COUNTRY", "USA"),
		ZipCode:  util.GetEnv("GN_ZIPCODE", "13490"),
		Headend:  util.GetEnv("GN_HEADEND", "lineupId"),
		LineupId: util.GetEnv("GN_LINEUP", "USA-lineupId-DEFAULT"),
		Device:   util.GetEnv("GN_DEVICE", "-"),
		Language: util.GetEnv("GN_LANGUAGE", "en-us"),
	}
}

func (p Preferences) Source() GuideSource {
	return GuideSource{Country: p.Country, ZipCode: p.ZipCode, Headend: p.Headend, LineupID: p.LineupId, Device: p.Device, Language: p.Language}
}

type Client struct {
	*http.Client
	pref Preferences
}

func (c *Client) Source() GuideSource {
	return c.pref.Source()
}

func (c *Client) GetDataByTime(t int64) (*GridResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= gridMaxAttempts; attempt++ {
		grid, err := c.getDataByTimeOnce(t)
		if err == nil {
			if attempt > 1 {
				log.Printf("Gracenote grid time=%d succeeded on attempt %d/%d", t, attempt, gridMaxAttempts)
			}
			if err := saveGridCache(t, c.Source(), grid); err != nil {
				log.Printf("Gracenote grid cache: failed to save time=%d: %v", t, err)
			}
			return grid, nil
		}
		lastErr = err
		if attempt == gridMaxAttempts {
			break
		}
		delay := gridRetryDelays[attempt-1]
		log.Printf("Gracenote grid time=%d attempt %d/%d failed: %v; retrying in %s", t, attempt, gridMaxAttempts, err, delay)
		time.Sleep(delay)
	}

	fallback, age, err := loadGridCache(t, c.Source())
	if err == nil {
		log.Printf("Gracenote grid time=%d exhausted %d attempts; using cached raw grid (%s old)", t, gridMaxAttempts, age.Round(time.Second))
		return fallback, nil
	}
	return nil, fmt.Errorf("Gracenote grid time=%d failed after %d attempts (%v); cached fallback unavailable: %w", t, gridMaxAttempts, lastErr, err)
}

func (c *Client) getDataByTimeOnce(t int64) (*GridResponse, error) {
	log.Printf("headendId=%s lineupId=%s zipCode=%s", c.pref.Headend, c.pref.LineupId, c.pref.ZipCode)
	params := url.Values{
		"aid": {"orbebb"}, "lineupId": {c.pref.LineupId}, "timespan": {"6"}, "headendId": {c.pref.Headend},
		"country": {c.pref.Country}, "device": {c.pref.Device}, "postalCode": {c.pref.ZipCode}, "isOverride": {"true"},
		"time": {fmt.Sprintf("%d", t)}, "timezone": {""}, "pref": {"16,256"}, "userId": {"-"}, "languagecode": {c.pref.Language},
	}
	gridURL := "https://tvlistings.gracenote.com/api/grid?" + params.Encode()
	log.Printf("Fetching: %s", gridURL)
	req, err := http.NewRequest("GET", gridURL, nil)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("guide API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
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

func NewClient() *Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatalf("Unable to create cookie storage for http client: %v", err)
		return nil
	}
	client := &Client{
		Client: &http.Client{Jar: jar, Timeout: 15 * time.Second, Transport: &headerTransport{rt: http.DefaultTransport}},
		pref: currentPreferences(),
	}
	if err := pruneGridCache(client.Source(), time.Now().UTC().Add(-48*time.Hour)); err != nil {
		log.Printf("Gracenote grid cache: prune failed: %v", err)
	}
	return client
}

type headerTransport struct{ rt http.RoundTripper }

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", userAgent)
	return t.rt.RoundTrip(req)
}
