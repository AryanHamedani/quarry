package wp

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClientOptions configures a WordPress REST API Client.
type ClientOptions struct {
	Timeout         time.Duration
	UserAgent       string
	BearerToken     string
	BasicUser       string
	BasicAppPass    string
	CookieHeader    string // Raw Cookie header value (e.g. "wordpress_logged_in_...=...; wp-settings-1=...")
	WPNonce         string // Optional X-WP-Nonce for cookie-authenticated REST API requests
	MaxRetries      int
	RequestsPerSec  float64
	InsecureSkipTLS bool
	CustomHeaders   map[string]string
}

func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		Timeout:        25 * time.Second,
		UserAgent:      "Quarry/0.1.0 (+https://github.com/AryanHamedani/quarry; WordPress REST API Explorer & Extractor)",
		MaxRetries:     3,
		RequestsPerSec: 8.0,
	}
}

// Client is a resilient, auto-discovering WordPress REST API v2 client.
type Client struct {
	baseURL        string
	apiRoot        string // e.g., "https://example.com/wp-json" or "https://example.com/?rest_route="
	supportsPretty bool
	httpClient     *http.Client
	opts           ClientOptions

	rateMu   sync.Mutex
	lastTick time.Time
	minDelay time.Duration

	nonceMu           sync.Mutex
	resolvedNonce     string
	nonceProbedCookie string
}

// NormalizeSiteURL normalizes a user-provided domain or URL into a clean base origin/path.
func NormalizeSiteURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty site URL")
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host in URL %q", raw)
	}
	u.Fragment = ""
	// Strip trailing /wp-json or /wp-json/wp/v2 if user pasted an endpoint directly
	cleanPath := strings.TrimRight(u.Path, "/")
	if idx := strings.Index(cleanPath, "/wp-json"); idx != -1 {
		cleanPath = cleanPath[:idx]
	}
	u.Path = cleanPath
	u.RawQuery = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// NewClient creates a new WordPress REST API client for the given target URL.
func NewClient(rawURL string, opts ClientOptions) (*Client, error) {
	base, err := NormalizeSiteURL(rawURL)
	if err != nil {
		return nil, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 25 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultClientOptions().UserAgent
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 2
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	if opts.InsecureSkipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	var minDelay time.Duration
	if opts.RequestsPerSec > 0 {
		minDelay = time.Duration(float64(time.Second) / opts.RequestsPerSec)
	}

	return &Client{
		baseURL:        base,
		apiRoot:        base + "/wp-json",
		supportsPretty: true,
		httpClient: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
		},
		opts:     opts,
		minDelay: minDelay,
	}, nil
}

func (c *Client) BaseURL() string { return c.baseURL }
func (c *Client) APIRoot() string { return c.apiRoot }

// SetCookieHeader updates the live Cookie header value used for all subsequent requests.
func (c *Client) SetCookieHeader(rawCookie string) {
	norm := NormalizeCookieValue(rawCookie)
	c.rateMu.Lock()
	c.opts.CookieHeader = norm
	c.rateMu.Unlock()

	c.nonceMu.Lock()
	if c.nonceProbedCookie != norm {
		c.resolvedNonce = ""
		c.nonceProbedCookie = ""
	}
	c.nonceMu.Unlock()
}

// CookieHeader returns the normalized Cookie header value currently configured on the client.
func (c *Client) CookieHeader() string {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	return NormalizeCookieValue(c.opts.CookieHeader)
}

// EffectiveNonce returns either the explicitly configured WPNonce or the auto-discovered cookie REST nonce.
func (c *Client) EffectiveNonce() string {
	if explicit := strings.TrimSpace(c.opts.WPNonce); explicit != "" {
		return explicit
	}
	c.nonceMu.Lock()
	defer c.nonceMu.Unlock()
	return c.resolvedNonce
}

