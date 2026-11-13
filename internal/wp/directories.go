package wp

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DirectoryScope identifies a target WordPress upload directory or e-commerce plugin ecosystem.
type DirectoryScope string

const (
	ScopeWooCommerce DirectoryScope = "woocommerce" // /wp-content/uploads/woocommerce_uploads/ + WC REST APIs
	ScopeEDD         DirectoryScope = "edd"         // /wp-content/uploads/edd/ + EDD REST APIs
	ScopeUploads     DirectoryScope = "uploads"     // /wp-content/uploads/ general directory tree
)

// NormalizeDirectoryScopes parses user-supplied scope tokens (e.g. "wc,edd", "woocommerce", "all")
// into canonical DirectoryScope values. Defaults to WooCommerce + EDD if empty or "all".
func NormalizeDirectoryScopes(raw []string) []DirectoryScope {
	seen := make(map[DirectoryScope]struct{})
	var out []DirectoryScope
	add := func(s DirectoryScope) {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}

	for _, r := range raw {
		for _, tok := range strings.Split(r, ",") {
			t := strings.ToLower(strings.TrimSpace(tok))
			switch t {
			case "", "all", "both", "commerce", "ecommerce":
				add(ScopeWooCommerce)
				add(ScopeEDD)
				if t == "all" {
					add(ScopeUploads)
				}
			case "wc", "woo", "woocommerce", "woocommerce_uploads":
				add(ScopeWooCommerce)
			case "edd", "easydigitaldownloads", "easy-digital-downloads":
				add(ScopeEDD)
			case "uploads", "wp-content", "files":
				add(ScopeUploads)
			}
		}
	}
	if len(out) == 0 {
		return []DirectoryScope{ScopeWooCommerce, ScopeEDD}
	}
	return out
}

// MatchesDirectoryScope returns true if a media URL, relative file path, or attachment metadata/link belongs to any of the requested scopes.
func MatchesDirectoryScope(sourceURL, relFile string, mediaType string, scopes []DirectoryScope) bool {
	if len(scopes) == 0 {
		return true
	}
	u := strings.ToLower(sourceURL)
	f := strings.ToLower(relFile)
	mt := strings.ToLower(mediaType)

	for _, sc := range scopes {
		switch sc {
		case ScopeWooCommerce:
			if strings.Contains(u, "/woocommerce_uploads/") ||
				strings.Contains(u, "download_file=") ||
				strings.HasPrefix(f, "woocommerce_uploads/") ||
				strings.Contains(mt, "woocommerce") ||
				strings.Contains(mt, "wc-") ||
				strings.Contains(mt, "/product/") {
				return true
			}
		case ScopeEDD:
			if strings.Contains(u, "/uploads/edd/") ||
				strings.Contains(u, "eddfile=") ||
				strings.Contains(u, "edd_action=process_download") ||
				strings.HasPrefix(f, "edd/") ||
				strings.Contains(mt, "edd") ||
				strings.Contains(mt, "/downloads/") {
				return true
			}
		case ScopeUploads:
			if strings.Contains(u, "/wp-content/uploads/") || f != "" {
				return true
			}
		}
	}
	return false
}

var (
	reAutoIndexTitle = regexp.MustCompile(`(?i)<title>\s*Index of\b|<h1>\s*Index of\b`)
	reAnchorHref     = regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reRowDateISO     = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})(?:\s+(\d{2}:\d{2}(?::\d{2})?))?\b`)
	reRowDateApache  = regexp.MustCompile(`(?i)\b(\d{1,2}-[A-Za-z]{3}-20\d{2})(?:\s+(\d{2}:\d{2}(?::\d{2})?))?\b`)
	reRowSize        = regexp.MustCompile(`(?i)(?:^|\s)(\d+(?:\.\d+)?)\s*([KMGT]i?B?|[KMGT])(?:\s|$)`)
	rePathYearMonth  = regexp.MustCompile(`/(20\d{2})/(0[1-9]|1[0-2])/`)
	reDownloadLinks  = regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']*(?:download_file=|eddfile=|edd_action=process_download|/woocommerce_uploads/|/uploads/edd/)[^"']*)["'][^>]*>(.*?)</a>`)
)

// ProbeDirectories checks the target WordPress site for WooCommerce (`woocommerce_uploads` + WC APIs),
// Easy Digital Downloads (`edd` + EDD APIs), and general `/wp-content/uploads/` directory indexing.
func (c *Client) ProbeDirectories(ctx context.Context) []DirectoryProbeStatus {
	specs := []struct {
		scope   DirectoryScope
		relPath string
	}{
		{ScopeWooCommerce, "/wp-content/uploads/woocommerce_uploads/"},
		{ScopeEDD, "/wp-content/uploads/edd/"},
		{ScopeUploads, "/wp-content/uploads/"},
	}

	statuses := make([]DirectoryProbeStatus, len(specs))
	var wg sync.WaitGroup

	for i, sp := range specs {
		i, sp := i, sp
		wg.Add(1)
		go func() {
			defer wg.Done()
			targetURL := c.baseURL + sp.relPath
			st := DirectoryProbeStatus{
				Scope: string(sp.scope),
				Path:  sp.relPath,
				URL:   targetURL,
			}

			// 1. Probe HTTP directory listing
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
			if err == nil {
				c.decorateRequest(req)
				req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")
				if resp, err := c.httpClient.Do(req); err == nil {
					st.HTTPStatus = resp.StatusCode
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK && isAutoIndexHTML(string(body)) {
						st.AutoIndex = true
					}
				}
			}

			// 2. Probe corresponding REST API endpoints
			switch sp.scope {
			case ScopeWooCommerce:
				var wcProducts []any
				meta, err := c.GetJSON(ctx, "/wc/store/v1/products", url.Values{"per_page": {"1"}}, &wcProducts)
				if err == nil && (meta.TotalItems > 0 || len(wcProducts) > 0) {
					st.APIAvailable = true
					st.APIEndpoint = "/wc/store/v1/products"
					st.APIItemCount = max(meta.TotalItems, len(wcProducts))
				} else {
					meta2, err2 := c.GetJSON(ctx, "/wp/v2/product", url.Values{"per_page": {"1"}, "_fields": {"id"}}, &wcProducts)
					if err2 == nil && (meta2.TotalItems > 0 || len(wcProducts) > 0) {
						st.APIAvailable = true
						st.APIEndpoint = "/wp/v2/product"
						st.APIItemCount = max(meta2.TotalItems, len(wcProducts))
					} else {
						meta3, err3 := c.GetJSON(ctx, "/wc/v3/products", url.Values{"per_page": {"1"}}, &wcProducts)
						if err3 == nil && (meta3.TotalItems > 0 || len(wcProducts) > 0) {
							st.APIAvailable = true
							st.APIEndpoint = "/wc/v3/products"
							st.APIItemCount = max(meta3.TotalItems, len(wcProducts))
						}
					}
				}

			case ScopeEDD:
				var eddItems []any
				meta, err := c.GetJSON(ctx, "/wp/v2/edd-downloads", url.Values{"per_page": {"1"}, "_fields": {"id"}}, &eddItems)
				if err == nil && (meta.TotalItems > 0 || len(eddItems) > 0) {
					st.APIAvailable = true
					st.APIEndpoint = "/wp/v2/edd-downloads"
					st.APIItemCount = max(meta.TotalItems, len(eddItems))
				} else {
					meta2, err2 := c.GetJSON(ctx, "/wp/v2/download", url.Values{"per_page": {"1"}, "_fields": {"id"}}, &eddItems)
					if err2 == nil && (meta2.TotalItems > 0 || len(eddItems) > 0) {
						st.APIAvailable = true
						st.APIEndpoint = "/wp/v2/download"
						st.APIItemCount = max(meta2.TotalItems, len(eddItems))
					} else if cnt, ok := c.probeEDDNativeAPI(ctx); ok {
						st.APIAvailable = true
						st.APIEndpoint = "/?edd-api=products"
						st.APIItemCount = cnt
					}
				}
			}

			statuses[i] = st
		}()
	}

	wg.Wait()
	return statuses
}

