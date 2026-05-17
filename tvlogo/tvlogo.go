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
	"national broadcasting company":               "nbc",
	"american broadcasting company":               "abc",
	"cbs television network":                      "cbs",
	"fox entertainment":                           "fox",
	"fox broadcasting":                            "fox",
	"fox broadcasting company":                    "fox",
	"turner network television":                   "tnt",
	"entertainment and sports programming network": "espn",
	"cable news network":                          "cnn",
	"the weather channel":                         "weather-channel",
	"comedy central":                              "comedy-central",
	"cartoon network":                             "cartoon-network",
	"animal planet":                               "animal-planet",
	"public broadcasting service":                 "pbs",
	"cable-satellite public affairs network":      "c-span",
	"turner classic movies":                       "tcm",
	"american movie classics":                     "amc",
	"freeform":                                    "freeform",
	"fx networks":                                 "fx",
	"investigation discovery":                     "investigation-discovery",
	"oprah winfrey network":                       "oprah-winfrey-network",
	"a and e":                                     "a-and-e",
	"a&e":                                         "a-and-e",
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

// matches common HD/SD/DT suffixes on callsigns (inline, e.g. "ESPNHD").
var hdSuffixRe = regexp.MustCompile(`(?i)(hd|sd|dt|hd2|hd3|hd4)$`)

// matches dash-separated station suffixes like -TV, -DT, -HD, -LD, -DT2.
var dashSuffixRe = regexp.MustCompile(`(?i)-(tv|dt|hd|ld|dt2|hd2|hd3)$`)

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

	// 1. Check alias table first (handles long-form and irregular names)
	if alias, ok := affiliateAliases[affiliate]; ok {
		add(alias)
	}

	// 2. Full affiliate slug — no stripping; matches "action-channel", "history-channel", etc.
	add(slugify(affiliate))

	// 3. {network}-{channelNo}-{callsign} — matches local affiliate logos like "abc-7-kabc"
	bare := bareCallSign(call)
	if network, ok := networkSlugs[affiliate]; ok && bare != "" {
		if channelNo != "" {
			add(network + "-" + channelNo + "-" + bare)
		}
		// 4. {network}-{callsign} — matches "abc-kota", "nbc-kdlt", "fox-wjzy", etc.
		add(network + "-" + bare)
	}

	// 5. Bare callsign alone — matches standalone entries like "wjxt", "wlny"
	add(bare)

	// 6. Affiliate without leading "the" — "The Weather Channel" → "weather-channel"
	if strings.HasPrefix(affiliate, "the ") {
		add(slugify(strings.TrimPrefix(affiliate, "the ")))
	}

	// 7. Normalized affiliate (strip noise words) — fallback for unusual long-form names
	add(normalizeAffiliate(affiliate))

	// 8. Known-prefix extraction for compound callsigns like "ESPNHD" → "espn"
	stripped := hdSuffixRe.ReplaceAllString(call, "")
	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(stripped, prefix) && len(stripped) > len(prefix) {
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

// returns the bare callsign with dash-separated and inline HD/SD/DT suffixes removed.
func bareCallSign(call string) string {
	s := dashSuffixRe.ReplaceAllString(call, "")
	s = hdSuffixRe.ReplaceAllString(s, "")
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