func (c *Client) waitRateLimit(ctx context.Context) error {
	if c.minDelay <= 0 {
		return nil
	}
	c.rateMu.Lock()
	now := time.Now()
	next := c.lastTick.Add(c.minDelay)
	var wait time.Duration
	if next.After(now) {
		wait = next.Sub(now)
		c.lastTick = next
	} else {
		c.lastTick = now
	}
	c.rateMu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

// BuildEndpointURL constructs the full URL for a given REST route (e.g. "/wp/v2/posts") and query params.
func (c *Client) BuildEndpointURL(route string, params url.Values) string {
	route = "/" + strings.TrimLeft(route, "/")
	if c.supportsPretty {
		base := strings.TrimRight(c.apiRoot, "/") + route
		if len(params) > 0 {
			return base + "?" + params.Encode()
		}
		return base
	}
	// Fallback ugly permalinks: https://example.com/?rest_route=/wp/v2/posts&per_page=25
	u, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return c.baseURL + "/?rest_route=" + route
	}
	q := u.Query()
	q.Set("rest_route", route)
	for k, vals := range params {
		for _, v := range vals {
			q.Add(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) decorateRequest(req *http.Request) {
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", "application/json")
	if c.opts.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.opts.BearerToken)
	} else if c.opts.BasicUser != "" || c.opts.BasicAppPass != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(c.opts.BasicUser + ":" + c.opts.BasicAppPass))
		req.Header.Set("Authorization", "Basic "+cred)
	}
	if cookie := c.CookieHeader(); cookie != "" {
		req.Header.Set("Cookie", cookie)
		if req.Header.Get("Referer") == "" {
			req.Header.Set("Referer", c.baseURL+"/")
		}
	}
	if nonce := c.EffectiveNonce(); nonce != "" {
		req.Header.Set("X-WP-Nonce", nonce)
	}
	for k, v := range c.opts.CustomHeaders {
		req.Header.Set(k, v)
	}
}

var (
	reValidNonce     = regexp.MustCompile(`^[a-zA-Z0-9]{6,32}$`)
	reHTMLRestNonce  = regexp.MustCompile(`(?i)(?:wpApiSettings|wcSettings)[^;]*?"nonce"\s*:\s*"([a-zA-Z0-9]{6,32})"`)
	reFetchMiddleware = regexp.MustCompile(`(?i)createNonceMiddleware\(\s*["']([a-zA-Z0-9]{6,32})["']\s*\)`)
)

// NormalizeCookieValue cleans a user-pasted Cookie header value, stripping any accidental leading "Cookie:" prefix,
// "-H" curl flags, or surrounding quotes.
func NormalizeCookieValue(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	if strings.HasPrefix(s, "-H ") || strings.HasPrefix(s, "--header ") {
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "-H "), "--header "))
	}
	s = strings.Trim(s, "\"'")
	if strings.HasPrefix(strings.ToLower(s), "cookie:") {
		s = strings.TrimSpace(s[len("cookie:"):])
	}
	return strings.Trim(s, "\"' ")
}

// ensureCookieNonce automatically exchanges a WordPress logged-in Cookie header for a `wp_rest`
// X-WP-Nonce token via `/wp-admin/admin-ajax.php?action=rest-nonce` (or HTML wpApiSettings fallback).
// WordPress Core requires X-WP-Nonce alongside `wordpress_logged_in_*` cookies to authenticate REST API calls.
func (c *Client) ensureCookieNonce(ctx context.Context) {
	cookie := c.CookieHeader()
	if cookie == "" || strings.TrimSpace(c.opts.WPNonce) != "" {
		return
	}
	c.nonceMu.Lock()
	if c.nonceProbedCookie == cookie {
		c.nonceMu.Unlock()
		return
	}
	c.nonceProbedCookie = cookie
	c.nonceMu.Unlock()

	// Only probe if the cookie actually contains a WordPress/WooCommerce session or auth token
	// (though probing admin-ajax.php?action=rest-nonce is lightweight and safe for any cookie).
	ajaxURL := c.baseURL + "/wp-admin/admin-ajax.php?action=rest-nonce"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ajaxURL, nil)
	if err == nil {
		req.Header.Set("User-Agent", c.opts.UserAgent)
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Referer", c.baseURL+"/")
		if resp, err := c.httpClient.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			candidate := strings.TrimSpace(string(body))
			if resp.StatusCode == http.StatusOK && candidate != "0" && candidate != "-1" && reValidNonce.MatchString(candidate) {
				c.nonceMu.Lock()
				c.resolvedNonce = candidate
				c.nonceMu.Unlock()
				return
			}
		}
	}

	// Fallback: inspect homepage / wp-admin HTML for localized wpApiSettings.nonce or createNonceMiddleware
	if strings.Contains(strings.ToLower(cookie), "wordpress_logged_in") {
		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
		if err == nil {
			req2.Header.Set("User-Agent", c.opts.UserAgent)
			req2.Header.Set("Cookie", cookie)
			if resp2, err := c.httpClient.Do(req2); err == nil {
				htmlBytes, _ := io.ReadAll(io.LimitReader(resp2.Body, 512*1024))
				_ = resp2.Body.Close()
				htmlStr := string(htmlBytes)
				if m := reHTMLRestNonce.FindStringSubmatch(htmlStr); len(m) > 1 {
					c.nonceMu.Lock()
					c.resolvedNonce = m[1]
					c.nonceMu.Unlock()
					return
				}
				if m := reFetchMiddleware.FindStringSubmatch(htmlStr); len(m) > 1 {
					c.nonceMu.Lock()
					c.resolvedNonce = m[1]
					c.nonceMu.Unlock()
					return
				}
			}
		}
	}
}

