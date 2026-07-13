package tvlogo

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

const cacheTTL = 30 * 24 * time.Hour

type CacheEntry struct {
	LogoURL        string `json:"logo_url"`
	FetchedAt      int64  `json:"fetched_at"`
	MatcherVersion int    `json:"matcher_version,omitempty"`
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]CacheEntry
	path    string
}

func LoadCache(path string) *Cache {
	c := &Cache{
		entries: make(map[string]CacheEntry),
		path:    path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}

	if err := json.Unmarshal(data, &c.entries); err != nil {
		log.Printf("tvlogo: cache file corrupt, starting fresh: %v", err)
		c.entries = make(map[string]CacheEntry)
	}
	return c
}

func (c *Cache) Get(key string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return CacheEntry{}, false
	}
	if time.Since(time.Unix(entry.FetchedAt, 0)) > cacheTTL {
		delete(c.entries, key)
		return CacheEntry{}, false
	}
	// Retry failures from older matcher versions so logic improvements take effect
	// without a manual cache wipe. Successful matches are preserved across versions.
	if entry.MatcherVersion < matcherVersion && entry.LogoURL == "" {
		delete(c.entries, key)
		return CacheEntry{}, false
	}
	return entry, true
}

func (c *Cache) Set(key string, entry CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry.FetchedAt = time.Now().Unix()
	entry.MatcherVersion = matcherVersion
	c.entries[key] = entry
}

func (c *Cache) Save() {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		log.Printf("tvlogo: failed to marshal cache: %v", err)
		return
	}
	if err := os.WriteFile(c.path, data, 0644); err != nil {
		log.Printf("tvlogo: failed to write cache file: %v", err)
	}
}
