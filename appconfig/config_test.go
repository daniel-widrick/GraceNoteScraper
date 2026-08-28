package appconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testConfig() Config {
	return Config{
		Version: CurrentVersion,
		Gracenote: GracenoteConfig{
			Country:      "USA",
			PostalCode:   "11743",
			Language:     "en-us",
			ProviderType: "CABLE",
			Device:       "X",
			LineupID:     "USA-NY67791-DEFAULT",
			ProviderName: "Verizon Fios - Digital",
			Location:     "Huntington",
			HeadendID:    "NY67791",
		},
	}
}

func TestStoreSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if _, configured, _ := store.Get(); configured {
		t.Fatal("new store is configured")
	}

	want := testConfig()
	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("config permissions = %o, want 600", got)
		}
	}

	reloaded, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore(reload) error = %v", err)
	}
	got, configured, source := reloaded.Get()
	if !configured || source != "file" {
		t.Fatalf("configured = %v, source = %q", configured, source)
	}
	if got.Fingerprint() != want.Fingerprint() {
		t.Fatalf("fingerprint = %q, want %q", got.Fingerprint(), want.Fingerprint())
	}
}

func TestStoreBootstrapsCompleteEnvironment(t *testing.T) {
	t.Setenv("GN_COUNTRY", "USA")
	t.Setenv("GN_ZIPCODE", "90210")
	t.Setenv("GN_LANGUAGE", "en-us")
	t.Setenv("GN_DEVICE", "X")
	t.Setenv("GN_LINEUP", "USA-DITV803-DEFAULT")
	t.Setenv("GN_HEADEND", "DITV803")

	store, err := LoadStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	config, configured, source := store.Get()
	if !configured || source != "environment" {
		t.Fatalf("configured = %v, source = %q", configured, source)
	}
	if config.Gracenote.PostalCode != "90210" || config.Gracenote.LineupID != "USA-DITV803-DEFAULT" {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestSavedConfigurationWinsOverEnvironment(t *testing.T) {
	t.Setenv("GN_ZIPCODE", "90210")
	t.Setenv("GN_LINEUP", "USA-DITV803-DEFAULT")
	t.Setenv("GN_HEADEND", "DITV803")

	path := filepath.Join(t.TempDir(), "config.json")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if err := store.Save(testConfig()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore(reload) error = %v", err)
	}
	config, _, source := reloaded.Get()
	if source != "file" || config.Gracenote.PostalCode != "11743" {
		t.Fatalf("saved configuration did not win: source=%q config=%+v", source, config)
	}
}

func TestConfigValidationRejectsIncompleteProvider(t *testing.T) {
	config := testConfig()
	config.Gracenote.HeadendID = ""
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want validation error")
	}
}

func TestConfigValidationRejectsFutureVersion(t *testing.T) {
	config := testConfig()
	config.Version = CurrentVersion + 1
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want version error")
	}
}
