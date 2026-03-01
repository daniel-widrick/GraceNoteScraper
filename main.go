package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"regexp"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/guide"
	"github.com/daniel-widrick/GraceNoteScraper/tmdb"
	"github.com/daniel-widrick/GraceNoteScraper/tvlogo"
	"github.com/daniel-widrick/GraceNoteScraper/util"
	"github.com/daniel-widrick/GraceNoteScraper/web"
	"github.com/joho/godotenv"
)

//go:embed guide.tmpl
var guideTmplFS embed.FS

//go:embed index.html
var indexHTML []byte

// ---------- GuideState ----------

// GuideState holds the current guide data, safe for concurrent access.
type GuideState struct {
	mu    sync.RWMutex
	guide *guide.TVGuide
}

func (s *GuideState) Update(g *guide.TVGuide) {
	s.mu.Lock()
	s.guide = g
	s.mu.Unlock()
}

func (s *GuideState) Get() *guide.TVGuide {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.guide
}

// ---------- JSON API types ----------

type APIGuide struct {
	Generated string       `json:"generated"`
	Channels  []APIChannel `json:"channels"`
}

type APIChannel struct {
	ID      string       `json:"id"`
	Number  string       `json:"number"`
	Name    string       `json:"name"`
	LogoURL string       `json:"logoUrl"`
	Programs []APIProgram `json:"programs"`
}

type APIProgram struct {
	Title       string `json:"title"`
	SubTitle    string `json:"subTitle,omitempty"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Category    string `json:"category,omitempty"`
	IsNew       bool   `json:"isNew,omitempty"`
	Rating      string `json:"rating,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
	Description string `json:"description,omitempty"`
}

// ---------- Conversion ----------

// guideToJSON converts a TVGuide into the simplified JSON API format.
func guideToJSON(g *guide.TVGuide) APIGuide {
	// Build channel-id -> programs map
	chanProgs := make(map[string][]APIProgram)
	for _, p := range g.Programs {
		cat := ""
		if len(p.Categories) > 0 {
			cat = p.Categories[0].Name
		}
		desc := html.UnescapeString(p.Description)
		if desc == "Unavailable" {
			desc = ""
		}
		ap := APIProgram{
			Title:       html.UnescapeString(p.Title),
			SubTitle:    html.UnescapeString(p.SubTitle),
			Start:       xmltvTimeToISO(p.Start),
			End:         xmltvTimeToISO(p.Stop),
			Category:    cat,
			IsNew:       p.New,
			Rating:      p.Rating,
			IconURL:     p.IconSrc,
			Description: desc,
		}
		chanProgs[p.Channel] = append(chanProgs[p.Channel], ap)
	}

	// Sort programs by start time within each channel
	for id := range chanProgs {
		progs := chanProgs[id]
		sort.Slice(progs, func(i, j int) bool {
			return progs[i].Start < progs[j].Start
		})
	}

	var channels []APIChannel
	for _, ch := range g.Channels {
		number := ""
		name := ""
		if len(ch.DisplayNames) >= 3 {
			number = ch.DisplayNames[1].Name // just the number
			name = ch.DisplayNames[2].Name   // just the callsign
		} else if len(ch.DisplayNames) >= 1 {
			name = ch.DisplayNames[0].Name
		}
		channels = append(channels, APIChannel{
			ID:       ch.ID,
			Number:   html.UnescapeString(number),
			Name:     html.UnescapeString(name),
			LogoURL:  ch.IconURL,
			Programs: chanProgs[ch.ID],
		})
	}

	// Sort channels by number (numeric sort)
	sort.Slice(channels, func(i, j int) bool {
		return channelNumberLess(channels[i].Number, channels[j].Number)
	})

	return APIGuide{
		Generated: time.Now().UTC().Format(time.RFC3339),
		Channels:  channels,
	}
}

