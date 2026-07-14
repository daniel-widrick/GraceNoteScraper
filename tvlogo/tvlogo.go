package tvlogo

import (
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	baseRawURL = "https://raw.githubusercontent.com/tv-logo/tv-logos/main/countries"
	rateDelay  = 200 * time.Millisecond // ~5 req/sec
)

type countryInfo struct {
	Dir    string
	Suffix string
}

var countryMap = map[string]countryInfo{
	"USA": {Dir: "united-states", Suffix: "-us"},
	"CAN": {Dir: "canada", Suffix: "-ca"},
	"GBR": {Dir: "united-kingdom", Suffix: "-uk"},
}

// affiliateAliases maps full affiliate names to their common short forms
// when algorithmic normalization wouldn't produce the right slug.
var affiliateAliases = map[string]string{
	"home box office":                              "hbo",
	"national broadcasting company":                "nbc",
	"american broadcasting company":                "abc",
	"cbs television network":                       "cbs-logo-white",
	"fox entertainment":                            "fox",
	"fox broadcasting":                             "fox",
	"fox broadcasting company":                     "fox",
	"turner network television":                    "tnt",
	"entertainment and sports programming network": "espn",
	"cable news network":                           "cnn",
	"the weather channel":                          "weather-channel",
	"comedy central":                               "comedy-central",
	"cartoon network":                              "cartoon-network",
	"animal planet":                                "animal-planet",
	"public broadcasting service":                  "pbs",
	"cable-satellite public affairs network":       "c-span-1",
	"turner classic movies":                        "tcm",
	"american movie classics":                      "amc",
	"freeform":                                     "freeform",
	"fx networks":                                  "fx",
	"investigation discovery":                      "investigation-discovery",
	"oprah winfrey network":                        "oprah-winfrey-network",
	"a and e":                                      "a-and-e",
	"a&e":                                          "a-and-e",
}

// networkSlugs maps affiliate name variations to their short network slug,
// used to generate {network}-{number}-{callsign} patterns for local affiliates.
var networkSlugs = map[string]string{
	"abc":                           "abc",
	"american broadcasting company": "abc",
	"cbs":                           "cbs",
	"cbs television network":        "cbs",
	"nbc":                           "nbc",
	"national broadcasting company": "nbc",
	"fox":                           "fox",
	"fox broadcasting":              "fox",
	"fox broadcasting company":      "fox",
	"fox entertainment":             "fox",
	"the cw":                        "cw",
	"cw":                            "cw",
	"pbs":                           "pbs",
	"public broadcasting service":   "pbs",
	"telemundo":                     "telemundo",
	"univision":                     "univision",
	"unimas":                        "unimas",
	"my network tv":                 "my-network-tv",
}

// noiseWords are stripped from affiliate names during normalization.
// Notably excludes "channel", "network", and "tv" since many repo slugs include those words.
var noiseWords = map[string]bool{
	"broadcasting": true,
	"company":      true,
	"corporation":  true,
	"inc":          true,
}

// matches common HD/SD/DT/Plus suffixes on callsigns (inline, e.g. "MAXHDP", "TOONP").
var hdSuffixRe = regexp.MustCompile(`(?i)(hdp|hp|hd[234]|hd|sd|dt2|dt|p)$`)

// matches dash-separated station suffixes like -TV, -DT, -HD, -LD, -DT2.
var dashSuffixRe = regexp.MustCompile(`(?i)-(tv|dt|hd|ld|dt2|hd2|hd3)$`)

// Bump when matching logic changes. Cache entries below this version with
// empty results are re-checked on next access (matched entries are preserved).
const matcherVersion = 2

// localAffiliateDir is the tv-logos repo subdirectory holding US local station
// logos. It is prepended to local-affiliate slugs so buildURL resolves e.g.
// countries/united-states/us-local/abc-7-kabc-us.png.
const localAffiliateDir = "us-local/"