func (c *Client) doWithRetry(ctx context.Context, targetURL string) (*http.Response, time.Duration, error) {
	var lastErr error
	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(300*(1<<uint(attempt-1))) * time.Millisecond
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, 0, ctx.Err()
			case <-timer.C:
			}
		}

		if err := c.waitRateLimit(ctx); err != nil {
			return nil, 0, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return nil, 0, err
		}
		c.decorateRequest(req)

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		latency := time.Since(start)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
			retryAfter := resp.Header.Get("Retry-After")
			_ = resp.Body.Close()
			if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 && secs <= 15 {
				select {
				case <-ctx.Done():
					return nil, latency, ctx.Err()
				case <-time.After(time.Duration(secs) * time.Second):
				}
			}
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, targetURL)
			continue
		}

		return resp, latency, nil
	}
	return nil, 0, lastErr
}

var reAPILinkHeader = regexp.MustCompile(`<([^>]+)>;\s*rel=["']https://api\.w\.org/["']`)
var reHTMLAPILink = regexp.MustCompile(`(?i)<link[^>]+rel=["']https://api\.w\.org/["'][^>]+href=["']([^"']+)["']`)

// Discover probes the site to locate its WordPress REST API root, site metadata, custom post types, and taxonomies.
func (c *Client) Discover(ctx context.Context) (*SiteProfile, error) {
	c.ensureCookieNonce(ctx)
	// 1. Try standard /wp-json/
	prettyURL := c.baseURL + "/wp-json/"
	resp, latency, err := c.doWithRetry(ctx, prettyURL)
	if err == nil && resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
			if err == nil {
				if profile, err := c.parseRootDiscovery(body, prettyURL, true, latency); err == nil {
					c.apiRoot = strings.TrimRight(prettyURL, "/")
					c.supportsPretty = true
					c.enrichProfile(ctx, profile)
					return profile, nil
				}
			}
		}
	}

	// 2. Try ugly permalink fallback: /?rest_route=/
	uglyURL := c.baseURL + "/?rest_route=/"
	resp2, latency2, err2 := c.doWithRetry(ctx, uglyURL)
	if err2 == nil && resp2 != nil {
		defer resp2.Body.Close()
		if resp2.StatusCode == http.StatusOK {
			body, err := io.ReadAll(io.LimitReader(resp2.Body, 10*1024*1024))
			if err == nil {
				if profile, err := c.parseRootDiscovery(body, uglyURL, false, latency2); err == nil {
					c.apiRoot = uglyURL
					c.supportsPretty = false
					c.enrichProfile(ctx, profile)
					return profile, nil
				}
			}
		}
	}

	// 3. Try inspecting the homepage Link header or HTML <link rel="https://api.w.org/">
	resp3, latency3, err3 := c.doWithRetry(ctx, c.baseURL+"/")
	if err3 == nil && resp3 != nil {
		defer resp3.Body.Close()
		var discoveredRoot string
		for _, linkHdr := range resp3.Header.Values("Link") {
			if m := reAPILinkHeader.FindStringSubmatch(linkHdr); len(m) > 1 {
				discoveredRoot = m[1]
				break
			}
		}
		if discoveredRoot == "" {
			htmlBytes, _ := io.ReadAll(io.LimitReader(resp3.Body, 512*1024))
			if m := reHTMLAPILink.FindStringSubmatch(string(htmlBytes)); len(m) > 1 {
				discoveredRoot = m[1]
			}
		}
		if discoveredRoot != "" {
			resp4, latency4, err4 := c.doWithRetry(ctx, discoveredRoot)
			if err4 == nil && resp4 != nil {
				defer resp4.Body.Close()
				if resp4.StatusCode == http.StatusOK {
					body, _ := io.ReadAll(io.LimitReader(resp4.Body, 10*1024*1024))
					isPretty := !strings.Contains(discoveredRoot, "rest_route=")
					if profile, err := c.parseRootDiscovery(body, discoveredRoot, isPretty, latency3+latency4); err == nil {
						c.apiRoot = strings.TrimRight(discoveredRoot, "/")
						c.supportsPretty = isPretty
						c.enrichProfile(ctx, profile)
						return profile, nil
					}
				}
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to discover WordPress REST API at %s: %w", c.baseURL, err)
	}
	return nil, fmt.Errorf("no valid WordPress REST API found at %s (checked /wp-json/ and /?rest_route=/)", c.baseURL)
}

func (c *Client) parseRootDiscovery(body []byte, apiRootURL string, pretty bool, latency time.Duration) (*SiteProfile, error) {
	// Trim BOM if present
	body = []byte(strings.TrimPrefix(string(body), "\xef\xbb\xbf"))
	var profile SiteProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("invalid JSON from %s: %w", apiRootURL, err)
	}
	if len(profile.Namespaces) == 0 && profile.Name == "" && len(profile.Routes) == 0 {
		return nil, fmt.Errorf("JSON at %s is not a WordPress REST API index", apiRootURL)
	}
	if profile.URL == "" {
		profile.URL = c.baseURL
	}
	if profile.Name == "" {
		profile.Name = c.baseURL
	}
	profile.APIRootURL = apiRootURL
	profile.SupportsPretty = pretty
	profile.DiscoveredAt = time.Now().UTC()
	profile.ResponseLatency = latency
	profile.EntityCounts = make(map[string]int)
	return &profile, nil
}