// channelNumberLess compares channel numbers numerically where possible.
func channelNumberLess(a, b string) bool {
	// Try to parse as float for numeric comparison (handles "5.1", "12", etc.)
	var ai, bi float64
	_, errA := fmt.Sscanf(a, "%f", &ai)
	_, errB := fmt.Sscanf(b, "%f", &bi)
	if errA == nil && errB == nil {
		return ai < bi
	}
	return a < b
}

// xmltvTimeToISO converts "20250225200000 +0000" → "2025-02-25T20:00:00Z"
func xmltvTimeToISO(xmltvTime string) string {
	xmltvTime = strings.TrimSpace(xmltvTime)
	// Strip the timezone suffix — we assume +0000 (UTC)
	if idx := strings.Index(xmltvTime, " "); idx >= 0 {
		xmltvTime = xmltvTime[:idx]
	}
	if len(xmltvTime) < 14 {
		return xmltvTime
	}
	return xmltvTime[0:4] + "-" + xmltvTime[4:6] + "-" + xmltvTime[6:8] +
		"T" + xmltvTime[8:10] + ":" + xmltvTime[10:12] + ":" + xmltvTime[12:14] + "Z"
}

// ---------- Scraping ----------

// runScrape performs the full scrape cycle and returns the populated TVGuide.
// It also writes xmlguide.xmltv atomically.
func runScrape(tmdbClient *tmdb.Client, logoClient *tvlogo.Client, lang, country, baseURL string, channelFilter map[string]bool) (*guide.TVGuide, error) {
	client := web.NewClient()

	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	endTime := midnight.Add(14 * 24 * time.Hour)

	channelMap := make(map[string]guide.Channel)
	eventMap := make(map[string]bool)
	var programs []guide.Program

	for t := midnight; t.Before(endTime); t = t.Add(6 * time.Hour) {
		ts := t.Unix()
		log.Printf("Fetching grid for time=%d (%s)", ts, t.Format(time.RFC3339))

		grid, err := client.GetDataByTime(ts)
		if err != nil {
			log.Printf("Error fetching grid at %d: %v", ts, err)
			continue
		}

		for _, ch := range grid.Channels {
			if _, exists := channelMap[ch.ChannelID]; !exists {
				channelMap[ch.ChannelID] = guide.ConvertChannel(ch)
			}

			for _, ev := range ch.Events {
				dedupKey := ch.ChannelID + "|" + ev.StartTime + "|" + ev.EndTime
				if eventMap[dedupKey] {
					continue
				}
				eventMap[dedupKey] = true
				programs = append(programs, guide.ConvertEvent(ev, ch.ChannelID, lang, country))
			}
		}

		log.Printf("Channels so far: %d, Events so far: %d", len(channelMap), len(programs))

		if t.Add(6 * time.Hour).Before(endTime) {
			time.Sleep(5 * time.Second)
		}
	}

	var channels []guide.Channel
	for _, ch := range channelMap {
		channels = append(channels, ch)
	}

	enrichChannelIcons(logoClient, channels)
	enrichProgramThumbnails(tmdbClient, programs)

	// Rewrite image URLs to go through the local proxy
	if baseURL != "" {
		proxy := strings.TrimRight(baseURL, "/") + "/img?url="
		for i := range channels {
			if channels[i].IconURL != "" {
				channels[i].IconURL = proxy + neturl.QueryEscape(channels[i].IconURL)
			}
		}
		for i := range programs {
			if programs[i].IconSrc != "" {
				programs[i].IconSrc = proxy + neturl.QueryEscape(programs[i].IconSrc)
			}
			for j := range programs[i].Images {
				if programs[i].Images[j].URL != "" {
					programs[i].Images[j].URL = proxy + neturl.QueryEscape(programs[i].Images[j].URL)
				}
			}
		}
		log.Printf("Rewrote image URLs with base %s", baseURL)
	}

	tvGuide := &guide.TVGuide{
		Channels: channels,
		Programs: programs,
	}

	if channelFilter != nil {
		before := len(tvGuide.Channels)
		tvGuide = filterGuideChannels(tvGuide, channelFilter)
		log.Printf("Channel filter: %d → %d channels (Jellyfin has %d)", before, len(tvGuide.Channels), len(channelFilter))
	}

	log.Printf("Rendering XMLTV: %d channels, %d programs", len(tvGuide.Channels), len(tvGuide.Programs))

	// Parse embedded template
	tmpl, err := template.ParseFS(guideTmplFS, "guide.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmpFile, err := os.CreateTemp(".", "xmlguide-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if err := tmpl.Execute(tmpFile, tvGuide); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpName, "xmlguide.xmltv"); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to rename output file: %w", err)
	}
	os.Chmod("xmlguide.xmltv", 0644)

	log.Printf("Wrote guide to xmlguide.xmltv")
	saveGuideCache(tvGuide)
	return tvGuide, nil
}

