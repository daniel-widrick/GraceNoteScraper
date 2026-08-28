package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/daniel-widrick/GraceNoteScraper/appconfig"
	"github.com/daniel-widrick/GraceNoteScraper/web"
)

//go:embed setup.html
var setupFS embed.FS

type providerFinder interface {
	FindProviders(ctx context.Context, country, postalCode, language string) (*web.ProviderResponse, error)
}

type providerChannelCounter interface {
	CountChannels(ctx context.Context, country, postalCode, language string, provider web.Provider) (int, error)
}

type webProviderChannelCounter struct{}

func (webProviderChannelCounter) CountChannels(ctx context.Context, country, postalCode, language string, provider web.Provider) (int, error) {
	client := web.NewClient(web.Preferences{
		Country: country, ZipCode: postalCode, Headend: provider.HeadendID,
		LineupId: provider.LineupID, Device: provider.Device, Language: language,
	})
	grid, err := client.GetDataByTimeContext(ctx, time.Now().UTC().Truncate(6*time.Hour).Unix())
	if err != nil {
		return 0, err
	}
	return len(grid.Channels), nil
}

type setupServer struct {
	store           *appconfig.Store
	providers       providerFinder
	channelCounter  providerChannelCounter
	scrapeStatus    *scrapeStatus
	onProviderSaved func(changed bool)
}

type setupConfigResponse struct {
	Configured bool                       `json:"configured"`
	Source     string                     `json:"source,omitempty"`
	Gracenote  *appconfig.GracenoteConfig `json:"gracenote,omitempty"`
}

type providerSelection struct {
	Country    string       `json:"country"`
	PostalCode string       `json:"postalCode"`
	Language   string       `json:"language"`
	Provider   web.Provider `json:"provider"`
}

func (s *setupServer) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/setup" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := setupFS.ReadFile("setup.html")
	if err != nil {
		http.Error(w, "setup page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (s *setupServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	config, configured, source := s.store.Get()
	response := setupConfigResponse{Configured: configured, Source: source}
	if configured {
		response.Gracenote = &config.Gracenote
	}
	writeSetupJSON(w, http.StatusOK, response)
}

func (s *setupServer) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	country := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country")))
	postalCode := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("postalCode")))
	language := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("language")))
	if country == "" {
		country = "USA"
	}
	if language == "" {
		language = "en-us"
	}
	if err := appconfig.ValidateLookup(country, postalCode, language); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := s.providers.FindProviders(r.Context(), country, postalCode, language)
	if err != nil {
		http.Error(w, "Unable to retrieve lineups from Gracenote: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.addProviderChannelCounts(r.Context(), country, postalCode, language, result.Providers)
	sort.SliceStable(result.Providers, func(i, j int) bool {
		left := providerTypeOrder(result.Providers[i].Type)
		right := providerTypeOrder(result.Providers[j].Type)
		if left != right {
			return left < right
		}
		return strings.ToLower(result.Providers[i].Name) < strings.ToLower(result.Providers[j].Name)
	})
	w.Header().Set("Cache-Control", "no-store")
	writeSetupJSON(w, http.StatusOK, result)
}

func (s *setupServer) addProviderChannelCounts(ctx context.Context, country, postalCode, language string, providers []web.Provider) {
	if s.channelCounter == nil || len(providers) == 0 {
		return
	}
	workers := 4
	if workers > len(providers) {
		workers = len(providers)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				count, err := s.channelCounter.CountChannels(ctx, country, postalCode, language, providers[index])
				if err != nil {
					log.Printf("Unable to count channels for lineup %s: %v", providers[index].LineupID, err)
					continue
				}
				providers[index].ChannelCount = count
				providers[index].ChannelCountKnown = true
			}
		}()
	}
	for index := range providers {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *setupServer) handleScrapeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.scrapeStatus == nil {
		writeSetupJSON(w, http.StatusOK, scrapeStatusSnapshot{Stage: "idle", Message: "Guide status is unavailable", UpdatedAt: time.Now().UTC()})
		return
	}
	writeSetupJSON(w, http.StatusOK, s.scrapeStatus.snapshotValue())
}

func (s *setupServer) handleProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var selection providerSelection
	if err := decoder.Decode(&selection); err != nil {
		http.Error(w, "Invalid provider selection: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Invalid provider selection: request must contain one JSON object", http.StatusBadRequest)
		return
	}

	selection.Country = strings.ToUpper(strings.TrimSpace(selection.Country))
	selection.PostalCode = strings.ToUpper(strings.TrimSpace(selection.PostalCode))
	selection.Language = strings.ToLower(strings.TrimSpace(selection.Language))
	if err := appconfig.ValidateLookup(selection.Country, selection.PostalCode, selection.Language); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	config := appconfig.Config{
		Version: appconfig.CurrentVersion,
		Gracenote: appconfig.GracenoteConfig{
			Country:      selection.Country,
			PostalCode:   selection.PostalCode,
			Language:     selection.Language,
			ProviderType: selection.Provider.Type,
			Device:       selection.Provider.Device,
			LineupID:     selection.Provider.LineupID,
			ProviderName: selection.Provider.Name,
			Location:     selection.Provider.Location,
			HeadendID:    selection.Provider.HeadendID,
		},
	}
	if err := config.Validate(); err != nil {
		http.Error(w, "Invalid provider selection: "+err.Error(), http.StatusBadRequest)
		return
	}

	oldConfig, wasConfigured, _ := s.store.Get()
	changed := !wasConfigured || oldConfig.Fingerprint() != config.Fingerprint()
	if err := s.store.Save(config); err != nil {
		http.Error(w, fmt.Sprintf("Unable to save provider: %v", err), http.StatusInternalServerError)
		return
	}
	if s.onProviderSaved != nil {
		s.onProviderSaved(changed)
	}
	savedConfig, _, _ := s.store.Get()

	writeSetupJSON(w, http.StatusOK, map[string]any{
		"configured":   true,
		"changed":      changed,
		"scrapeQueued": true,
		"provider":     savedConfig.Gracenote,
	})
}

func providerTypeOrder(providerType string) int {
	switch strings.ToUpper(providerType) {
	case "CABLE":
		return 0
	case "SATELLITE":
		return 1
	case "OTA":
		return 2
	default:
		return 3
	}
}

func writeSetupJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(true)
	_ = encoder.Encode(value)
}