func (c *Client) enrichProfile(ctx context.Context, profile *SiteProfile) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Discover Post Types
	wg.Add(1)
	go func() {
		defer wg.Done()
		var types FlexMap[PostType]
		if _, err := c.GetJSON(ctx, "/wp/v2/types", nil, &types); err == nil && len(types) > 0 {
			mu.Lock()
			profile.PostTypes = types
			mu.Unlock()
		}
	}()

	// Discover Taxonomies
	wg.Add(1)
	go func() {
		defer wg.Done()
		var taxes FlexMap[Taxonomy]
		if _, err := c.GetJSON(ctx, "/wp/v2/taxonomies", nil, &taxes); err == nil && len(taxes) > 0 {
			mu.Lock()
			profile.Taxonomies = taxes
			mu.Unlock()
		}
	}()

	// Probe counts for standard endpoints using per_page=1&_fields=id
	endpoints := []string{"posts", "pages", "media", "categories", "tags", "users", "comments"}
	for _, ep := range endpoints {
		ep := ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			params := url.Values{
				"per_page": {"1"},
				"_fields":  {"id"},
			}
			var dummy []any
			meta, err := c.GetJSON(ctx, "/wp/v2/"+ep, params, &dummy)
			if err == nil && meta.TotalItems >= 0 {
				mu.Lock()
				profile.EntityCounts[ep] = meta.TotalItems
				mu.Unlock()
			}
		}()
	}

	// Probe WooCommerce, Easy Digital Downloads (EDD), and Upload Directories
	wg.Add(1)
	go func() {
		defer wg.Done()
		dirs := c.ProbeDirectories(ctx)
		mu.Lock()
		profile.Directories = dirs
		for _, d := range dirs {
			if d.APIItemCount > 0 {
				profile.EntityCounts[d.Scope] = d.APIItemCount
			}
		}
		mu.Unlock()
	}()

	wg.Wait()
}

// GetJSON performs a GET request to a WordPress REST API route, decodes JSON into target, and extracts pagination headers.
func (c *Client) GetJSON(ctx context.Context, route string, params url.Values, target any) (PageMeta, error) {
	c.ensureCookieNonce(ctx)
	reqURL := c.BuildEndpointURL(route, params)
	resp, latency, err := c.doWithRetry(ctx, reqURL)
	if err != nil {
		return PageMeta{RequestURL: reqURL, Latency: latency}, err
	}
	defer resp.Body.Close()

	meta := PageMeta{
		Page:       1,
		PerPage:    25,
		Latency:    latency,
		RequestURL: reqURL,
	}
	if p := params.Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			meta.Page = v
		}
	}
	if pp := params.Get("per_page"); pp != "" {
		if v, err := strconv.Atoi(pp); err == nil {
			meta.PerPage = v
		}
	}
	if tot := resp.Header.Get("X-WP-Total"); tot != "" {
		if v, err := strconv.Atoi(tot); err == nil {
			meta.TotalItems = v
		}
	}
	if tp := resp.Header.Get("X-WP-TotalPages"); tp != "" {
		if v, err := strconv.Atoi(tp); err == nil {
			meta.TotalPages = v
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return meta, fmt.Errorf("reading response from %s: %w", reqURL, err)
	}
	body = []byte(strings.TrimPrefix(string(body), "\xef\xbb\xbf"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var wpErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &wpErr) == nil && wpErr.Code != "" {
			return meta, fmt.Errorf("WP API %s (%d): %s", wpErr.Code, resp.StatusCode, wpErr.Message)
		}
		return meta, fmt.Errorf("HTTP %d from %s", resp.StatusCode, reqURL)
	}

	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			return meta, fmt.Errorf("decoding JSON from %s: %w", reqURL, err)
		}
	}
	return meta, nil
}