// ---------- Guide cache ----------

const guideCachePath = "guide_cache.json"

type guideCache struct {
	SavedAt time.Time      `json:"saved_at"`
	Guide   guide.TVGuide  `json:"guide"`
}

// saveGuideCache persists the TVGuide to a JSON file.
func saveGuideCache(g *guide.TVGuide) {
	data, err := json.Marshal(guideCache{SavedAt: time.Now(), Guide: *g})
	if err != nil {
		log.Printf("guide cache: failed to marshal: %v", err)
		return
	}
	if err := os.WriteFile(guideCachePath, data, 0644); err != nil {
		log.Printf("guide cache: failed to write: %v", err)
		return
	}
	log.Println("Saved guide cache")
}

// loadGuideCache loads the TVGuide from the JSON cache if it's younger than maxAge.
// Returns the guide, its age, and whether it was loaded.
func loadGuideCache(maxAge time.Duration) (*guide.TVGuide, time.Duration, bool) {
	data, err := os.ReadFile(guideCachePath)
	if err != nil {
		return nil, 0, false
	}
	var c guideCache
	if err := json.Unmarshal(data, &c); err != nil {
		log.Printf("guide cache: corrupt, ignoring: %v", err)
		return nil, 0, false
	}
	age := time.Since(c.SavedAt)
	if age >= maxAge {
		return nil, age, false
	}
	return &c.Guide, age, true
}

// ---------- File rotation ----------

// rotateFiles copies the current xmlguide.xmltv to a dated file and prunes old ones.
func rotateFiles() {
	dated := fmt.Sprintf("xmlguide.%s.xmltv", time.Now().UTC().Format("20060102"))

	src, err := os.ReadFile("xmlguide.xmltv")
	if err != nil {
		log.Printf("rotate: failed to read xmlguide.xmltv: %v", err)
		return
	}

	if err := os.WriteFile(dated, src, 0644); err != nil {
		log.Printf("rotate: failed to write %s: %v", dated, err)
		return
	}
	log.Printf("Rotated guide to %s", dated)

	// Prune: keep only the 7 most recent dated files
	matches, _ := filepath.Glob("xmlguide.*.xmltv")
	if len(matches) <= 7 {
		return
	}

	// Sort descending (newest first)
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for _, old := range matches[7:] {
		log.Printf("Pruning old guide: %s", old)
		os.Remove(old)
	}
}

// ---------- Background scraper ----------