func (c *Client) probeEDDNativeAPI(ctx context.Context) (int, bool) {
	u := c.baseURL + "/?edd-api=products&number=1"
	resp, _, err := c.doWithRetry(ctx, u)
	if err != nil || resp == nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return 0, false
	}
	var payload struct {
		Products FlexSlice[any] `json:"products"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Products) == 0 {
		return 0, false
	}
	return len(payload.Products), true
}

// ScanDirectories indexes and searches files across WooCommerce (`woocommerce_uploads` + WC APIs),
// Easy Digital Downloads (`edd` + EDD APIs), and `/wp-content/uploads/` directories.
// It combines:
//  1. Recursive HTTP autoindex directory crawling (`/wp-content/uploads/woocommerce_uploads/`, `/wp-content/uploads/edd/`, `/wp-content/uploads/`, and active `YYYY/MM/` subdirectories)
//  2. WooCommerce REST API product & downloadable file harvesting (`/wc/store/v1/products`, `/wc/v3/products`, `/wp/v2/product`) + Cookie-authenticated `/my-account/downloads/`
//  3. Easy Digital Downloads REST API harvesting (`/wp/v2/download`, `/?edd-api=products`)
//  4. Targeted WordPress Media Library (`/wp/v2/media`) queries matching `_wp_attached_file` paths (`woocommerce_uploads`, `edd`)
func (c *Client) ScanDirectories(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]MediaItem, PageMeta, error) {
	start := time.Now()
	c.ensureCookieNonce(ctx)
	scopes := NormalizeDirectoryScopes(q.DirectoryScopes)

	var (
		mu                sync.Mutex
		seenURL           = make(map[string]int) // SourceURL -> index in results
		results           []MediaItem
		mediaLibraryItems []MediaItem
		wcActive          bool
		eddActive         bool
	)

	addEntry := func(item MediaItem) {
		u := strings.TrimSpace(item.SourceURL)
		if u == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if idx, exists := seenURL[u]; exists {
			// Enrich existing entry with known FileSize / ID if the new item has richer metadata from /wp/v2/media
			if results[idx].MediaDetails.FileSize == 0 && item.MediaDetails.FileSize > 0 {
				results[idx].MediaDetails.FileSize = item.MediaDetails.FileSize
			}
			if results[idx].MediaDetails.File == "" && item.MediaDetails.File != "" {
				results[idx].MediaDetails.File = item.MediaDetails.File
			}
			return
		}
		seenURL[u] = len(results)
		results = append(results, item)
	}

	yearMonths := c.discoverActiveYearMonths(ctx)

	var wg sync.WaitGroup
	hasWC, hasEDD, hasUploads := false, false, false
	for _, sc := range scopes {
		switch sc {
		case ScopeWooCommerce:
			hasWC = true
		case ScopeEDD:
			hasEDD = true
		case ScopeUploads:
			hasUploads = true
		}
	}

	// 1. Crawl HTTP Autoindex Directories for each requested scope
	for _, sc := range scopes {
		sc := sc
		wg.Add(1)
		go func() {
			defer wg.Done()
			var baseRel string
			switch sc {
			case ScopeWooCommerce:
				baseRel = "/wp-content/uploads/woocommerce_uploads/"
			case ScopeEDD:
				baseRel = "/wp-content/uploads/edd/"
			case ScopeUploads:
				baseRel = "/wp-content/uploads/"
			}
			if baseRel == "" {
				return
			}
			crawled := c.crawlUploadDirectoryTree(ctx, baseRel, string(sc), yearMonths, 3)
			for _, it := range crawled {
				addEntry(it)
			}
		}()
	}

	// 2. Harvest WooCommerce REST APIs + Customer Downloads if ScopeWooCommerce is active
	if hasWC {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wcItems := c.harvestWooCommerceAssets(ctx, q)
			if len(wcItems) > 0 {
				mu.Lock()
				wcActive = true
				mu.Unlock()
			}
			for _, it := range wcItems {
				addEntry(it)
			}
		}()
	}

	// 3. Harvest Easy Digital Downloads REST APIs if ScopeEDD is active
	if hasEDD {
		wg.Add(1)
		go func() {
			defer wg.Done()
			eddItems := c.harvestEDDAssets(ctx, q)
			if len(eddItems) > 0 {
				mu.Lock()
				eddActive = true
				mu.Unlock()
			}
			for _, it := range eddItems {
				addEntry(it)
			}
		}()
	}

	// 4. Query /wp/v2/media and match attachments belonging to the requested scopes
	//    (via `/uploads/edd/`, `/woocommerce_uploads/`, `media_details.file`, or attachment permalink `/downloads/...` / `/product/...`).
	//    Note: WordPress Core's public `/wp/v2/media?search=` only searches post_title/post_excerpt/post_content
	//    (not `_wp_attached_file` or `source_url`), so we never overwrite `q.Search` with directory names.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var mediaQueries []QueryFilter
		hasExplicitTypeFilter := strings.TrimSpace(q.MediaType) != "" ||
			strings.TrimSpace(q.MimeType) != "" ||
			len(q.Extensions) > 0 ||
			len(q.ExcludeMediaTypes) > 0 ||
			len(q.ExcludeExtensions) > 0

		if !hasUploads && !hasExplicitTypeFilter {
			// 4a. Fetch ALL non-image downloadable attachments (application, file, text, video, audio) across all pages
			mqNonImg := q
			mqNonImg.Endpoint = "media"
			mqNonImg.DirectoryScopes = nil
			mqNonImg.ExcludeMediaTypes = []string{"image"}
			mediaQueries = append(mediaQueries, mqNonImg)

			// 4b. Fetch recent image attachments (bounded to 2 pages / 200 items by default unless --max-pages is set)
			mqImg := q
			mqImg.Endpoint = "media"
			mqImg.DirectoryScopes = nil
			mqImg.MediaType = "image"
			if mqImg.MaxPages <= 0 {
				mqImg.MaxPages = 2
			}
			mediaQueries = append(mediaQueries, mqImg)
		} else {
			mq := q
			mq.Endpoint = "media"
			mq.DirectoryScopes = nil
			mediaQueries = append(mediaQueries, mq)
		}

		var fetched []MediaItem
		for _, mq := range mediaQueries {
			items, err := c.FetchAllMedia(ctx, mq, nil)
			if err != nil {
				continue
			}
			fetched = append(fetched, items...)
		}
		mu.Lock()
		mediaLibraryItems = fetched
		mu.Unlock()
	}()

	wg.Wait()

	// Merge and filter media library items now that we know whether WooCommerce/EDD APIs are active on this site
	for _, it := range mediaLibraryItems {
		metaContext := it.MediaType + " " + it.Type + " " + it.AltText + " " + it.Link
		matchedScope := MatchesDirectoryScope(it.SourceURL, it.MediaDetails.File, metaContext, scopes)
		if !matchedScope && (eddActive || wcActive) {
			// On active EDD or WooCommerce stores, downloadable document/archive/spreadsheet attachments
			// (`application`, `text`, `file` — e.g. .xlsx, .xls, .zip, .csv, .pdf, .rar) uploaded via the
			// Media Library are store downloadable/sample files even when stored in `/wp-content/uploads/YYYY/MM/`.
			mtLower := strings.ToLower(strings.TrimSpace(it.MediaType))
			if mtLower == "application" || mtLower == "text" || mtLower == "file" {
				matchedScope = true
			}
		}
		if matchedScope {
			uLower := strings.ToLower(it.SourceURL)
			linkLower := strings.ToLower(it.Link)
			if strings.Contains(uLower, "/woocommerce_uploads/") || strings.HasPrefix(strings.ToLower(it.MediaDetails.File), "woocommerce_uploads/") {
				it.AltText = "[woocommerce_uploads] " + it.AltText
			} else if strings.Contains(linkLower, "/product/") || wcActive {
				it.AltText = "[woocommerce] " + it.AltText
			} else if strings.Contains(uLower, "/uploads/edd/") || strings.HasPrefix(strings.ToLower(it.MediaDetails.File), "edd/") || strings.Contains(linkLower, "/downloads/") || eddActive {
				it.AltText = "[edd] " + it.AltText
			}
			addEntry(it)
		} else {
			// Even if not a new scope match, enrich any already-discovered product asset with its known FileSize
			u := strings.TrimSpace(it.SourceURL)
			if idx, exists := seenURL[u]; exists && results[idx].MediaDetails.FileSize == 0 && it.MediaDetails.FileSize > 0 {
				results[idx].MediaDetails.FileSize = it.MediaDetails.FileSize
			}
		}
	}

	// Parse optional date bounds (--after / --before)
	var afterTime, beforeTime time.Time
	if q.After != "" {
		if t, err := time.Parse("2006-01-02T15:04:05", normalizeISO8601Date(q.After, false)); err == nil {
			afterTime = t
		}
	}
	if q.Before != "" {
		if t, err := time.Parse("2006-01-02T15:04:05", normalizeISO8601Date(q.Before, true)); err == nil {
			beforeTime = t
		}
	}

	// Filter results using Quarry's unified media & directory filter
	filtered := make([]MediaItem, 0, len(results))
	searchLower := strings.ToLower(strings.TrimSpace(q.Search))
	for _, item := range results {
		if searchLower != "" {
			haystack := strings.ToLower(item.Title.Rendered + " " + item.Slug + " " + item.SourceURL + " " + item.AltText + " " + item.Caption.Rendered + " " + item.MediaDetails.File)
			if !strings.Contains(haystack, searchLower) {
				continue
			}
		}
		if !afterTime.IsZero() && !item.Date.Time.IsZero() && item.Date.Time.Before(afterTime) {
			continue
		}
		if !beforeTime.IsZero() && !item.Date.Time.IsZero() && item.Date.Time.After(beforeTime) {
			continue
		}
		if !MatchesMediaFilter(item, q) {
			continue
		}
		filtered = append(filtered, item)
	}

	// Sort deterministically according to q.OrderBy and q.Order
	asc := strings.ToLower(q.Order) == "asc"
	orderBy := strings.ToLower(strings.TrimSpace(q.OrderBy))
	sort.Slice(filtered, func(i, j int) bool {
		var cmp int
		switch orderBy {
		case "title", "name":
			cmp = strings.Compare(strings.ToLower(filtered[i].Title.Rendered), strings.ToLower(filtered[j].Title.Rendered))
		case "slug":
			cmp = strings.Compare(strings.ToLower(filtered[i].Slug), strings.ToLower(filtered[j].Slug))
		case "id":
			cmp = filtered[i].ID - filtered[j].ID
		case "size", "filesize":
			if filtered[i].MediaDetails.FileSize != filtered[j].MediaDetails.FileSize {
				if filtered[i].MediaDetails.FileSize < filtered[j].MediaDetails.FileSize {
					cmp = -1
				} else {
					cmp = 1
				}
			}
		default: // "date", "modified"
			if !filtered[i].Date.Time.Equal(filtered[j].Date.Time) {
				if filtered[i].Date.Time.Before(filtered[j].Date.Time) {
					cmp = -1
				} else {
					cmp = 1
				}
			}
		}
		if cmp == 0 {
			return filtered[i].SourceURL < filtered[j].SourceURL
		}
		if asc {
			return cmp < 0
		}
		return cmp > 0
	})

	if q.MaxItems > 0 && len(filtered) > q.MaxItems {
		filtered = filtered[:q.MaxItems]
	}

	if onProgress != nil {
		onProgress(1, 1, len(filtered), len(filtered))
	}

	meta := PageMeta{
		TotalItems: len(filtered),
		TotalPages: 1,
		Page:       1,
		PerPage:    max(25, len(filtered)),
		Latency:    time.Since(start),
		RequestURL: c.baseURL + "/wp-content/uploads/{woocommerce_uploads,edd}",
	}
	return filtered, meta, nil
}

func (c *Client) discoverActiveYearMonths(ctx context.Context) []string {
	now := time.Now().UTC()
	seen := make(map[string]struct{})
	var list []string
	addYM := func(y, m int) {
		if y < 2005 || y > now.Year()+1 || m < 1 || m > 12 {
			return
		}
		ym := fmt.Sprintf("%04d/%02d/", y, m)
		if _, ok := seen[ym]; !ok {
			seen[ym] = struct{}{}
			list = append(list, ym)
		}
	}

	// Include recent 6 months as baseline probes
	for i := 0; i < 6; i++ {
		t := now.AddDate(0, -i, 0)
		addYM(t.Year(), int(t.Month()))
	}

	// Check latest media items for actual upload year/month folders on this site
	var sample FlexSlice[MediaItem]
	if _, err := c.GetJSON(ctx, "/wp/v2/media", url.Values{"per_page": {"25"}, "_fields": {"date,source_url"}}, &sample); err == nil {
		for _, m := range sample {
			if !m.Date.Time.IsZero() {
				addYM(m.Date.Time.Year(), int(m.Date.Time.Month()))
			}
			if match := rePathYearMonth.FindStringSubmatch(m.SourceURL); len(match) == 3 {
				y, _ := strconv.Atoi(match[1])
				mo, _ := strconv.Atoi(match[2])
				addYM(y, mo)
			}
		}
	}
	return list
}

func isAutoIndexHTML(body string) bool {
	if reAutoIndexTitle.MatchString(body) {
		return true
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "parent directory") && strings.Contains(lower, "<a href=")
}

func (c *Client) crawlUploadDirectoryTree(ctx context.Context, rootRelPath string, scopeLabel string, fallbackYearMonths []string, maxDepth int) []MediaItem {
	rootURL := c.baseURL + rootRelPath
	var (
		mu      sync.Mutex
		visited = make(map[string]struct{})
		items   []MediaItem
	)

	var crawl func(dirURL string, depth int) bool
	crawl = func(dirURL string, depth int) bool {
		if depth > maxDepth {
			return false
		}
		mu.Lock()
		if _, ok := visited[dirURL]; ok {
			mu.Unlock()
			return false
		}
		visited[dirURL] = struct{}{}
		mu.Unlock()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, dirURL, nil)
		if err != nil {
			return false
		}
		c.decorateRequest(req)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")

		if err := c.waitRateLimit(ctx); err != nil {
			return false
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return false
		}
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return false
		}
		body := string(bodyBytes)
		if !isAutoIndexHTML(body) {
			return false
		}

		subDirs, files := parseAutoIndexEntries(dirURL, body, scopeLabel)
		if len(files) > 0 {
			mu.Lock()
			items = append(items, files...)
			mu.Unlock()
		}

		for _, sub := range subDirs {
			select {
			case <-ctx.Done():
				return true
			default:
			}
			crawl(sub, depth+1)
		}
		return true
	}

	rootOpen := crawl(rootURL, 0)
	if !rootOpen && len(fallbackYearMonths) > 0 {
		// Root had an index.html or 403; probe active YYYY/MM/ subdirectories
		for _, ym := range fallbackYearMonths {
			select {
			case <-ctx.Done():
				break
			default:
			}
			crawl(rootURL+ym, 1)
		}
	}
	return items
}

// parseAutoIndexEntries extracts subdirectory URLs and file MediaItems from an Apache/Nginx/LiteSpeed autoindex HTML page.
func parseAutoIndexEntries(dirURL string, htmlBody string, scopeLabel string) (subDirs []string, files []MediaItem) {
	baseParsed, err := url.Parse(dirURL)
	if err != nil {
		return nil, nil
	}
	if !strings.HasSuffix(baseParsed.Path, "/") {
		baseParsed.Path += "/"
	}

	matches := reAnchorHref.FindAllStringSubmatchIndex(htmlBody, -1)
	for _, idx := range matches {
		if len(idx) < 6 {
			continue
		}
		rawHref := strings.TrimSpace(html.UnescapeString(htmlBody[idx[2]:idx[3]]))
		linkText := strings.TrimSpace(CleanPlainText(htmlBody[idx[4]:idx[5]]))

		if rawHref == "" || strings.HasPrefix(rawHref, "?") || strings.HasPrefix(rawHref, "#") ||
			strings.HasPrefix(rawHref, "javascript:") || strings.HasPrefix(rawHref, "mailto:") {
			continue
		}
		if rawHref == "../" || rawHref == "./" || rawHref == "/" ||
			strings.EqualFold(linkText, "Parent Directory") || strings.EqualFold(linkText, "Name") ||
			strings.EqualFold(linkText, "Last modified") || strings.EqualFold(linkText, "Size") ||
			strings.EqualFold(linkText, "Description") {
			continue
		}

		resolved, err := baseParsed.Parse(rawHref)
		if err != nil {
			continue
		}
		// Never crawl outside or above baseParsed.Path
		if resolved.Host != baseParsed.Host || !strings.HasPrefix(resolved.Path, baseParsed.Path) || resolved.Path == baseParsed.Path {
			continue
		}

		if strings.HasSuffix(resolved.Path, "/") {
			subDirs = append(subDirs, resolved.String())
			continue
		}

		// Extract trailing context on the same line (date + size in Apache/Nginx/LiteSpeed autoindex)
		lineTail := htmlBody[idx[1]:min(len(htmlBody), idx[1]+200)]
		if nl := strings.IndexAny(lineTail, "\r\n"); nl != -1 {
			lineTail = lineTail[:nl]
		}
		cleanTail := CleanPlainText(lineTail)
		modTime, tailWithoutDate := parseAutoIndexDate(cleanTail, resolved.Path)
		sizeBytes := parseAutoIndexSize(tailWithoutDate)

		fileName := path.Base(resolved.Path)
		ext := ExtractFileExtension(resolved.Path)
		mimeType, mediaType := InferMimeAndMediaType(ext)

		relPath := resolved.Path
		if upIdx := strings.Index(relPath, "/wp-content/uploads/"); upIdx != -1 {
			relPath = strings.TrimPrefix(relPath[upIdx:], "/wp-content/uploads/")
		}

		files = append(files, MediaItem{
			ID:        syntheticMediaID(resolved.String()),
			Date:      WPTime{Time: modTime},
			Modified:  WPTime{Time: modTime},
			Slug:      fileName,
			Status:    "inherit",
			Type:      scopeLabel + "-directory-file",
			Link:      resolved.String(),
			Title:     RenderedField{Rendered: fileName},
			Caption:   RenderedField{Rendered: fmt.Sprintf("Indexed from %s directory (%s)", scopeLabel, relPath)},
			AltText:   fmt.Sprintf("[%s] %s", scopeLabel, relPath),
			MediaType: mediaType,
			MimeType:  mimeType,
			SourceURL: resolved.String(),
			MediaDetails: MediaDetails{
				File:     relPath,
				FileSize: sizeBytes,
			},
		})
	}
	return subDirs, files
}

func parseAutoIndexDate(tail string, urlPath string) (time.Time, string) {
	if loc := reRowDateISO.FindStringSubmatchIndex(tail); len(loc) >= 4 {
		datePart := tail[loc[2]:loc[3]]
		timePart := "00:00:00"
		if loc[4] != -1 && loc[5] != -1 {
			tp := tail[loc[4]:loc[5]]
			if len(tp) == 5 {
				timePart = tp + ":00"
			} else {
				timePart = tp
			}
		}
		t, _ := time.Parse("2006-01-02 15:04:05", datePart+" "+timePart)
		return t, tail[loc[1]:]
	}
	if loc := reRowDateApache.FindStringSubmatchIndex(tail); len(loc) >= 4 {
		datePart := tail[loc[2]:loc[3]]
		timePart := "00:00"
		if loc[4] != -1 && loc[5] != -1 {
			timePart = tail[loc[4]:loc[5]]
		}
		layout := "02-Jan-2006 15:04"
		if len(timePart) == 8 {
			layout = "02-Jan-2006 15:04:05"
		}
		t, _ := time.Parse(layout, datePart+" "+timePart)
		return t, tail[loc[1]:]
	}
	// Fallback: infer year/month from URL path (/YYYY/MM/)
	if m := rePathYearMonth.FindStringSubmatch(urlPath); len(m) == 3 {
		t, _ := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-01")
		return t, tail
	}
	return time.Time{}, tail
}

func parseAutoIndexSize(tail string) int64 {
	m := reRowSize.FindStringSubmatch(tail)
	if len(m) < 3 {
		// Check if there is a plain integer byte count at the end of the line
		fields := strings.Fields(tail)
		if len(fields) > 0 {
			last := fields[len(fields)-1]
			if n, err := strconv.ParseInt(last, 10, 64); err == nil && n > 0 {
				return n
			}
		}
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0
	}
	unit := strings.ToUpper(m[2])
	switch unit {
	case "K", "KB", "KIB":
		return int64(val * 1024)
	case "M", "MB", "MIB":
		return int64(val * 1024 * 1024)
	case "G", "GB", "GIB":
		return int64(val * 1024 * 1024 * 1024)
	case "T", "TB", "TIB":
		return int64(val * 1024 * 1024 * 1024 * 1024)
	}
	return 0
}

// InferMimeAndMediaType maps a lowercase file extension to a canonical MIME type and WordPress media_type.
func InferMimeAndMediaType(ext string) (mimeType string, mediaType string) {
	ext = strings.TrimLeft(strings.ToLower(strings.TrimSpace(ext)), ".")
	switch ext {
	case "zip":
		return "application/zip", "application"
	case "rar":
		return "application/x-rar-compressed", "application"
	case "7z":
		return "application/x-7z-compressed", "application"
	case "tar":
		return "application/x-tar", "application"
	case "gz", "tgz":
		return "application/gzip", "application"
	case "csv":
		return "text/csv", "text"
	case "tsv":
		return "text/tab-separated-values", "text"
	case "xls":
		return "application/vnd.ms-excel", "application"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application"
	case "ods":
		return "application/vnd.oasis.opendocument.spreadsheet", "application"
	case "pdf":
		return "application/pdf", "application"
	case "doc":
		return "application/msword", "application"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application"
	case "ppt":
		return "application/vnd.ms-powerpoint", "application"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation", "application"
	case "json":
		return "application/json", "application"
	case "xml":
		return "application/xml", "application"
	case "txt", "md", "log":
		return "text/plain", "text"
	case "jpg", "jpeg":
		return "image/jpeg", "image"
	case "png":
		return "image/png", "image"
	case "webp":
		return "image/webp", "image"
	case "gif":
		return "image/gif", "image"
	case "svg":
		return "image/svg+xml", "image"
	case "avif":
		return "image/avif", "image"
	case "mp4", "m4v":
		return "video/mp4", "video"
	case "webm":
		return "video/webm", "video"
	case "mov":
		return "video/quicktime", "video"
	case "mp3":
		return "audio/mpeg", "audio"
	case "wav":
		return "audio/wav", "audio"
	case "ogg":
		return "audio/ogg", "audio"
	default:
		return "application/octet-stream", "file"
	}
}

func syntheticMediaID(u string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(u))
	id := int(h.Sum32() & 0x7fffffff)
	if id == 0 {
		id = 1
	}
	return id
}

// wcStoreProductWire represents a WooCommerce Store API v1 product item (`/wp-json/wc/store/v1/products`).
type wcStoreProductWire struct {
	ID          FlexInt    `json:"id"`
	Name        FlexString `json:"name"`
	Slug        FlexString `json:"slug"`
	Permalink   FlexString `json:"permalink"`
	Description FlexString `json:"description"`
	ShortDesc   FlexString `json:"short_description"`
	SKU         FlexString `json:"sku"`
	Prices      struct {
		Price        FlexString `json:"price"`
		RegularPrice FlexString `json:"regular_price"`
		SalePrice    FlexString `json:"sale_price"`
		CurrencyCode FlexString `json:"currency_code"`
	} `json:"prices"`
	Images FlexSlice[struct {
		ID   FlexInt    `json:"id"`
		Src  FlexString `json:"src"`
		Name FlexString `json:"name"`
		Alt  FlexString `json:"alt"`
	}] `json:"images"`
}

func (c *Client) fetchWooCommerceStoreProductsAsPosts(ctx context.Context, q QueryFilter) ([]Post, PageMeta, error) {
	params := url.Values{
		"per_page": {strconv.Itoa(max(1, min(100, q.PerPage)))},
		"page":     {strconv.Itoa(max(1, q.Page))},
	}
	if strings.TrimSpace(q.Search) != "" {
		params.Set("search", strings.TrimSpace(q.Search))
	}
	var products FlexSlice[wcStoreProductWire]
	meta, err := c.GetJSON(ctx, "/wc/store/v1/products", params, &products)
	if err != nil {
		return nil, meta, err
	}
	posts := make([]Post, 0, len(products))
	for _, p := range products {
		var featURL string
		var featID int
		if len(p.Images) > 0 {
			featURL = string(p.Images[0].Src)
			featID = int(p.Images[0].ID)
		}
		var emb *EmbeddedData
		if featURL != "" {
			emb = &EmbeddedData{
				FeaturedMedia: FlexSlice[MediaItem]{{ID: featID, SourceURL: featURL}},
			}
		}
		posts = append(posts, Post{
			ID:            int(p.ID),
			Slug:          string(p.Slug),
			Status:        "publish",
			Type:          "product",
			Link:          string(p.Permalink),
			Title:         RenderedField{Rendered: string(p.Name)},
			Content:       RenderedField{Rendered: string(p.Description)},
			Excerpt:       RenderedField{Rendered: string(p.ShortDesc)},
			FeaturedMedia: featID,
			Embedded:      emb,
			Extra: map[string]any{
				"sku":      string(p.SKU),
				"price":    string(p.Prices.Price),
				"currency": string(p.Prices.CurrencyCode),
			},
		})
	}
	if meta.TotalPages == 0 && len(posts) > 0 {
		meta.TotalPages = 1
		meta.TotalItems = len(posts)
	}
	return posts, meta, nil
}

func (c *Client) fetchEDDProductsAsPosts(ctx context.Context, q QueryFilter) ([]Post, PageMeta, error) {
	start := time.Now()
	eddURL := fmt.Sprintf("%s/?edd-api=products&number=%d&page=%d", c.baseURL, max(1, min(100, q.PerPage)), max(1, q.Page))
	if strings.TrimSpace(q.Search) != "" {
		eddURL += "&s=" + url.QueryEscape(strings.TrimSpace(q.Search))
	}
	resp, latency, err := c.doWithRetry(ctx, eddURL)
	if err != nil {
		return nil, PageMeta{RequestURL: eddURL, Latency: latency}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, PageMeta{RequestURL: eddURL, Latency: latency}, fmt.Errorf("HTTP %d from %s", resp.StatusCode, eddURL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, PageMeta{RequestURL: eddURL, Latency: latency}, err
	}
	var payload struct {
		Products FlexSlice[struct {
			Info struct {
				ID        FlexInt    `json:"id"`
				Slug      FlexString `json:"slug"`
				Title     FlexString `json:"title"`
				CreateDate WPTime    `json:"create_date"`
				Modified  WPTime     `json:"modified_date"`
				Status    FlexString `json:"status"`
				Permalink FlexString `json:"permalink"`
				Content   FlexString `json:"content"`
				Excerpt   FlexString `json:"excerpt"`
				Thumbnail FlexString `json:"thumbnail"`
			} `json:"info"`
		}] `json:"products"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, PageMeta{RequestURL: eddURL, Latency: time.Since(start)}, err
	}
	posts := make([]Post, 0, len(payload.Products))
	for _, prod := range payload.Products {
		var emb *EmbeddedData
		if prod.Info.Thumbnail != "" {
			emb = &EmbeddedData{
				FeaturedMedia: FlexSlice[MediaItem]{{SourceURL: string(prod.Info.Thumbnail)}},
			}
		}
		posts = append(posts, Post{
			ID:       int(prod.Info.ID),
			Date:     prod.Info.CreateDate,
			Modified: prod.Info.Modified,
			Slug:     string(prod.Info.Slug),
			Status:   string(prod.Info.Status),
			Type:     "download",
			Link:     string(prod.Info.Permalink),
			Title:    RenderedField{Rendered: string(prod.Info.Title)},
			Content:  RenderedField{Rendered: string(prod.Info.Content)},
			Excerpt:  RenderedField{Rendered: string(prod.Info.Excerpt)},
			Embedded: emb,
		})
	}
	meta := PageMeta{
		TotalItems: len(posts),
		TotalPages: 1,
		Page:       max(1, q.Page),
		PerPage:    max(25, len(posts)),
		Latency:    time.Since(start),
		RequestURL: eddURL,
	}
	return posts, meta, nil
}