// BuildQueryValues converts a QueryFilter into url.Values for the WordPress REST API.
func BuildQueryValues(q QueryFilter) url.Values {
	v := url.Values{}
	if q.PerPage > 0 {
		if q.PerPage > 100 {
			q.PerPage = 100
		}
		v.Set("per_page", strconv.Itoa(q.PerPage))
	} else {
		v.Set("per_page", "25")
	}
	if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	} else {
		v.Set("page", "1")
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	if strings.TrimSpace(q.Search) != "" {
		v.Set("search", strings.TrimSpace(q.Search))
	}
	if len(q.Author) > 0 {
		v.Set("author", joinInts(q.Author))
	}
	if len(q.Categories) > 0 {
		v.Set("categories", joinInts(q.Categories))
	}
	if len(q.Tags) > 0 {
		v.Set("tags", joinInts(q.Tags))
	}
	if q.Status != "" && q.Status != "publish" {
		v.Set("status", q.Status)
	}
	if q.After != "" {
		v.Set("after", normalizeISO8601Date(q.After, false))
	}
	if q.Before != "" {
		v.Set("before", normalizeISO8601Date(q.Before, true))
	}
	if q.OrderBy != "" {
		v.Set("orderby", q.OrderBy)
	}
	if q.Order != "" {
		v.Set("order", strings.ToLower(q.Order))
	}
	if q.Sticky != nil {
		v.Set("sticky", strconv.FormatBool(*q.Sticky))
	}
	if q.MimeType != "" {
		v.Set("mime_type", q.MimeType)
	}
	if q.MediaType != "" {
		v.Set("media_type", q.MediaType)
	}
	if q.Parent != nil {
		v.Set("parent", strconv.Itoa(*q.Parent))
	}
	if q.Slug != "" {
		v.Set("slug", q.Slug)
	}
	if len(q.Fields) > 0 {
		v.Set("_fields", strings.Join(q.Fields, ","))
	}
	if q.Embed {
		v.Set("_embed", "1")
	}
	return v
}

func normalizeISO8601Date(s string, endOfDay bool) string {
	s = strings.TrimSpace(s)
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		if endOfDay {
			return s + "T23:59:59"
		}
		return s + "T00:00:00"
	}
	return s
}