// startScraper runs the scrape cycle on a 24-hour ticker.
// If initialDelay > 0, the first scrape fires after that delay instead of 24h
// (used when we skipped the startup scrape because the file was still fresh).
func startScraper(ctx context.Context, state *GuideState, tmdbClient *tmdb.Client, logoClient *tvlogo.Client, lang, country, baseURL string, initialDelay time.Duration, jellyfinURL, jellyfinAPIKey string, filterEnabled bool) {
	if initialDelay <= 0 {
		initialDelay = 24 * time.Hour
	}

	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Scraper shutting down")
			return
		case <-timer.C:
			log.Println("Starting scheduled scrape cycle")

			var channelFilter map[string]bool
			if filterEnabled {
				cf, err := fetchJellyfinChannelNumbers(jellyfinURL, jellyfinAPIKey)
				if err != nil {
					log.Printf("Warning: could not fetch Jellyfin channels for filter, proceeding unfiltered: %v", err)
				} else {
					channelFilter = cf
				}
			}

			g, err := runScrape(tmdbClient, logoClient, lang, country, baseURL, channelFilter)
			if err != nil {
				log.Printf("Scheduled scrape failed: %v", err)
			} else {
				state.Update(g)
				rotateFiles()
				log.Println("Scheduled scrape complete")
			}
			// All subsequent runs at 24h intervals
			timer.Reset(24 * time.Hour)
		}
	}
}

// ---------- HTTP handlers ----------

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func handleXMLTV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	http.ServeFile(w, r, "xmlguide.xmltv")
}

func handleGuideJSON(state *GuideState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g := state.Get()
		if g == nil {
			http.Error(w, "Guide not available yet", http.StatusServiceUnavailable)
			return
		}

		apiGuide := guideToJSON(g)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.Encode(apiGuide)
	}
}

// ---------- Image proxy ----------

const imageCacheDir = "image_cache"

// imageURLAllowed checks whether a URL is on the proxy allowlist.
func imageURLAllowed(rawURL string) bool {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "image.tmdb.org" {
		return true
	}
	if host == "raw.githubusercontent.com" && strings.HasPrefix(u.Path, "/tv-logo/tv-logos/") {
		return true
	}
	return false
}

func handleImage(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	if !imageURLAllowed(rawURL) {
		http.Error(w, "url not allowed", http.StatusForbidden)
		return
	}

	// Cache key
	h := sha256.Sum256([]byte(rawURL))
	key := hex.EncodeToString(h[:])
	datPath := filepath.Join(imageCacheDir, key+".dat")
	typePath := filepath.Join(imageCacheDir, key+".type")

	// Cache hit — verify both files exist and are readable
	ct, ctErr := os.ReadFile(typePath)
	_, datErr := os.Stat(datPath)
	if ctErr == nil && datErr == nil {
		w.Header().Set("Content-Type", string(ct))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, datPath)
		return
	}

	// Cache miss or inconsistent — fetch upstream
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "upstream returned "+resp.Status, http.StatusBadGateway)
		return
	}

	// Ensure cache dir
	os.MkdirAll(imageCacheDir, 0755)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed reading upstream", http.StatusBadGateway)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Write cache files atomically (temp + rename)
	if tmpDat, err := os.CreateTemp(imageCacheDir, "img-*.tmp"); err == nil {
		if _, wErr := tmpDat.Write(body); wErr == nil {
			tmpDat.Close()
			os.Rename(tmpDat.Name(), datPath)
			os.WriteFile(typePath, []byte(contentType), 0644)
		} else {
			tmpDat.Close()
			os.Remove(tmpDat.Name())
		}
	}

	// Serve
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(body)
}

// ---------- Jellyfin Live TV ----------

var validJellyfinID = regexp.MustCompile(`^[0-9a-fA-F-]+$`)