// callsignSlugs maps cryptic GraceNote callsign abbreviations to tv-logo repo slugs.
// Many providers send these short callsigns with NO affiliate name, so direct
// slugification fails (e.g., "HISTORY" never matches "history-channel-us.png").
// Keys must be lowercase. Lookup is tried against both the raw and suffix-stripped
// callsign, so entries here should be the suffix-stripped form when applicable.
var callsignSlugs = map[string]string{
	// A&E / History / Discovery family
	"aetv":    "a-and-e",
	"ahc":     "american-heroes-channel",
	"apl":     "animal-planet",
	"history": "history-channel",
	"hstry":   "history-channel",
	"histe":   "history-en-espanol",
	"dsc":     "discovery-channel",
	"dsce":    "discovery-en-espanol",
	"dfam":    "discovery-family",
	"dfc":     "discovery-family",
	"dlc":     "discovery-life",
	"dest":    "destination-america",
	"science": "discovery-science",
	"sci":     "discovery-science",
	"id":      "investigation-discovery",
	"idtv":    "investigation-discovery",
	// Disney family
	"disn": "disney-channel",
	"dsn":  "disney-channel",
	"dxd":  "disney-xd",
	"djch": "disney-jr",
	"djr":  "disney-jr",
	// BBC / News
	"bbca":    "bbc-america",
	"bbcaus":  "bbc-america",
	"cnbc":    "cnbc",
	"fnc":     "fox-news",
	"fbn":     "fox-business",
	"hln":     "hln",
	"msnow":   "ms-now",
	"msnbc":   "msnbc-alt",
	"newsmx":  "newsmax-tv",
	"nwsntn":  "news-nation",
	"newsntn": "news-nation",
	"nwsnt":   "news-nation",
	// BET / Music
	"bet":    "bet",
	"bher":   "bet-her",
	"mtv":    "mtv",
	"mtv2":   "mtv-2",
	"vh1":    "vh1",
	"cmtv":   "cmt",
	"cmtmus": "cmt-music",
	"revolt": "revolt",
	"rvlt":   "revolt",
	"fuse":   "fuse",
	// Sports networks
	"bigten":  "big-ten-network",
	"big10":   "big-ten-network",
	"btn":     "btn",
	"sec":     "sec-network",
	"secn":    "sec-network",
	"secnp":   "sec-network",
	"acc":     "espn-accn",
	"accn":    "espn-accn",
	"cbssn":   "cbs-sports-network",
	"fs1":     "fox-sports-1",
	"fs2":     "fox-sports-2",
	"fxdep":   "fox-sports-deportes",
	"golf":    "nbc-golf",
	"nbcgolf": "nbc-golf",
	"gsn":     "game-show-network",
	"marq":    "marquee-sports-network",
	"mlbn":    "mlb-network",
	"mlb":     "mlb-network",
	"mlbsz":   "mlb-network-strike-zone",
	"nbatv":   "nba-tv",
	"nba":     "nba-tv",
	"nhlnet":  "nhl-network",
	"nhl":     "nhl-network",
	"nflnet":  "nfl-network",
	"nfl":     "nfl-network",
	"nflnrz":  "nfl-red-zone",
	"redzone": "nfl-red-zone",
	"tennis":  "tennis-channel",
	"tenis":   "tennis-channel",
	"snla":    "spectrum-sportsnet-la",
	"specsn":  "spectrum-sportsnet",
	"willow":  "willow",
	"sprtman": "sportsman-channel",
	"cowboy":  "cowboy-channel",
	"rfdtv":   "rfd-tv",
	"outd":    "outdoor-channel",
	"out":     "outdoor-channel",
	"racer":   "racer-network",
	"pursuit": "pursuit",
	"purst":   "pursuit",
	"fduel":   "fanduel-tv",
	"fdueltv": "fanduel-tv",
	"fduelrc": "fanduel-racing",
	// Bloomberg / Misc news
	"bloom":   "bloomberg-television",
	"cnbcwld": "cnbc-world",
	// Cartoon / Kids
	"boom":   "boomerang",
	"toon":   "cartoon-network",
	"toonp":  "cartoon-network",
	"tnck":   "teen-nick",
	"nik":    "nick",
	"nicjr":  "nick-jr",
	"nikton": "nick-toons",
	"niktn":  "nick-toons",
	// Religious / Inspirational
	"byutv":   "byu-tv",
	"kdtx":    "tbn",
	"tbn":     "tbn",
	"sbn":     "sbn",
	"insp":    "insp",
	"daystar": "daystar",
	// Cars / Specialty
	"carstv": "cars-tv",
	"mt":     "motor-trend",
	"mthd":   "motor-trend",
	// Cinemax (base)
	"cmax":    "cinemax",
	"max":     "cinemax",
	"cin":     "cinemax",
	"cinr":    "cinemax",
	"cinhd":   "cinemax",
	"cinlus":  "cinemax-en-espanol",
	"cinact":  "cinemax-action",
	"cinacht": "cinemax-action",
	"cinach":  "cinemax-action",
	"cinacsp": "cinemax-en-espanol",
	"cinecls": "cinemax-classics",
	"cincl":   "cinemax-classics",
	"cinehit": "cinemax-hits",
	"cinehp":  "cinemax-hits",
	"cinehh":  "cinemax-hits",
	"cineh":   "cinemax-hits",
	"cinhh":   "cinemax-hits",
	"cinenos": "cinemax-outermax",
	"cineste": "cinemax-thrillermax",
	"cinete":  "cinemax-thrillermax",
	// Comedy / Cooking / Lifestyle
	"comedy": "comedy-central",
	"cmdytv": "comedy-central",
	"cmdtv":  "comedy-central",
	"cook":   "cooking-channel",
	"cozitv": "cozi-tv",
	"food":   "food-network",
	"hgtv":   "hgtv",
	"magn":   "magnolia-network",
	"recipe": "recipe-tv",
	"retro":  "retro-tv",
	"shorts": "shorts-tv",
	"shoplc": "shop-lc",
	"gems":   "gem-shopping-network",
	"qvc":    "qvc",
	"qvc2":   "qvc-2",
	"qvc3":   "qvc-3",
	"hsn":    "hsn",
	"hsn2":   "hsn-2",
	// C-SPAN
	"cspan":  "c-span-1",
	"cspan1": "c-span-1",
	"cspan2": "c-span-2",
	"cspan3": "c-span-3",
	// E! / ET
	"e":     "e-entertainment",
	"etstv": "et-live",
	"etnew": "et-live",
	// Freeform / Family
	"freefrm": "freeform",
	"frefm":   "freeform",
	"freefm":  "freeform",
	// Fox / FX
	"fx":  "fx",
	"fxx": "fxx",
	"fxm": "fxm-movie-channel",
	"fyi": "fyi",
	"fmc": "fmc-family-movie-classics",
	// Spanish/Latin
	"gala":      "galavision",
	"telemundo": "telemundo",
	"telen":     "telemundo",
	"unimas":    "unimas",
	"univision": "univision",
	"unvso":     "nbc-universo",
	"tudn":      "tudn",
	"tudnu":     "tudn",
	"tr3s":      "tres",
	// Hallmark
	"hall":  "hallmark-channel",
	"hmys":  "hallmark-mystery",
	"hallm": "hallmark-movies-now",
	// Heroes & Icons
	"hericns": "heroes-and-icons",
	"heroicn": "heroes-and-icons",
	// IFC / Indie
	"ifc": "ifc",
	// ION
	"kpxd": "ion-television",
	"ion":  "ion-television",
	// Laff / Bounce / Grit / Court
	"laff":   "laff",
	"bounce": "bounce",
	"grit":   "grit",
	"court":  "court-tv",
	// Lifetime
	"life":  "lifetime",
	"lmn":   "lifetime-movie-network",
	"lrw":   "lifetime-real-women",
	"women": "lifetime-real-women",
	// MGM+
	"mgm":    "mgm-plus",
	"mgmhit": "mgm-plus-hits",
	"mgmhth": "mgm-plus-hits",
	"mgmdrv": "mgm-plus-drive-in",
	"mgmmr":  "mgm-plus-marquee",
	"mgmw":   "mgm-plus",
	// Military
	"mil": "military-history",
	// Me-TV / MyNetwork
	"metvn": "me-tv",
	"metv":  "me-tv",
	"mnnt":  "my-network-tv",
	"mynet": "my-network-tv",
	// Movie Plex
	"mplex": "movie-plex",
	// National Geographic
	"ngc":     "national-geographic",
	"ngmundo": "nat-geo-mundo",
	"ngwild":  "nat-geo-wild",
	"ngwi":    "nat-geo-wild",
	// Ovation / OWN / Oxygen
	"ovatn": "ovation",
	"own":   "oprah-winfrey-network",
	"oxy":   "oxygen",
	"oxygn": "oxygen",
	// Paramount
	"par":       "paramount-network",
	"parsho":    "paramount-plus-with-showtime",
	"parshow":   "paramount-plus-with-showtime",
	"paramount": "paramount-plus",
	// Pets / Misc
	"petstv":  "pets-tv",
	"playboy": "playboy-tv",
	"play":    "playboy-tv",
	"positiv": "positiv",
	"postv":   "positiv",
	"pop":     "pop",
	// Showtime
	"sho":     "showtime",
	"sho2":    "showtime-2",
	"shocse":  "showtime-showcase",
	"shocs":   "showtime-showcase",
	"showx":   "showtime-extreme",
	"shobet":  "sho-bet",
	"shobeth": "sho-bet",
	"szeb":    "showtime-beyond",
	"szesu":   "showtime-showcase",
	// Smithsonian
	"smith": "smithsonian-channel",
	"smth":  "smithsonian-channel",
	"schn":  "smithsonian-channel",
	// Sony / Movie
	"sony":   "sony-movie-channel",
	"sonyhd": "sony-movie-channel",
	// Starz family
	"stz":     "starz",
	"stze":    "starz-edge",
	"stzk":    "starz-kids-and-family",
	"stzc":    "starz-cinema",
	"stzib":   "starz-in-black",
	"strzib":  "starz-in-black",
	"stzesp":  "starz-encore-espanol",
	"stzenc":  "starz-encore",
	"stzenbk": "starz-encore-black",
	"stzenac": "starz-encore-action",
	"stzensu": "starz-encore-suspense",
	"stzencl": "starz-encore-classic",
	"stzenws": "starz-encore-westerns",
	"stzenfm": "starz-encore-family",
	// Sundance
	"sundanc":  "sundance-tv",
	"sundance": "sundance-tv",
	"sund":     "sundance-tv",
	// TBS / TCM / TBN / TLC / TNT
	"tbs": "tbs",
	"tcm": "tcm",
	"tlc": "tlc",
	"tnt": "tnt",
	// TMC (The Movie Channel)
	"tmc":  "the-movie-channel",
	"tmcx": "the-movie-channel-xtra",
	// Travel
	"trav":   "travel-channel",
	"travel": "travel-channel",
	// TruTV
	"trutv": "tru-tv",
	// TV Land
	"tvland": "tv-land",
	"tvlnd":  "tv-land",
	// Up / USA
	"up":  "up-tv",
	"usa": "usa",
	// V-me / WE / Weather / WGN
	"vme":     "v-me",
	"vmekids": "vme-kids",
	"we":      "we-tv",
	"weath":   "weather-channel",
	"weather": "weather-channel",
	"wgn":     "wgn-america",
	// Bravo / Syfy
	"bravo": "bravo",
	"syfy":  "syfy",
	// HBO
	"hbo":    "hbo",
	"hbo2":   "hbo", // no dedicated hbo-2 logo upstream; fall back to HBO brand
	"hbocom": "hbo-comedy",
	"hbofam": "hbo-family",
	"hboltn": "hbo-latino",
	"hbosig": "hbo", // no dedicated hbo-signature logo upstream
	"hbozn":  "hbo", // no dedicated hbo-zone logo upstream
	// Gol TV / Misc
	"goltv":   "gol-tv",
	"goltve":  "gol-tv",
	"gol":     "gol-tv",
	"getv":    "get-tv",
	"gettv":   "get-tv",
	"local":   "local-now",
	"logo":    "logo",
	"hdnetmv": "hdnet-movies",
}