func joinInts(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// FetchPosts queries `/wp/v2/<endpoint>` (posts, pages, or custom post type) for a single page.
// For WooCommerce ("product"/"products") and Easy Digital Downloads ("download"/"downloads"/"edd-downloads"),
// if the `/wp/v2/` CPT route is disabled or 404s, it transparently falls back to `/wc/store/v1/products`, `/wp/v2/edd-downloads`, or `/?edd-api=products`.
func (c *Client) FetchPosts(ctx context.Context, q QueryFilter) ([]Post, PageMeta, error) {
	endpoint := strings.ToLower(strings.TrimSpace(q.Endpoint))
	if endpoint == "" {
		endpoint = "posts"
	}
	if endpoint == "products" {
		endpoint = "product"
	} else if endpoint == "downloads" {
		endpoint = "download"
	}
	route := "/wp/v2/" + strings.TrimLeft(endpoint, "/")
	var items FlexSlice[Post]
	meta, err := c.GetJSON(ctx, route, BuildQueryValues(q), &items)
	if err != nil {
		if endpoint == "product" {
			if wcPosts, wcMeta, wcErr := c.fetchWooCommerceStoreProductsAsPosts(ctx, q); wcErr == nil {
				return wcPosts, wcMeta, nil
			}
		} else if endpoint == "download" || endpoint == "edd-downloads" {
			altRoute := "/wp/v2/edd-downloads"
			if endpoint == "edd-downloads" {
				altRoute = "/wp/v2/download"
			}
			if altMeta, altErr := c.GetJSON(ctx, altRoute, BuildQueryValues(q), &items); altErr == nil {
				if altMeta.TotalPages == 0 && len(items) > 0 {
					altMeta.TotalPages = 1
					altMeta.TotalItems = len(items)
				}
				return items, altMeta, nil
			}
			if eddPosts, eddMeta, eddErr := c.fetchEDDProductsAsPosts(ctx, q); eddErr == nil {
				return eddPosts, eddMeta, nil
			}
		}
		return items, meta, err
	}
	if meta.TotalPages == 0 && len(items) > 0 {
		meta.TotalPages = 1
		meta.TotalItems = len(items)
	}
	return items, meta, nil
}

// FetchMedia queries `/wp/v2/media` for a single page (used in interactive TUI page-by-page browsing).
func (c *Client) FetchMedia(ctx context.Context, q QueryFilter) ([]MediaItem, PageMeta, error) {
	subQueries, matcher := ExpandMediaQueryPlan(q)

	if len(subQueries) == 1 {
		var rawItems FlexSlice[MediaItem]
		meta, err := c.GetJSON(ctx, "/wp/v2/media", BuildQueryValues(subQueries[0]), &rawItems)
		if err != nil {
			return nil, meta, err
		}
		items := []MediaItem(rawItems)
		if matcher != nil {
			filtered := make([]MediaItem, 0, len(items))
			for _, it := range items {
				if matcher(it) {
					filtered = append(filtered, it)
				}
			}
			items = filtered
		}
		if meta.TotalPages == 0 && len(items) > 0 {
			meta.TotalPages = 1
			meta.TotalItems = len(items)
		}
		return items, meta, nil
	}

	seen := make(map[int]struct{})
	var merged []MediaItem
	var aggMeta PageMeta
	aggMeta.Page = max(1, q.Page)
	aggMeta.PerPage = max(25, q.PerPage)

	for _, qSub := range subQueries {
		var subItems FlexSlice[MediaItem]
		meta, err := c.GetJSON(ctx, "/wp/v2/media", BuildQueryValues(qSub), &subItems)
		aggMeta.Latency += meta.Latency
		if aggMeta.RequestURL == "" {
			aggMeta.RequestURL = meta.RequestURL
		}
		if err != nil {
			continue
		}
		aggMeta.TotalItems += meta.TotalItems
		if meta.TotalPages > aggMeta.TotalPages {
			aggMeta.TotalPages = meta.TotalPages
		}
		for _, it := range subItems {
			if _, ok := seen[it.ID]; !ok {
				if matcher == nil || matcher(it) {
					seen[it.ID] = struct{}{}
					merged = append(merged, it)
				}
			}
		}
	}
	if aggMeta.TotalPages == 0 && len(merged) > 0 {
		aggMeta.TotalPages = 1
		aggMeta.TotalItems = len(merged)
	}
	return merged, aggMeta, nil
}

// FetchTerms queries `/wp/v2/categories`, `/wp/v2/tags`, or custom taxonomy rest_base.
func (c *Client) FetchTerms(ctx context.Context, q QueryFilter) ([]TermItem, PageMeta, error) {
	endpoint := q.Endpoint
	if endpoint == "" {
		endpoint = "categories"
	}
	q.Embed = false
	var items FlexSlice[TermItem]
	meta, err := c.GetJSON(ctx, "/wp/v2/"+strings.TrimLeft(endpoint, "/"), BuildQueryValues(q), &items)
	if meta.TotalPages == 0 && len(items) > 0 {
		meta.TotalPages = 1
		meta.TotalItems = len(items)
	}
	return items, meta, err
}

// FetchUsers queries `/wp/v2/users`.
func (c *Client) FetchUsers(ctx context.Context, q QueryFilter) ([]User, PageMeta, error) {
	q.Endpoint = "users"
	q.Embed = false
	var items FlexSlice[User]
	meta, err := c.GetJSON(ctx, "/wp/v2/users", BuildQueryValues(q), &items)
	if meta.TotalPages == 0 && len(items) > 0 {
		meta.TotalPages = 1
		meta.TotalItems = len(items)
	}
	return items, meta, err
}

// FetchComments queries `/wp/v2/comments`.
func (c *Client) FetchComments(ctx context.Context, q QueryFilter) ([]Comment, PageMeta, error) {
	q.Endpoint = "comments"
	q.Embed = false
	var items FlexSlice[Comment]
	meta, err := c.GetJSON(ctx, "/wp/v2/comments", BuildQueryValues(q), &items)
	if meta.TotalPages == 0 && len(items) > 0 {
		meta.TotalPages = 1
		meta.TotalItems = len(items)
	}
	return items, meta, err
}

// ProgressCallback is invoked after each page is fetched during bulk pagination.
type ProgressCallback func(page, totalPages, itemsFetched, totalItems int)

// FetchAllPosts automatically paginates through all pages matching QueryFilter.
func (c *Client) FetchAllPosts(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]Post, error) {
	if q.PerPage <= 0 || q.PerPage < 50 {
		q.PerPage = 100
	}
	q.Page = 1

	firstPage, meta, err := c.FetchPosts(ctx, q)
	if err != nil {
		return nil, err
	}
	all := make([]Post, 0, max(len(firstPage), meta.TotalItems))
	all = append(all, firstPage...)
	if onProgress != nil {
		onProgress(1, max(1, meta.TotalPages), len(all), max(len(all), meta.TotalItems))
	}
	if q.MaxItems > 0 && len(all) >= q.MaxItems {
		return all[:q.MaxItems], nil
	}

	totalPages := meta.TotalPages
	if q.MaxPages > 0 && q.MaxPages < totalPages {
		totalPages = q.MaxPages
	}
	for page := 2; page <= totalPages; page++ {
		q.Page = page
		items, _, err := c.FetchPosts(ctx, q)
		if err != nil {
			// Stop gracefully if WP returns 400 rest_post_invalid_page_number
			if strings.Contains(err.Error(), "rest_post_invalid_page_number") {
				break
			}
			return all, err
		}
		if len(items) == 0 {
			break
		}
		all = append(all, items...)
		if onProgress != nil {
			onProgress(page, totalPages, len(all), max(len(all), meta.TotalItems))
		}
		if q.MaxItems > 0 && len(all) >= q.MaxItems {
			return all[:q.MaxItems], nil
		}
	}
	return all, nil
}