// harvestWooCommerceAssets discovers WooCommerce products and downloadable/attached files across all pages via:
//   - /wp-json/wc/store/v1/products (WooCommerce Store API)
//   - /wp-json/wc/v3/products (WooCommerce REST API v3 when authenticated)
//   - /wp-json/wp/v2/product (WooCommerce Custom Post Type)
//   - /my-account/downloads/ (when authenticated with a customer Cookie header)
func (c *Client) harvestWooCommerceAssets(ctx context.Context, q QueryFilter) []MediaItem {
	var out []MediaItem
	maxPages := q.MaxPages
	if maxPages <= 0 {
		maxPages = 20
	}

	// 1. WooCommerce Store API (/wc/store/v1/products) — paginated
	for page := 1; page <= maxPages; page++ {
		params := url.Values{
			"per_page": {"100"},
			"page":     {strconv.Itoa(page)},
		}
		if strings.TrimSpace(q.Search) != "" {
			params.Set("search", strings.TrimSpace(q.Search))
		}
		var storeProducts FlexSlice[wcStoreProductWire]
		meta, err := c.GetJSON(ctx, "/wc/store/v1/products", params, &storeProducts)
		if err != nil || len(storeProducts) == 0 {
			break
		}
		for _, p := range storeProducts {
			title := string(p.Name)
			for _, img := range p.Images {
				if img.Src != "" {
					ext := ExtractFileExtension(string(img.Src))
					mime, mtype := InferMimeAndMediaType(ext)
					out = append(out, MediaItem{
						ID:        max(int(img.ID), syntheticMediaID(string(img.Src))),
						Slug:      string(p.Slug),
						Type:      "woocommerce-product-asset",
						Link:      string(p.Permalink),
						Title:     RenderedField{Rendered: fmt.Sprintf("[WC Product #%d] %s — %s", p.ID, title, img.Name)},
						AltText:   fmt.Sprintf("[woocommerce] SKU:%s %s", p.SKU, img.Alt),
						MediaType: mtype,
						MimeType:  mime,
						SourceURL: string(img.Src),
					})
				}
			}
			for _, u := range ExtractMediaURLsFromHTML(string(p.Description) + " " + string(p.ShortDesc)) {
				ext := ExtractFileExtension(u)
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        syntheticMediaID(u),
					Slug:      string(p.Slug),
					Type:      "woocommerce-product-file",
					Link:      string(p.Permalink),
					Title:     RenderedField{Rendered: fmt.Sprintf("[WC Product #%d] %s (%s)", p.ID, title, path.Base(u))},
					AltText:   "[woocommerce]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: u,
				})
			}
		}
		if meta.TotalPages > 0 && page >= meta.TotalPages {
			break
		}
		if len(storeProducts) < 100 {
			break
		}
	}

	// 2. Authenticated WooCommerce v3 API (/wc/v3/products) which exposes `downloads` array directly
	for page := 1; page <= maxPages; page++ {
		params := url.Values{
			"per_page": {"100"},
			"page":     {strconv.Itoa(page)},
		}
		if strings.TrimSpace(q.Search) != "" {
			params.Set("search", strings.TrimSpace(q.Search))
		}
		var v3Products FlexSlice[struct {
			ID        FlexInt    `json:"id"`
			Name      FlexString `json:"name"`
			Slug      FlexString `json:"slug"`
			Permalink FlexString `json:"permalink"`
			Downloads FlexSlice[struct {
				ID   FlexString `json:"id"`
				Name FlexString `json:"name"`
				File FlexString `json:"file"`
			}] `json:"downloads"`
		}]
		meta, err := c.GetJSON(ctx, "/wc/v3/products", params, &v3Products)
		if err != nil || len(v3Products) == 0 {
			break
		}
		for _, p := range v3Products {
			for _, dl := range p.Downloads {
				if dl.File == "" {
					continue
				}
				ext := ExtractFileExtension(string(dl.File))
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        syntheticMediaID(string(dl.File)),
					Slug:      string(p.Slug),
					Type:      "woocommerce-download",
					Link:      string(p.Permalink),
					Title:     RenderedField{Rendered: fmt.Sprintf("[WC Download #%d] %s — %s", p.ID, p.Name, dl.Name)},
					AltText:   "[woocommerce_uploads]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: string(dl.File),
				})
			}
		}
		if meta.TotalPages > 0 && page >= meta.TotalPages {
			break
		}
		if len(v3Products) < 100 {
			break
		}
	}

	// 3. Standard WP REST v2 CPT (/wp/v2/product)
	pq := DefaultQueryFilter("product")
	pq.Search = q.Search
	pq.PerPage = 100
	pq.MaxPages = maxPages
	var rawProducts FlexSlice[Post]
	if _, err := c.GetJSON(ctx, "/wp/v2/product", BuildQueryValues(pq), &rawProducts); err == nil && len(rawProducts) > 0 {
		posts, err := c.FetchAllPosts(ctx, pq, nil)
		if err != nil {
			posts = rawProducts
		}
		for _, p := range posts {
			title := CleanPlainText(p.Title.Rendered)
			if feat := p.ResolveFeaturedURL(); feat != "" {
				ext := ExtractFileExtension(feat)
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        max(p.FeaturedMedia, syntheticMediaID(feat)),
					Date:      p.Date,
					Slug:      p.Slug,
					Type:      "woocommerce-product",
					Link:      p.Link,
					Title:     RenderedField{Rendered: fmt.Sprintf("[WC #%d] %s", p.ID, title)},
					AltText:   "[woocommerce]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: feat,
				})
			}
			for _, u := range ExtractMediaURLsFromHTML(p.Content.Rendered + " " + p.Excerpt.Rendered) {
				ext := ExtractFileExtension(u)
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        syntheticMediaID(u),
					Date:      p.Date,
					Slug:      p.Slug,
					Type:      "woocommerce-product-file",
					Link:      p.Link,
					Title:     RenderedField{Rendered: fmt.Sprintf("[WC #%d] %s (%s)", p.ID, title, path.Base(u))},
					AltText:   "[woocommerce]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: u,
				})
			}
		}
	}

	// 4. If a Cookie header is configured, also inspect /my-account/downloads/ for customer downloadable files
	if c.CookieHeader() != "" {
		out = append(out, c.harvestAccountDownloadsPage(ctx, "/my-account/downloads/", "woocommerce")...)
	}

	return out
}