// helps split compound callsigns like "ESPNHD" → "espn".
var knownPrefixes = []string{
	"espn", "fox", "hbo", "cnn", "tbs", "tnt", "usa", "amc",
	"bet", "bravo", "mtv", "nick", "syfy", "tlc", "vh1",
	"food", "hgtv", "lifetime", "oxygen", "showtime", "starz",
}

type Client struct {
	http    *http.Client
	cache   *Cache
	mu      sync.Mutex
	last    time.Time
	country countryInfo
}

// creates a tv-logo client for the given Gracenote country code.
// Returns nil if the country is not supported.
func NewClient(gnCountry, cachePath string) *Client {
	ci, ok := countryMap[gnCountry]
	if !ok {
		return nil
	}
	return &Client{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache:   LoadCache(cachePath),
		country: ci,
	}
}

// saves the cache to disk.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.cache.Save()
}

// returns a verified logo URL for the channel, or "" if none found.
// Results are cached by channel ID.
func (c *Client) Resolve(channelID, callSign, affiliateName, channelNo string) string {
	if c == nil {
		return ""
	}

	key := "channelId:" + channelID
	if entry, ok := c.cache.Get(key); ok {
		return entry.LogoURL
	}

	candidates := c.generateCandidates(callSign, affiliateName, channelNo)

	logoURL := ""
	for _, slug := range candidates {
		u := baseRawURL + "/" + c.country.Dir + "/" + slug + c.country.Suffix + ".png"
		if c.checkURL(u) {
			logoURL = u
			break
		}
	}

	c.cache.Set(key, CacheEntry{LogoURL: logoURL})
	return logoURL
}

