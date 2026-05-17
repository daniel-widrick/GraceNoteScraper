# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
# Build
go build -o gracenotescraper .

# Run (server mode, port 8080)
./gracenotescraper

# Run (scrape once and exit — no server)
./gracenotescraper --guide-only

# Docker
docker compose up -d
docker compose logs -f
docker compose up -d --build

# Release builds (CGO disabled, trimmed)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o gracenotescraper .
```

There are no test files in this project.

## Architecture

The binary is a single Go process that scrapes GraceNote/TMS for 14 days of TV listings and serves the data as XMLTV over HTTP. The main orchestration lives entirely in `main.go`.

**Data flow:**

1. `web.Client.GetDataByTime` fetches 6-hour grid slices from the GraceNote API (`tvlistings.gracenote.com/api/grid`) — 56 slots for 14 days. A 5-second sleep separates requests. Raw JSON types live in `web/web.go`.
2. `guide.ConvertChannel` / `guide.ConvertEvent` translate the raw JSON into `guide.TVGuide` (internal canonical types). The `guide.tmpl` template renders these to XMLTV. Both `index.html` and `guide.tmpl` are embedded at build time via `//go:embed`.
3. `tmdb.Client.Lookup` enriches programs (poster images, ratings, overview, year) via TMDB search API. Deduplicates by `(title, isMovie)` before hitting the API. Rate-limited to ~4 req/sec.
4. `tvlogo.Client.Resolve` replaces Gracenote channel icons with verified PNGs from `github.com/tv-logo/tv-logos`. Generates candidate URL slugs from callsign/affiliate name and HEAD-checks each (rate-limited to ~5 req/sec).
5. `fixDeadImageURLs` rewrites `zap2it.tmsimg.com` → `tmsimg.com` for broken Gracenote image URLs.
6. If `BASE_URL` is set, all image URLs are rewritten to route through the local `/img` proxy endpoint.

**Caching layers:**

| Cache | File | TTL |
|---|---|---|
| Guide (in-memory + disk) | `guide_cache.json` | 4h (startup skip) / 24h (rescrape) |
| TMDB lookups | `tmdb_cache.json` | 7 days |
| TV logo HEAD checks | `tvlogo_cache.json` | persisted, no expiry |
| Image proxy | `image_cache/` dir | indefinite (per-URL SHA256 key) |

**Server mode startup logic:** On start, if `xmlguide.xmltv` and `guide_cache.json` both exist and the cache is under 4 hours old, the scrape is skipped. The next scrape fires when the cache would have been 24 hours old. A `sync.RWMutex`-guarded `GuideState` struct holds the live `*guide.TVGuide` and is swapped atomically after each background scrape.

**Jellyfin integration:** Optional. When `JELLYFIN_URL` + `JELLYFIN_API_KEY` are set, three extra routes are registered (`/api/livetv/channels`, `/api/livetv/tune`, `/api/livetv/stop`). The tune flow does a 3-step Jellyfin handshake (PlaybackInfo → LiveStreams/Open → build master.m3u8 URL) with a hardcoded 4-second delay before returning the HLS URL. Channel filter (`JELLYFIN_CHANNEL_FILTER`) reduces the guide to only channels Jellyfin has in its live TV lineup, matched by channel number.

**Image proxy allowlist:** Only `image.tmdb.org`, `tmsimg.com`, and `raw.githubusercontent.com/tv-logo/tv-logos/` paths are proxied. All other URLs return 403.