// harvestEDDAssets discovers Easy Digital Downloads items and files via:
//   - /wp-json/wp/v2/edd-downloads (EDD 3.x Custom Post Type REST base)
//   - /wp-json/wp/v2/download (EDD legacy/custom Custom Post Type REST base)
//   - /?edd-api=products (EDD native JSON API)
//   - /purchase-history/ (when authenticated with a customer Cookie header)
func (c *Client) harvestEDDAssets(ctx context.Context, q QueryFilter) []MediaItem {
	var out []MediaItem
	maxPages := q.MaxPages
	if maxPages <= 0 {
		maxPages = 20
	}

	// 1. WP REST v2 CPT (/wp/v2/edd-downloads or /wp/v2/download)
	//    When q.Search is empty, query `?search=wp-content/uploads` first so MySQL filters thousands of EDD products
	//    down to only those whose post_content embeds `/wp-content/uploads/...` links in a single fast request.
	cptFound := false
	for _, cptEndpoint := range []string{"edd-downloads", "download"} {
		dq := DefaultQueryFilter(cptEndpoint)
		dq.Search = strings.TrimSpace(q.Search)
		if dq.Search == "" {
			dq.Search = "wp-content/uploads"
		}
		dq.PerPage = 100
		dq.MaxPages = maxPages
		if q.MaxPages <= 0 {
			dq.MaxPages = 3
		}
		var posts []Post
		var rawDownloads FlexSlice[Post]
		meta, err := c.GetJSON(ctx, "/wp/v2/"+cptEndpoint, BuildQueryValues(dq), &rawDownloads)
		if err == nil {
			cptFound = true
			if len(rawDownloads) > 0 {
				posts = append(posts, rawDownloads...)
				if meta.TotalPages > 1 {
					if allPosts, errAll := c.FetchAllPosts(ctx, dq, nil); errAll == nil {
						posts = allPosts
					}
				}
			} else if strings.TrimSpace(q.Search) == "" {
				// Fallback to page 1 without search if no posts matched "wp-content/uploads"
				dqNoSearch := dq
				dqNoSearch.Search = ""
				if _, err2 := c.GetJSON(ctx, "/wp/v2/"+cptEndpoint, BuildQueryValues(dqNoSearch), &rawDownloads); err2 == nil {
					posts = rawDownloads
				}
			}
		}
		if cptFound {
			for _, p := range posts {
				title := CleanPlainText(p.Title.Rendered)
				if feat := p.ResolveFeaturedURL(); feat != "" {
					ext := ExtractFileExtension(feat)
					mime, mtype := InferMimeAndMediaType(ext)
					out = append(out, MediaItem{
						ID:        max(p.FeaturedMedia, syntheticMediaID(feat)),
						Date:      p.Date,
						Slug:      p.Slug,
						Type:      "edd-download",
						Link:      p.Link,
						Title:     RenderedField{Rendered: fmt.Sprintf("[EDD #%d] %s", p.ID, title)},
						AltText:   "[edd]",
						MediaType: mtype,
						MimeType:  mime,
						SourceURL: feat,
					})
				}
				for _, u := range ExtractMediaURLsFromHTML(p.Content.Rendered + " " + p.Excerpt.Rendered) {
					ext := ExtractFileExtension(u)
					mime, mtype := InferMimeAndMediaType(ext)
					out = append(out, MediaItem{
						ID:        syntheticMediaID(u),
						Date:      p.Date,
						Slug:      p.Slug,
						Type:      "edd-download-file",
						Link:      p.Link,
						Title:     RenderedField{Rendered: fmt.Sprintf("[EDD #%d] %s (%s)", p.ID, title, path.Base(u))},
						AltText:   "[edd]",
						MediaType: mtype,
						MimeType:  mime,
						SourceURL: u,
					})
				}
			}
			break
		}
	}

	// 2. Native EDD API (/?edd-api=products) — paginated
	eddMaxPages := maxPages
	if q.MaxPages <= 0 {
		eddMaxPages = 2
	}
	for page := 1; page <= eddMaxPages; page++ {
		eddURL := fmt.Sprintf("%s/?edd-api=products&number=100&page=%d", c.baseURL, page)
		if strings.TrimSpace(q.Search) != "" {
			eddURL += "&s=" + url.QueryEscape(strings.TrimSpace(q.Search))
		}
		resp, _, err := c.doWithRetry(ctx, eddURL)
		if err != nil || resp == nil {
			break
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			break
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		_ = resp.Body.Close()
		if err != nil {
			break
		}
		var payload struct {
			Products FlexSlice[struct {
				Info struct {
					ID         FlexInt    `json:"id"`
					Slug       FlexString `json:"slug"`
					Title      FlexString `json:"title"`
					CreateDate WPTime     `json:"create_date"`
					Permalink  FlexString `json:"permalink"`
					Thumbnail  FlexString `json:"thumbnail"`
					Content    FlexString `json:"content"`
				} `json:"info"`
				Files FlexSlice[struct {
					Name FlexString `json:"name"`
					File FlexString `json:"file"`
				}] `json:"files"`
			}] `json:"products"`
		}
		if json.Unmarshal(body, &payload) != nil || len(payload.Products) == 0 {
			break
		}
		pageHasFiles := false
		for _, prod := range payload.Products {
			for _, f := range prod.Files {
				if f.File == "" {
					continue
				}
				pageHasFiles = true
				ext := ExtractFileExtension(string(f.File))
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        syntheticMediaID(string(f.File)),
					Date:      prod.Info.CreateDate,
					Slug:      string(prod.Info.Slug),
					Type:      "edd-file",
					Link:      string(prod.Info.Permalink),
					Title:     RenderedField{Rendered: fmt.Sprintf("[EDD #%d] %s — %s", prod.Info.ID, prod.Info.Title, f.Name)},
					AltText:   "[edd]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: string(f.File),
				})
			}
			for _, u := range ExtractMediaURLsFromHTML(string(prod.Info.Content)) {
				ext := ExtractFileExtension(u)
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        syntheticMediaID(u),
					Date:      prod.Info.CreateDate,
					Slug:      string(prod.Info.Slug),
					Type:      "edd-download-file",
					Link:      string(prod.Info.Permalink),
					Title:     RenderedField{Rendered: fmt.Sprintf("[EDD #%d] %s (%s)", prod.Info.ID, prod.Info.Title, path.Base(u))},
					AltText:   "[edd]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: u,
				})
			}
			if prod.Info.Thumbnail != "" {
				ext := ExtractFileExtension(string(prod.Info.Thumbnail))
				mime, mtype := InferMimeAndMediaType(ext)
				out = append(out, MediaItem{
					ID:        syntheticMediaID(string(prod.Info.Thumbnail)),
					Date:      prod.Info.CreateDate,
					Slug:      string(prod.Info.Slug),
					Type:      "edd-thumbnail",
					Link:      string(prod.Info.Permalink),
					Title:     RenderedField{Rendered: fmt.Sprintf("[EDD #%d] %s", prod.Info.ID, prod.Info.Title)},
					AltText:   "[edd]",
					MediaType: mtype,
					MimeType:  mime,
					SourceURL: string(prod.Info.Thumbnail),
				})
			}
		}
		if page == 1 && pageHasFiles && cptFound && q.MaxPages <= 0 {
			eddMaxPages = maxPages
		}
		if len(payload.Products) < 10 {
			break
		}
	}

	// 3. If a Cookie header is configured, also inspect /purchase-history/ for customer downloadable files
	if c.CookieHeader() != "" {
		out = append(out, c.harvestAccountDownloadsPage(ctx, "/purchase-history/", "edd")...)
	}

	return out
}