// returns an ordered list of slugs to try.
func (c *Client) generateCandidates(callSign, affiliateName, channelNo string) []string {
	seen := make(map[string]bool)
	var candidates []string

	add := func(slug string) {
		if slug != "" && !seen[slug] {
			seen[slug] = true
			candidates = append(candidates, slug)
		}
	}

	affiliate := strings.ToLower(strings.TrimSpace(affiliateName))
	call := strings.ToLower(strings.TrimSpace(callSign))
	bare := bareCallSign(call)

	// 1. Check affiliate alias table (handles long-form and irregular names)
	if alias, ok := affiliateAliases[affiliate]; ok {
		add(alias)
	}

	// 2. Callsign abbreviation map — covers the common case where the provider
	// sends a cryptic callsign with no affiliate name (e.g., "HISTORY" → "history-channel").
	// Try raw callsign first, then suffix-stripped form.
	if slug, ok := callsignSlugs[call]; ok {
		add(slug)
	}
	if bare != call {
		if slug, ok := callsignSlugs[bare]; ok {
			add(slug)
		}
	}

	// 3. Local-affiliate patterns — tried before the generic network slug so a
	// station's own logo wins over the plain network logo. These live in the
	// "us-local/" subdirectory of the tv-logos repo, e.g.
	// countries/united-states/us-local/abc-7-kabc-us.png.
	if network, ok := networkSlugs[affiliate]; ok && bare != "" {
		// {network}-{channelNo}-{callsign} — e.g. "abc-7-kabc"
		if channelNo != "" {
			add(localAffiliateDir + network + "-" + channelNo + "-" + bare)
		}
		// {network}-{callsign} — e.g. "abc-kota", "nbc-kdlt", "fox-wjzy"
		add(localAffiliateDir + network + "-" + bare)
	}

	// 4. Full affiliate slug — no stripping; matches "action-channel", "history-channel", etc.
	add(slugify(affiliate))

	// 5. Bare callsign alone — matches standalone entries like "wjxt", "wlny"
	add(bare)

	// 7. Affiliate without leading "the" — "The Weather Channel" → "weather-channel"
	if strings.HasPrefix(affiliate, "the ") {
		add(slugify(strings.TrimPrefix(affiliate, "the ")))
	}

	// 8. Normalized affiliate (strip noise words) — fallback for unusual long-form names
	add(normalizeAffiliate(affiliate))

	// 9. Known-prefix extraction for compound callsigns like "ESPNHD" → "espn"
	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(bare, prefix) && len(bare) > len(prefix) {
			add(prefix)
			break
		}
	}

	return candidates
}

