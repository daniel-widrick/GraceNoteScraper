package main

import (
	"sync"
	"time"
)

type scrapeStatusSnapshot struct {
	Running     bool      `json:"running"`
	Stage       string    `json:"stage"`
	Message     string    `json:"message"`
	Completed   int       `json:"completed,omitempty"`
	Total       int       `json:"total,omitempty"`
	Channels    int       `json:"channels,omitempty"`
	Programs    int       `json:"programs,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
}

type scrapeStatus struct {
	mu       sync.RWMutex
	snapshot scrapeStatusSnapshot
}

func newScrapeStatus(guideReady bool, channels, programs int) *scrapeStatus {
	status := &scrapeStatus{}
	now := time.Now().UTC()
	if guideReady {
		status.snapshot = scrapeStatusSnapshot{
			Stage: "ready", Message: "Guide ready", Channels: channels, Programs: programs,
			UpdatedAt: now, CompletedAt: now,
		}
	} else {
		status.snapshot = scrapeStatusSnapshot{Stage: "idle", Message: "Waiting for a guide build", UpdatedAt: now}
	}
	return status
}

func (s *scrapeStatus) queue(message string) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.snapshot = scrapeStatusSnapshot{Running: true, Stage: "queued", Message: message, UpdatedAt: now}
	s.mu.Unlock()
}

func (s *scrapeStatus) start(message string) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.snapshot = scrapeStatusSnapshot{Running: true, Stage: "starting", Message: message, StartedAt: now, UpdatedAt: now}
	s.mu.Unlock()
}

func (s *scrapeStatus) update(stage, message string, completed, total, channels, programs int) {
	now := time.Now().UTC()
	s.mu.Lock()
	startedAt := s.snapshot.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	s.snapshot = scrapeStatusSnapshot{
		Running: true, Stage: stage, Message: message, Completed: completed, Total: total,
		Channels: channels, Programs: programs, StartedAt: startedAt, UpdatedAt: now,
	}
	s.mu.Unlock()
}

func (s *scrapeStatus) ready(channels, programs int) {
	now := time.Now().UTC()
	s.mu.Lock()
	startedAt := s.snapshot.StartedAt
	s.snapshot = scrapeStatusSnapshot{
		Stage: "ready", Message: "Guide ready", Channels: channels, Programs: programs,
		StartedAt: startedAt, UpdatedAt: now, CompletedAt: now,
	}
	s.mu.Unlock()
}

func (s *scrapeStatus) fail(message string) {
	now := time.Now().UTC()
	s.mu.Lock()
	startedAt := s.snapshot.StartedAt
	s.snapshot = scrapeStatusSnapshot{
		Stage: "error", Message: message, StartedAt: startedAt, UpdatedAt: now, CompletedAt: now,
	}
	s.mu.Unlock()
}

func (s *scrapeStatus) snapshotValue() scrapeStatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}