func (c *Client) harvestAccountDownloadsPage(ctx context.Context, relPath string, scope string) []MediaItem {
	pageURL := c.baseURL + relPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil
	}
	c.decorateRequest(req)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")

	resp, err := c.httpClient.Do(req)
	if err != nil || resp == nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil
	}
	baseParsed, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	var items []MediaItem
	matches := reDownloadLinks.FindAllStringSubmatch(string(body), -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		rawHref := html.UnescapeString(strings.TrimSpace(m[1]))
		label := CleanPlainText(m[2])
		resolved, err := baseParsed.Parse(rawHref)
		if err != nil {
			continue
		}
		ext := ExtractFileExtension(resolved.Path)
		if ext == "" {
			ext = ExtractFileExtension(label)
		}
		mime, mtype := InferMimeAndMediaType(ext)
		if label == "" {
			label = path.Base(resolved.Path)
		}
		items = append(items, MediaItem{
			ID:        syntheticMediaID(resolved.String()),
			Slug:      label,
			Type:      scope + "-account-download",
			Link:      pageURL,
			Title:     RenderedField{Rendered: fmt.Sprintf("[%s Account Download] %s", strings.ToUpper(scope), label)},
			AltText:   fmt.Sprintf("[%s] authenticated download", scope),
			MediaType: mtype,
			MimeType:  mime,
			SourceURL: resolved.String(),
		})
	}
	return items
}