// strips noise words from the affiliate name and slugifies.
func normalizeAffiliate(name string) string {
	words := strings.Fields(name)
	var kept []string
	for _, w := range words {
		if !noiseWords[w] {
			kept = append(kept, w)
		}
	}
	return slugify(strings.Join(kept, " "))
}

// returns the bare callsign with dash-separated and inline HD/SD/DT/Plus suffixes
// removed. Strips iteratively so compound suffixes like "HDP" or "HD2" collapse all the
// way (e.g., MAXHDP → MAXHD → MAX). Stops if the result would be shorter than 2 chars.
func bareCallSign(call string) string {
	s := dashSuffixRe.ReplaceAllString(call, "")
	for {
		next := hdSuffixRe.ReplaceAllString(s, "")
		if next == s || len(next) < 2 {
			break
		}
		s = next
	}
	return strings.Trim(s, "- ")
}

// converts a name to a URL-safe slug: lowercase, & → "and", spaces/punctuation to hyphens.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	prev := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prev = false
		} else if !prev {
			b.WriteByte('-')
			prev = true
		}
	}
	result := strings.Trim(b.String(), "-")
	return result
}

// sends an HTTP HEAD request and returns true if the server responds 200.
func (c *Client) checkURL(url string) bool {
	c.rateWait()

	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return false
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// enforces a minimum delay between HTTP requests.
func (c *Client) rateWait() {
	c.mu.Lock()
	defer c.mu.Unlock()

	since := time.Since(c.last)
	if since < rateDelay {
		time.Sleep(rateDelay - since)
	}
	c.last = time.Now()
}
