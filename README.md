# GraceNote XMLTV Scraper

Generate XMLTV guide data from GraceNote/TMS listings for use with Jellyfin, Plex, Emby, TVHeadend, and other DVR/IPTV software.

## Features

- Scrapes 14 days of GraceNote/TMS program listings and outputs standard XMLTV format
- Enriches programs with TMDB poster images, ratings, descriptions, and release dates
- Enriches channel icons via the [tv-logo/tv-logos](https://github.com/tv-logo/tv-logos) project
- Runs as a long-lived server with automatic 24-hour refresh, or as a one-shot scrape for cron jobs
- Guide data cached on disk — fast restarts without re-scraping
- Automatic XMLTV file rotation with 7-day retention
- Optional Jellyfin Live TV integration with in-browser streaming
- Optional channel filter to limit guide output to Jellyfin-available channels
- Bonus: built-in retro TV guide web UI ("The Grid")

## Jellyfin / Plex Setup

1. Run the scraper in server mode (see below)
2. In your DVR software, add an XMLTV guide source pointing to:
   ```
   http://<your-host>:8080/xmlguide.xmltv
   ```
3. Guide data refreshes automatically every 24 hours

Alternatively, use `--guide-only` mode with a cron job and point your DVR software at the local `xmlguide.xmltv` file.

## Quick Start (Docker Compose)

1. Clone the repo:
   ```sh
   git clone https://github.com/daniel-widrick/GraceNoteScraper.git
   cd GraceNoteScraper
   ```

2. Copy and fill in the environment file:
   ```sh
   cp .env.example .env
   # Edit .env with your headend/lineup details and optional TMDB token
   ```

3. Start the container:
   ```sh
   docker compose up -d
   ```

4. Point your DVR software at `http://<your-host>:8080/xmlguide.xmltv`

Guide data, caches, and images are persisted in a Docker volume. The container restarts automatically and refreshes guide data every 24 hours.

To view logs:
```sh
docker compose logs -f
```

To rebuild after pulling updates:
```sh
docker compose up -d --build
```

## Requirements

- Docker and Docker Compose, **or** Go 1.25+ for building from source
- A GraceNote/TMS headend lineup ID
- (Optional) A [TMDB API read access token](https://www.themoviedb.org/settings/api) for poster images and metadata

## Building from Source

If you prefer to run without Docker:

```sh
go build -o gracenotescraper .
cp .env.example .env
# Edit .env
./gracenotescraper
```

### Scrape-only mode

Scrapes once, writes `xmlguide.xmltv` to the working directory, and exits. Useful for cron-based setups where you don't need the server running.

```sh
./gracenotescraper --guide-only
```

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `GN_HEADEND` | GraceNote headend/lineup ID | — |
| `GN_LINEUP` | Full lineup string | — |
| `GN_COUNTRY` | Country code | `USA` |
| `GN_ZIPCODE` | ZIP code for lineup | — |
| `GN_LANGUAGE` | Language code | `en` |
| `GN_DEVICE` | Device identifier | `-` |
| `TMDB_TOKEN` | TMDB read access token (optional) | — |
| `BASE_URL` | Server base URL — rewrites XMLTV image URLs to use the built-in proxy cache (e.g. `http://192.168.1.50:8080`) | — |
| `PORT` | HTTP server port | `8080` |
| `JELLYFIN_URL` | Jellyfin server URL (optional — enables live TV integration) | — |
| `JELLYFIN_API_KEY` | Jellyfin API key | — |
| `JELLYFIN_CHANNEL_FILTER` | Set to any non-empty value to filter guide to only Jellyfin-available channels. Requires `JELLYFIN_URL` and `JELLYFIN_API_KEY`. | — |

## HTTP Endpoints

| Endpoint | Description |
|---|---|
| `GET /xmlguide.xmltv` | XMLTV guide data (point your DVR here) |
| `GET /api/guide.json` | Guide data as JSON |
| `GET /` | The Grid — built-in web UI |
| `GET /img?url=...` | Image proxy with local cache |
| `GET /api/livetv/config` | Returns `{"enabled":true/false}` — whether Jellyfin live TV is configured |
| `GET /api/livetv/channels` | Proxies Jellyfin channel list (requires `JELLYFIN_URL` and `JELLYFIN_API_KEY`) |
| `GET /api/livetv/tune?id=<channelId>` | Starts a live stream for the given channel and returns an HLS URL |
| `POST /api/livetv/stop` | Forwards a playback-stop notification to Jellyfin to end a live stream |

## The Grid

The server includes a built-in retro-styled TV guide web UI at the root URL. It auto-scrolls through your channel lineup and shows program details, posters, and metadata. Handy for a quick glance at what's on without opening your DVR app.

![The Grid](https://gist.githubusercontent.com/daniel-widrick/2c52c4d023ffe75d163b4eff58263c77/raw/demo.gif)

## Project Structure

```
main.go          Entry point, HTTP server, scraper, image proxy
guide/           GraceNote data types and XMLTV conversion
web/             HTTP client for GraceNote API
tmdb/            TMDB client and cache
tvlogo/          TV logo resolver and cache
util/            Shared helpers
index.html       The Grid web UI (embedded at build time)
guide.tmpl       XMLTV output template (embedded at build time)
```

---

<sub>Portions of this project were developed with the assistance of generative AI ([Claude](https://claude.ai)).</sub>
