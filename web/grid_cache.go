package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var gridCacheRoot = "grid_cache"

type cachedGrid struct {
	SavedAt time.Time    `json:"saved_at"`
	Source  GuideSource  `json:"source"`
	Time    int64        `json:"time"`
	Grid    GridResponse `json:"grid"`
}

func gridSourceKey(source GuideSource) string {
	parts := []string{
		source.Country,
		source.ZipCode,
		source.Headend,
		source.LineupID,
		source.Device,
		source.Language,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func gridCachePath(source GuideSource, gridTime int64) string {
	return filepath.Join(gridCacheRoot, gridSourceKey(source), fmt.Sprintf("%d.json", gridTime))
}

func saveGridCache(gridTime int64, source GuideSource, grid *GridResponse) error {
	if grid == nil {
		return fmt.Errorf("cannot cache nil grid")
	}

	path := gridCachePath(source, gridTime)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create grid cache directory: %w", err)
	}

	data, err := json.Marshal(cachedGrid{
		SavedAt: time.Now().UTC(),
		Source:  source,
		Time:    gridTime,
		Grid:    *grid,
	})
	if err != nil {
		return fmt.Errorf("marshal grid cache: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "grid-*.tmp")
	if err != nil {
		return fmt.Errorf("create grid cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write grid cache temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync grid cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close grid cache temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod grid cache temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace grid cache file: %w", err)
	}
	return nil
}

func loadGridCache(gridTime int64, source GuideSource) (*GridResponse, time.Duration, error) {
	path := gridCachePath(source, gridTime)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read grid cache: %w", err)
	}

	var cached cachedGrid
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, 0, fmt.Errorf("decode grid cache: %w", err)
	}
	if cached.Source != source {
		return nil, 0, fmt.Errorf("grid cache source mismatch")
	}
	if cached.Time != gridTime {
		return nil, 0, fmt.Errorf("grid cache time mismatch: got %d, want %d", cached.Time, gridTime)
	}
	return &cached.Grid, time.Since(cached.SavedAt), nil
}

func pruneGridCache(source GuideSource, cutoff time.Time) error {
	dir := filepath.Join(gridCacheRoot, gridSourceKey(source))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read grid cache directory: %w", err)
	}

	cutoffUnix := cutoff.Unix()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		stamp := strings.TrimSuffix(entry.Name(), ".json")
		gridTime, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || gridTime >= cutoffUnix {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old grid cache %s: %w", entry.Name(), err)
		}
	}
	return nil
}