// FetchAllMedia automatically paginates through all media items matching QueryFilter.
// Each expanded sub-query stream is paginated independently according to its own X-WP-TotalPages
// so we never send out-of-range page requests to streams with fewer pages.
func (c *Client) FetchAllMedia(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]MediaItem, error) {
	if q.PerPage <= 0 || q.PerPage < 50 {
		q.PerPage = 100
	}
	subQueries, matcher := ExpandMediaQueryPlan(q)

	seen := make(map[int]struct{})
	var all []MediaItem
	pagesDone := 0
	totalPages := 0
	totalExpectedItems := 0

	type streamState struct {
		sub        QueryFilter
		totalPages int
	}
	var multiPageStreams []streamState

	// Phase 1: Fetch page 1 of each sub-query stream to discover its exact pagination bounds
	var lastErr error
	succeededStreams := 0
	for i, sub := range subQueries {
		sub.Page = 1
		var items FlexSlice[MediaItem]
		meta, err := c.GetJSON(ctx, "/wp/v2/media", BuildQueryValues(sub), &items)
		if err != nil {
			lastErr = err
			if len(subQueries) == 1 {
				return nil, err
			}
			continue
		}
		succeededStreams++

		for _, it := range items {
			if _, ok := seen[it.ID]; !ok {
				if matcher == nil || matcher(it) {
					seen[it.ID] = struct{}{}
					all = append(all, it)
				}
			}
		}

		streamPages := meta.TotalPages
		if streamPages == 0 && len(items) > 0 {
			streamPages = 1
		}
		if q.MaxPages > 0 && streamPages > q.MaxPages {
			streamPages = q.MaxPages
		}

		if streamPages > 0 {
			pagesDone++
			totalPages += streamPages
			totalExpectedItems += max(len(items), meta.TotalItems)
		}
		if streamPages > 1 {
			multiPageStreams = append(multiPageStreams, streamState{
				sub:        sub,
				totalPages: streamPages,
			})
		}

		// Report progress as soon as single-stream or after probing streams
		if onProgress != nil && (len(subQueries) == 1 || i == len(subQueries)-1) {
			onProgress(max(1, pagesDone), max(1, totalPages), len(all), max(len(all), totalExpectedItems))
		}
		if q.MaxItems > 0 && len(all) >= q.MaxItems {
			return all[:q.MaxItems], nil
		}
	}

	if succeededStreams == 0 && lastErr != nil {
		return nil, lastErr
	}

	// Phase 2: Paginate remaining pages (2..stream.totalPages) only for streams that actually have them
	for _, st := range multiPageStreams {
		for page := 2; page <= st.totalPages; page++ {
			st.sub.Page = page
			var items FlexSlice[MediaItem]
			_, err := c.GetJSON(ctx, "/wp/v2/media", BuildQueryValues(st.sub), &items)
			if err != nil {
				if strings.Contains(err.Error(), "rest_post_invalid_page_number") {
					break
				}
				if len(subQueries) == 1 {
					return all, err
				}
				break
			}
			if len(items) == 0 {
				break
			}
			for _, it := range items {
				if _, ok := seen[it.ID]; !ok {
					if matcher == nil || matcher(it) {
						seen[it.ID] = struct{}{}
						all = append(all, it)
					}
				}
			}
			pagesDone++
			if onProgress != nil {
				onProgress(pagesDone, max(pagesDone, totalPages), len(all), max(len(all), totalExpectedItems))
			}
			if q.MaxItems > 0 && len(all) >= q.MaxItems {
				return all[:q.MaxItems], nil
			}
		}
	}

	return all, nil
}

