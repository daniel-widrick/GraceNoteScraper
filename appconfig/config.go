package appconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/daniel-widrick/GraceNoteScraper/web"
)

const CurrentVersion = 1

var (
	countryPattern  = regexp.MustCompile(`^[A-Z]{3}$`)
	postalPattern   = regexp.MustCompile(`^[A-Z0-9][A-Z0-9 -]{1,15}$`)
	languagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z]{2})?$`)
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	typePattern     = regexp.MustCompile(`^[A-Z][A-Z0-9 _-]{0,31}$`)
)

// Config is the persisted, non-secret application configuration.
type Config struct {
	Version   int             `json:"version"`
	Gracenote GracenoteConfig `json:"gracenote"`
}

// GracenoteConfig identifies the active provider lineup.
type GracenoteConfig struct {
	Country      string `json:"country"`
	PostalCode   string `json:"postalCode"`
	Language     string `json:"language"`
	ProviderType string `json:"providerType,omitempty"`
	Device       string `json:"device"`
	LineupID     string `json:"lineupId"`
	ProviderName string `json:"providerName"`
	Location     string `json:"location,omitempty"`
	HeadendID    string `json:"headendId"`
}

func (c Config) normalized() Config {
	c.Gracenote.Country = strings.ToUpper(strings.TrimSpace(c.Gracenote.Country))
	c.Gracenote.PostalCode = strings.ToUpper(strings.TrimSpace(c.Gracenote.PostalCode))
	c.Gracenote.Language = strings.ToLower(strings.TrimSpace(c.Gracenote.Language))
	c.Gracenote.ProviderType = strings.ToUpper(strings.TrimSpace(c.Gracenote.ProviderType))
	c.Gracenote.Device = strings.TrimSpace(c.Gracenote.Device)
	c.Gracenote.LineupID = strings.TrimSpace(c.Gracenote.LineupID)
	c.Gracenote.ProviderName = strings.TrimSpace(c.Gracenote.ProviderName)
	c.Gracenote.Location = strings.TrimSpace(c.Gracenote.Location)
	c.Gracenote.HeadendID = strings.TrimSpace(c.Gracenote.HeadendID)
	return c
}

func ValidateLookup(country, postalCode, language string) error {
	country = strings.ToUpper(strings.TrimSpace(country))
	postalCode = strings.ToUpper(strings.TrimSpace(postalCode))
	language = strings.ToLower(strings.TrimSpace(language))
	if !countryPattern.MatchString(country) {
		return errors.New("country must be a three-letter code")
	}
	if !postalPattern.MatchString(postalCode) {
		return errors.New("postal code is invalid")
	}
	if !languagePattern.MatchString(language) {
		return errors.New("language must look like en or en-us")
	}
	return nil
}

func (c Config) Validate() error {
	c = c.normalized()
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported configuration version %d", c.Version)
	}
	if !countryPattern.MatchString(c.Gracenote.Country) {
		return errors.New("country must be a three-letter code")
	}
	if !postalPattern.MatchString(c.Gracenote.PostalCode) {
		return errors.New("postal code is invalid")
	}
	if !languagePattern.MatchString(c.Gracenote.Language) {
		return errors.New("language must look like en or en-us")
	}
	if c.Gracenote.ProviderType != "" && !typePattern.MatchString(c.Gracenote.ProviderType) {
		return errors.New("provider type is invalid")
	}
	if c.Gracenote.Device != "" && !idPattern.MatchString(c.Gracenote.Device) {
		return errors.New("provider device is invalid")
	}
	if !idPattern.MatchString(c.Gracenote.LineupID) {
		return errors.New("lineup ID is invalid")
	}
	if !idPattern.MatchString(c.Gracenote.HeadendID) {
		return errors.New("headend ID is invalid")
	}
	if c.Gracenote.ProviderName == "" || len(c.Gracenote.ProviderName) > 200 {
		return errors.New("provider name is invalid")
	}
	if len(c.Gracenote.Location) > 200 {
		return errors.New("provider location is invalid")
	}
	return nil
}

func (c Config) Preferences() web.Preferences {
	c = c.normalized()
	return web.Preferences{
		Country:  c.Gracenote.Country,
		ZipCode:  c.Gracenote.PostalCode,
		Headend:  c.Gracenote.HeadendID,
		LineupId: c.Gracenote.LineupID,
		Device:   c.Gracenote.Device,
		Language: c.Gracenote.Language,
	}
}

// Fingerprint identifies every setting that changes a Gracenote grid source.
func (c Config) Fingerprint() string {
	p := c.Preferences()
	value := strings.Join([]string{
		p.Country,
		p.ZipCode,
		p.Headend,
		p.LineupId,
		p.Device,
		p.Language,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// FromEnvironment returns a complete legacy GN_* configuration, if present.
func FromEnvironment() (Config, bool) {
	postalCode := strings.TrimSpace(os.Getenv("GN_ZIPCODE"))
	headendID := strings.TrimSpace(os.Getenv("GN_HEADEND"))
	lineupID := strings.TrimSpace(os.Getenv("GN_LINEUP"))
	if postalCode == "" || headendID == "" || lineupID == "" {
		return Config{}, false
	}

	country := strings.TrimSpace(os.Getenv("GN_COUNTRY"))
	if country == "" {
		country = "USA"
	}
	language := strings.TrimSpace(os.Getenv("GN_LANGUAGE"))
	if language == "" {
		language = "en-us"
	}
	device := strings.TrimSpace(os.Getenv("GN_DEVICE"))
	if device == "" {
		device = "-"
	}

	config := Config{
		Version: CurrentVersion,
		Gracenote: GracenoteConfig{
			Country:      country,
			PostalCode:   postalCode,
			Language:     language,
			Device:       device,
			LineupID:     lineupID,
			ProviderName: "Environment configuration",
			HeadendID:    headendID,
		},
	}.normalized()
	if err := config.Validate(); err != nil {
		return Config{}, false
	}
	return config, true
}

type Store struct {
	mu         sync.RWMutex
	path       string
	config     Config
	configured bool
	source     string
}

// LoadStore loads saved setup first, then falls back to a complete legacy
// environment configuration. A non-nil store is returned even when a saved
// file is invalid so server mode can still expose /setup for recovery.
func LoadStore(path string) (*Store, error) {
	store := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return store, fmt.Errorf("reading configuration: %w", err)
		}
		if config, ok := FromEnvironment(); ok {
			store.config = config
			store.configured = true
			store.source = "environment"
		}
		return store, nil
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return store, fmt.Errorf("decoding configuration: %w", err)
	}
	config = config.normalized()
	if err := config.Validate(); err != nil {
		return store, fmt.Errorf("validating configuration: %w", err)
	}
	store.config = config
	store.configured = true
	store.source = "file"
	return store, nil
}

func (s *Store) Get() (Config, bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config, s.configured, s.source
}

// WhileCurrent runs fn while holding a read lock only when fingerprint still
// identifies the active source. Save waits for fn to finish, which prevents an
// old scrape from publishing files after a provider change.
func (s *Store) WhileCurrent(fingerprint string, fn func() error) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.configured || s.config.Fingerprint() != fingerprint {
		return false, nil
	}
	return true, fn()
}

func (s *Store) Save(config Config) error {
	if config.Version == 0 {
		config.Version = CurrentVersion
	}
	config = config.normalized()
	if err := config.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding configuration: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating configuration directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".gracenote-config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary configuration: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securing temporary configuration: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing configuration: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing configuration: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing configuration: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replacing configuration: %w", err)
	}
	removeTemp = false
	if err := os.Chmod(s.path, 0600); err != nil {
		return fmt.Errorf("securing configuration: %w", err)
	}

	s.config = config
	s.configured = true
	s.source = "file"
	return nil
}