func handleLiveTVConfig(jellyfinURL, jellyfinAPIKey string) http.HandlerFunc {
	enabled := jellyfinURL != "" && jellyfinAPIKey != ""
	body := []byte(`{"enabled":false}`)
	if enabled {
		body = []byte(`{"enabled":true}`)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

// handleLiveTVChannels proxies the Jellyfin channel list so the frontend
// doesn't need credentials.
func handleLiveTVChannels(jellyfinURL, jellyfinAPIKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if jellyfinURL == "" || jellyfinAPIKey == "" {
			http.Error(w, "Live TV not configured", http.StatusServiceUnavailable)
			return
		}
		url := fmt.Sprintf("%s/LiveTv/Channels?api_key=%s&SortBy=SortName&SortOrder=Ascending&AddCurrentProgram=true",
			jellyfinURL, jellyfinAPIKey)
		resp, err := http.Get(url)
		if err != nil {
			http.Error(w, "Failed to reach Jellyfin", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	}
}

// handleLiveTVTune does the three-step Jellyfin live-stream handshake
// server-side and returns a ready-to-play HLS URL.
//
// Flow: GET PlaybackInfo → POST LiveStreams/Open → build master.m3u8 URL.
func handleLiveTVTune(jellyfinURL, jellyfinAPIKey string) http.HandlerFunc {
	type playbackInfoResponse struct {
		PlaySessionId string `json:"PlaySessionId"`
		MediaSources  []struct {
			Id        string `json:"Id"`
			OpenToken string `json:"OpenToken"`
		} `json:"MediaSources"`
	}
	type openStreamResponse struct {
		MediaSource struct {
			Id           string `json:"Id"`
			LiveStreamId string `json:"LiveStreamId"`
		} `json:"MediaSource"`
	}

	client := &http.Client{Timeout: 15 * time.Second}

	jfGet := func(path string) ([]byte, error) {
		url := fmt.Sprintf("%s%s?api_key=%s", jellyfinURL, path, jellyfinAPIKey)
		resp, err := client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("GET %s returned %d: %s", path, resp.StatusCode, string(body))
		}
		return body, nil
	}

	jfPost := func(path, jsonBody string) ([]byte, error) {
		url := fmt.Sprintf("%s%s&api_key=%s", jellyfinURL, path, jellyfinAPIKey)
		req, err := http.NewRequest("POST", url, strings.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("creating POST %s: %w", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("POST %s: %w", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("POST %s returned %d: %s", path, resp.StatusCode, string(body))
		}
		return body, nil
	}

	return func(w http.ResponseWriter, r *http.Request) {
		channelId := r.URL.Query().Get("id")
		if channelId == "" {
			http.Error(w, "missing id parameter", http.StatusBadRequest)
			return
		}
		if !validJellyfinID.MatchString(channelId) {
			http.Error(w, "invalid id parameter", http.StatusBadRequest)
			return
		}

		// Step 1: Get playback info → OpenToken, PlaySessionId, MediaSourceId
		path := fmt.Sprintf("/Items/%s/PlaybackInfo", channelId)
		body, err := jfGet(path)
		if err != nil {
			log.Printf("livetv tune step 1: %v", err)
			http.Error(w, "PlaybackInfo failed: "+err.Error(), http.StatusBadGateway)
			return
		}

		var info playbackInfoResponse
		if err := json.Unmarshal(body, &info); err != nil {
			log.Printf("livetv tune: parsing playback info: %v", err)
			http.Error(w, "Failed to parse PlaybackInfo", http.StatusBadGateway)
			return
		}
		if len(info.MediaSources) == 0 {
			http.Error(w, "No media sources for channel", http.StatusBadGateway)
			return
		}

		// Step 2: Open live stream → LiveStreamId
		openBody := fmt.Sprintf(
			`{"OpenToken":%q,"PlaySessionId":%q,"ItemId":%q}`,
			info.MediaSources[0].OpenToken, info.PlaySessionId, channelId,
		)
		openPath := fmt.Sprintf("/LiveStreams/Open?PlaySessionId=%s&ItemId=%s",
			info.PlaySessionId, channelId)
		respBody, err := jfPost(openPath, openBody)
		if err != nil {
			log.Printf("livetv tune step 2: %v", err)
			http.Error(w, "LiveStreams/Open failed: "+err.Error(), http.StatusBadGateway)
			return
		}

		var opened openStreamResponse
		if err := json.Unmarshal(respBody, &opened); err != nil {
			log.Printf("livetv tune: parsing open stream: %v", err)
			http.Error(w, "Failed to parse LiveStreams/Open", http.StatusBadGateway)
			return
		}

		// Give the transcoder time to produce initial segments before
		// handing the URL to the browser.  The working jellyfinapi
		// implementation has a natural ~4s gap here because the user
		// clicks "play" after the handshake completes.
		time.Sleep(4 * time.Second)

		// Step 3: Build master.m3u8 URL with all required parameters
		streamURL := fmt.Sprintf(
			"%s/Videos/%s/master.m3u8?api_key=%s&MediaSourceId=%s&PlaySessionId=%s&LiveStreamId=%s&VideoCodec=h264&AudioCodec=aac&SegmentContainer=ts&MinSegments=1&BreakOnNonKeyFrames=true&VideoBitrate=2000000&AudioBitrate=192000&MaxWidth=1920&MaxHeight=1080&AudioStreamIndex=-1&VideoStreamIndex=-1",
			jellyfinURL, channelId, jellyfinAPIKey,
			opened.MediaSource.Id, info.PlaySessionId, opened.MediaSource.LiveStreamId,
		)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url":           streamURL,
			"playSessionId": info.PlaySessionId,
		})
	}
}

// handleLiveTVStop forwards a playback-stop notification to Jellyfin.
func handleLiveTVStop(jellyfinURL, jellyfinAPIKey string) http.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		url := fmt.Sprintf("%s/Sessions/Playing/Stopped?api_key=%s", jellyfinURL, jellyfinAPIKey)
		req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
		if err != nil {
			http.Error(w, "failed to build request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Jellyfin unreachable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		w.WriteHeader(resp.StatusCode)
	}
}

// ---------- Jellyfin channel filter ----------

// fetchJellyfinChannelNumbers queries Jellyfin for available live TV channels
// and returns a set of their channel number strings.
func fetchJellyfinChannelNumbers(jellyfinURL, jellyfinAPIKey string) (map[string]bool, error) {
	url := fmt.Sprintf("%s/LiveTv/Channels?api_key=%s&SortBy=SortName", jellyfinURL, jellyfinAPIKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching Jellyfin channels: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Jellyfin returned %d", resp.StatusCode)
	}

	var result struct {
		Items []struct {
			ChannelNumber string `json:"ChannelNumber"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding Jellyfin channels: %w", err)
	}

	allowed := make(map[string]bool, len(result.Items))
	for _, item := range result.Items {
		if item.ChannelNumber != "" {
			allowed[item.ChannelNumber] = true
		}
	}
	return allowed, nil
}

// filterGuideChannels returns a new TVGuide containing only channels whose
// number (DisplayNames[1]) is in the allowed set, along with their programs.
func filterGuideChannels(g *guide.TVGuide, allowed map[string]bool) *guide.TVGuide {
	allowedIDs := make(map[string]bool)
	var channels []guide.Channel
	for _, ch := range g.Channels {
		number := ""
		if len(ch.DisplayNames) >= 3 {
			number = ch.DisplayNames[1].Name
		}
		if allowed[number] {
			channels = append(channels, ch)
			allowedIDs[ch.ID] = true
		}
	}

	var programs []guide.Program
	for _, p := range g.Programs {
		if allowedIDs[p.Channel] {
			programs = append(programs, p)
		}
	}

	return &guide.TVGuide{
		Channels: channels,
		Programs: programs,
	}
}

// ---------- Main ----------

func main() {
	guideOnly := flag.Bool("guide-only", false, "Scrape once and exit (no server)")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	lang := util.GetEnv("GN_LANGUAGE", "en")
	country := util.GetEnv("GN_COUNTRY", "USA")
	port := util.GetEnv("PORT", "8080")
	baseURL := util.GetEnv("BASE_URL", "")

	jellyfinURL := strings.TrimRight(util.GetEnv("JELLYFIN_URL", ""), "/")
	jellyfinAPIKey := util.GetEnv("JELLYFIN_API_KEY", "")
	jellyfinConfigured := jellyfinURL != "" && jellyfinAPIKey != ""
	if jellyfinConfigured {
		log.Printf("Jellyfin Live TV integration enabled (%s)", jellyfinURL)
	}

	// Channel filter: only show channels available in Jellyfin
	channelFilterEnabled := util.GetEnv("JELLYFIN_CHANNEL_FILTER", "") != "" && jellyfinConfigured
	var channelFilter map[string]bool
	if channelFilterEnabled {
		cf, err := fetchJellyfinChannelNumbers(jellyfinURL, jellyfinAPIKey)
		if err != nil {
			log.Printf("Warning: could not fetch Jellyfin channels for filter: %v", err)
			log.Println("Channel filter will be retried on next scheduled scrape")
		} else {
			channelFilter = cf
			log.Printf("Channel filter enabled: %d Jellyfin channels", len(channelFilter))
		}
	}

	tmdbToken := util.GetEnv("TMDB_TOKEN", "")
	tmdbClient := tmdb.NewClient(tmdbToken, "tmdb_cache.json")
	if tmdbClient != nil {
		log.Println("TMDB integration enabled")
	} else {
		log.Println("No TMDB token configured, skipping image enrichment")
	}
	defer tmdbClient.Close()

	logoClient := tvlogo.NewClient(country, "tvlogo_cache.json")
	if logoClient != nil {
		log.Println("TV logo enrichment enabled")
	} else {
		log.Printf("TV logo enrichment not available for country %s", country)
	}
	defer logoClient.Close()

	// --guide-only: always scrape, write output, exit
	if *guideOnly {
		log.Println("Starting scrape (guide-only mode)...")
		if _, err := runScrape(tmdbClient, logoClient, lang, country, baseURL, channelFilter); err != nil {
			log.Fatalf("Scrape failed: %v", err)
		}
		log.Println("--guide-only: done")
		return
	}

	// Server mode: try loading cached guide data to skip a slow scrape.
	// Always re-scrape if the XMLTV file or guide cache is missing.
	var g *guide.TVGuide
	var nextScrapeIn time.Duration
	_, xmltvMissing := os.Stat("xmlguide.xmltv")
	cached, age, cacheOK := loadGuideCache(4 * time.Hour)
	if cacheOK && xmltvMissing == nil {
		log.Printf("Loaded guide from cache (%s old), skipping scrape", age.Round(time.Second))
		g = cached
		if channelFilter != nil {
			before := len(g.Channels)
			g = filterGuideChannels(g, channelFilter)
			log.Printf("Channel filter: %d → %d channels (cached guide)", before, len(g.Channels))
		}
		// Schedule next scrape for when the cache turns 24h old
		nextScrapeIn = 24*time.Hour - age
		if nextScrapeIn < time.Hour {
			nextScrapeIn = time.Hour
		}
	} else {
		if xmltvMissing != nil {
			log.Println("xmlguide.xmltv missing, scrape required")
		}
		log.Println("Starting initial scrape...")
		var err error
		g, err = runScrape(tmdbClient, logoClient, lang, country, baseURL, channelFilter)
		if err != nil {
			log.Fatalf("Initial scrape failed: %v", err)
		}
		rotateFiles()
		nextScrapeIn = 24 * time.Hour
	}

	state := &GuideState{}
	state.Update(g)

	// Signal context for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start background scraper
	log.Printf("Next scrape in %s", nextScrapeIn.Round(time.Minute))
	go startScraper(ctx, state, tmdbClient, logoClient, lang, country, baseURL, nextScrapeIn, jellyfinURL, jellyfinAPIKey, channelFilterEnabled)

	// HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/xmlguide.xmltv", handleXMLTV)
	mux.HandleFunc("/api/guide.json", handleGuideJSON(state))
	mux.HandleFunc("/img", handleImage)
	mux.HandleFunc("/api/livetv/config", handleLiveTVConfig(jellyfinURL, jellyfinAPIKey))
	if jellyfinURL != "" && jellyfinAPIKey != "" {
		mux.HandleFunc("/api/livetv/channels", handleLiveTVChannels(jellyfinURL, jellyfinAPIKey))
		mux.HandleFunc("/api/livetv/tune", handleLiveTVTune(jellyfinURL, jellyfinAPIKey))
		mux.HandleFunc("/api/livetv/stop", handleLiveTVStop(jellyfinURL, jellyfinAPIKey))
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Start server in background
	go func() {
		log.Printf("HTTP server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	log.Println("Goodbye")
}

// ---------- Enrichment helpers ----------

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// replaces broken Gracenote thumbnail URLs with TMDB
// poster images, star ratings, dates, and descriptions.
func enrichProgramThumbnails(client *tmdb.Client, programs []guide.Program) {
	if client == nil {
		return
	}

	// Phase 1: collect unique {title, isMovie} pairs
	type titleKey struct {
		title   string
		isMovie bool
	}
	seen := make(map[titleKey]bool)
	var unique []titleKey

	for _, p := range programs {
		title := strings.ToLower(html.UnescapeString(p.Title))
		isMovie := false
		for _, cat := range p.Categories {
			if cat.Name == "movie" {
				isMovie = true
				break
			}
		}
		k := titleKey{title: title, isMovie: isMovie}
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}

	log.Printf("TMDB: looking up %d unique titles", len(unique))

	// Phase 2: lookup each unique title
	results := make(map[titleKey]tmdb.CacheEntry)
	for _, k := range unique {
		results[k] = client.Lookup(k.title, k.isMovie)
	}

	// Phase 3: apply results back to programs
	enriched := 0
	for i := range programs {
		title := strings.ToLower(html.UnescapeString(programs[i].Title))
		isMovie := false
		for _, cat := range programs[i].Categories {
			if cat.Name == "movie" {
				isMovie = true
				break
			}
		}
		entry := results[titleKey{title: title, isMovie: isMovie}]
		if entry.TMDBID == 0 && entry.ImageURL == "" && entry.Rating == 0 {
			continue
		}
		enriched++
		if entry.ImageURL != "" {
			programs[i].IconSrc = entry.ImageURL
			programs[i].Images = []guide.Image{{
				URL:    entry.ImageURL,
				Type:   "poster",
				Size:   "3",
				Orient: "P",
				System: "tmdb",
			}}
		}
		if entry.Rating > 0 {
			programs[i].StarRating = fmt.Sprintf("%.1f/10", entry.Rating)
		}
		if entry.Year != "" {
			programs[i].Date = entry.Year
		}
		if entry.Overview != "" && programs[i].Description == "Unavailable" {
			programs[i].Description = xmlEscape(entry.Overview)
		}
		if entry.OrigLanguage != "" {
			programs[i].OrigLanguage = entry.OrigLanguage
		}
		if entry.TMDBID != 0 {
			tmdbEpNum := fmt.Sprintf("%d", entry.TMDBID)
			if !isMovie {
				tmdbEpNum = fmt.Sprintf("series/%d", entry.TMDBID)
			}
			programs[i].EpisodeNumbers = append(programs[i].EpisodeNumbers, guide.EpisodeNumber{
				System:        "themoviedb.org",
				EpisodeNumber: tmdbEpNum,
			})
		}
	}

	log.Printf("TMDB: enriched %d/%d programs", enriched, len(programs))
}

// resolves channel logos from the tv-logo/tv-logos repo,
// replacing dead Gracenote icon URLs with verified GitHub-hosted PNGs.
func enrichChannelIcons(client *tvlogo.Client, channels []guide.Channel) {
	if client == nil {
		return
	}

	log.Printf("TV logos: resolving icons for %d channels", len(channels))

	enriched := 0
	for i := range channels {
		logoURL := client.Resolve(channels[i].ID, channels[i].CallSign, channels[i].Affiliate)
		if logoURL != "" {
			channels[i].IconURL = logoURL
			enriched++
		} else {
			channels[i].IconURL = ""
		}
	}

	log.Printf("TV logos: enriched %d/%d channels", enriched, len(channels))
}