// FetchAllTerms automatically paginates through all categories/tags matching QueryFilter.
func (c *Client) FetchAllTerms(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]TermItem, error) {
	if q.PerPage <= 0 || q.PerPage < 50 {
		q.PerPage = 100
	}
	q.Page = 1

	firstPage, meta, err := c.FetchTerms(ctx, q)
	if err != nil {
		return nil, err
	}
	all := make([]TermItem, 0, max(len(firstPage), meta.TotalItems))
	all = append(all, firstPage...)
	if onProgress != nil {
		onProgress(1, max(1, meta.TotalPages), len(all), max(len(all), meta.TotalItems))
	}
	for page := 2; page <= meta.TotalPages; page++ {
		q.Page = page
		items, _, err := c.FetchTerms(ctx, q)
		if err != nil || len(items) == 0 {
			break
		}
		all = append(all, items...)
		if onProgress != nil {
			onProgress(page, meta.TotalPages, len(all), max(len(all), meta.TotalItems))
		}
	}
	return all, nil
}

// FetchAllUsers automatically paginates through all public users.
func (c *Client) FetchAllUsers(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]User, error) {
	if q.PerPage <= 0 || q.PerPage < 50 {
		q.PerPage = 100
	}
	q.Page = 1

	firstPage, meta, err := c.FetchUsers(ctx, q)
	if err != nil {
		return nil, err
	}
	all := make([]User, 0, max(len(firstPage), meta.TotalItems))
	all = append(all, firstPage...)
	if onProgress != nil {
		onProgress(1, max(1, meta.TotalPages), len(all), max(len(all), meta.TotalItems))
	}
	for page := 2; page <= meta.TotalPages; page++ {
		q.Page = page
		items, _, err := c.FetchUsers(ctx, q)
		if err != nil || len(items) == 0 {
			break
		}
		all = append(all, items...)
		if onProgress != nil {
			onProgress(page, meta.TotalPages, len(all), max(len(all), meta.TotalItems))
		}
	}
	return all, nil
}

// FetchAllComments automatically paginates through all public comments.
func (c *Client) FetchAllComments(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]Comment, error) {
	if q.PerPage <= 0 || q.PerPage < 50 {
		q.PerPage = 100
	}
	q.Page = 1

	firstPage, meta, err := c.FetchComments(ctx, q)
	if err != nil {
		return nil, err
	}
	all := make([]Comment, 0, max(len(firstPage), meta.TotalItems))
	all = append(all, firstPage...)
	if onProgress != nil {
		onProgress(1, max(1, meta.TotalPages), len(all), max(len(all), meta.TotalItems))
	}
	totalPages := meta.TotalPages
	if q.MaxPages > 0 && q.MaxPages < totalPages {
		totalPages = q.MaxPages
	}
	for page := 2; page <= totalPages; page++ {
		q.Page = page
		items, _, err := c.FetchComments(ctx, q)
		if err != nil || len(items) == 0 {
			break
		}
		all = append(all, items...)
		if onProgress != nil {
			onProgress(page, totalPages, len(all), max(len(all), meta.TotalItems))
		}
		if q.MaxItems > 0 && len(all) >= q.MaxItems {
			return all[:q.MaxItems], nil
		}
	}
	return all, nil
}
