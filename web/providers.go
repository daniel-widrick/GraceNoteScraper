package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const providerBaseURL = "https://tvlistings.gracenote.com/gapzap_webapi/api"

// Provider describes one Gracenote lineup returned for a postal code.
type Provider struct {
	Type              string `json:"type"`
	Device            string `json:"device"`
	LineupID          string `json:"lineupId"`
	Name              string `json:"name"`
	Location          string `json:"location"`
	Timezone          string `json:"timezone"`
	IsDefaultProvider string `json:"isDefaultProvider"`
	PostalCode        string `json:"postalCode"`
	HeadendID         string `json:"headendId"`
	ChannelCount      int    `json:"channelCount,omitempty"`
	ChannelCountKnown bool   `json:"channelCountKnown,omitempty"`
}

// ProviderResponse is the public provider-discovery response used by the
// Gracenote listings website.
type ProviderResponse struct {
	DSTUTCOffset string     `json:"DSTUTCOffset"`
	StdUTCOffset string     `json:"StdUTCOffset"`
	DSTEnd       string     `json:"DSTEnd"`
	DSTStart     string     `json:"DSTStart"`
	PrimeTime    string     `json:"primetime"`
	Providers    []Provider `json:"Providers"`
}

// ProviderClient retrieves the lineups available for a postal code.
type ProviderClient struct {
	httpClient *http.Client
	baseURL    string
}

func NewProviderClient() *ProviderClient {
	return &ProviderClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    providerBaseURL,
	}
}

func newProviderClient(httpClient *http.Client, baseURL string) *ProviderClient {
	return &ProviderClient{httpClient: httpClient, baseURL: strings.TrimRight(baseURL, "/")}
}

func (c *ProviderClient) FindProviders(ctx context.Context, country, postalCode, language string) (*ProviderResponse, error) {
	endpoint, err := url.JoinPath(
		c.baseURL,
		"Providers",
		"getPostalCodeProviders",
		strings.ToUpper(country),
		strings.ToUpper(postalCode),
		"gapzap",
		strings.ToLower(language),
	)
	if err != nil {
		return nil, fmt.Errorf("building provider lookup URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building provider lookup request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://tvlistings.gracenote.com/grid-affiliates.html?aid=gapzap")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider lookup failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("provider lookup returned %s", resp.Status)
	}

	var result ProviderResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding provider lookup: %w", err)
	}
	if result.Providers == nil {
		result.Providers = []Provider{}
	}
	return &result, nil
}
