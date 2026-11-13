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

// MatchesDirectoryScope returns true if a media URL or file path belongs to any of the requested scopes.
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
				strings.HasPrefix(f, "woocommerce_uploads/") ||
				strings.Contains(mt, "woocommerce") ||
				strings.Contains(mt, "wc-") {
				return true
			}
		case ScopeEDD:
			if strings.Contains(u, "/uploads/edd/") ||
				strings.HasPrefix(f, "edd/") ||
				strings.Contains(mt, "edd") {
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
	reRowSize        = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*([KMGT]?B?)\b`)
	reYearMonth      = regexp.MustCompile(`^(20\d{2}|19\d{2})/?$`)
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
				// Check WooCommerce Store API v1 (public)
				var wcProducts []any
				meta, err := c.GetJSON(ctx, "/wc/store/v1/products", url.Values{"per_page": {"1"}}, &wcProducts)
				if err == nil && (meta.TotalItems > 0 || len(wcProducts) > 0) {
					st.APIAvailable = true
					st.APIEndpoint = "/wc/store/v1/products"
					st.APIItemCount = max(meta.TotalItems, len(wcProducts))
				} else {
					// Fallback to /wp/v2/product CPT
					meta2, err2 := c.GetJSON(ctx, "/wp/v2/product", url.Values{"per_page": {"1"}, "_fields": {"id"}}, &wcProducts)
					if err2 == nil && (meta2.TotalItems > 0 || len(wcProducts) > 0) {
						st.APIAvailable = true
						st.APIEndpoint = "/wp/v2/product"
						st.APIItemCount = max(meta2.TotalItems, len(wcProducts))
					}
				}

			case ScopeEDD:
				// Check /wp/v2/download CPT
				var eddItems []any
				meta, err := c.GetJSON(ctx, "/wp/v2/download", url.Values{"per_page": {"1"}, "_fields": {"id"}}, &eddItems)
				if err == nil && (meta.TotalItems > 0 || len(eddItems) > 0) {
					st.APIAvailable = true
					st.APIEndpoint = "/wp/v2/download"
					st.APIItemCount = max(meta.TotalItems, len(eddItems))
				} else {
					// Check native /edd-api/products/
					if cnt, ok := c.probeEDDNativeAPI(ctx); ok {
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
//  2. WooCommerce REST API product & downloadable file harvesting (`/wc/store/v1/products`, `/wc/v3/products`, `/wp/v2/product`)
//  3. Easy Digital Downloads REST API harvesting (`/wp/v2/download`, `/?edd-api=products`)
//  4. WordPress Media Library (`/wp/v2/media`) entries located inside `woocommerce_uploads/` or `edd/`
func (c *Client) ScanDirectories(ctx context.Context, q QueryFilter, onProgress ProgressCallback) ([]MediaItem, PageMeta, error) {
	start := time.Now()
	scopes := NormalizeDirectoryScopes(q.DirectoryScopes)

	var (
		mu      sync.Mutex
		seenURL = make(map[string]struct{})
		results []MediaItem
	)

	addEntry := func(item MediaItem) {
		u := strings.TrimSpace(item.SourceURL)
		if u == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if _, exists := seenURL[u]; exists {
			return
		}
		seenURL[u] = struct{}{}
		results = append(results, item)
	}

	// Discover candidate YYYY/MM subpaths from recent posts/media so even if root directory listing
	// is 403 Forbidden (e.g. index.html in root), we can still probe open year/month subdirectories.
	yearMonths := c.discoverActiveYearMonths(ctx)

	var wg sync.WaitGroup

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

	// 2. Harvest WooCommerce REST APIs if ScopeWooCommerce is active
	for _, sc := range scopes {
		if sc == ScopeWooCommerce {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, it := range c.harvestWooCommerceAssets(ctx, q.Search) {
					addEntry(it)
				}
			}()
		}
		if sc == ScopeEDD {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, it := range c.harvestEDDAssets(ctx, q.Search) {
					addEntry(it)
				}
			}()
		}
	}

	// 3. Search/scan /wp/v2/media for attachments inside woocommerce_uploads/ or edd/ (or matching scopes)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mq := QueryFilter{
			Endpoint: "media",
			Search:   q.Search,
			PerPage:  100,
			MaxPages: max(1, q.MaxPages),
		}
		if mq.MaxPages == 0 {
			mq.MaxPages = 10
		}
		// Fetch media items and keep those that belong to the target directory scopes
		items, err := c.FetchAllMedia(ctx, mq, nil)
		if err == nil {
			for _, it := range items {
				if MatchesDirectoryScope(it.SourceURL, it.MediaDetails.File, it.MediaType, scopes) {
					if strings.Contains(strings.ToLower(it.SourceURL), "/woocommerce_uploads/") {
						it.AltText = "[woocommerce_uploads] " + it.AltText
					} else if strings.Contains(strings.ToLower(it.SourceURL), "/uploads/edd/") {
						it.AltText = "[edd] " + it.AltText
					}
					addEntry(it)
				}
			}
		}
	}()

	wg.Wait()

	// Filter results using Quarry's unified media & directory filter
	filtered := make([]MediaItem, 0, len(results))
	searchLower := strings.ToLower(strings.TrimSpace(q.Search))
	for _, item := range results {
		if searchLower != "" {
			haystack := strings.ToLower(item.Title.Rendered + " " + item.Slug + " " + item.SourceURL + " " + item.AltText + " " + item.Caption.Rendered)
			if !strings.Contains(haystack, searchLower) {
				continue
			}
		}
		if !MatchesMediaFilter(item, q) {
			continue
		}
		filtered = append(filtered, item)
	}

	// Sort deterministically (newest date first, then title/URL)
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].Date.Time.Equal(filtered[j].Date.Time) {
			if strings.ToLower(q.Order) == "asc" {
				return filtered[i].Date.Time.Before(filtered[j].Date.Time)
			}
			return filtered[i].Date.Time.After(filtered[j].Date.Time)
		}
		return filtered[i].SourceURL < filtered[j].SourceURL
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

	// Always include recent 6 months as quick probes
	for i := 0; i < 6; i++ {
		t := now.AddDate(0, -i, 0)
		addYM(t.Year(), int(t.Month()))
	}

	// Also check latest media items for actual upload year/month folders on this site
	var sample FlexSlice[MediaItem]
	if _, err := c.GetJSON(ctx, "/wp/v2/media", url.Values{"per_page": {"20"}, "_fields": {"date,source_url"}}, &sample); err == nil {
		for _, m := range sample {
			if !m.Date.Time.IsZero() {
				addYM(m.Date.Time.Year(), int(m.Date.Time.Month()))
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

		// Extract trailing context on the same line (size / date in Apache/Nginx autoindex)
		lineTail := htmlBody[idx[1]:min(len(htmlBody), idx[1]+160)]
		if nl := strings.IndexAny(lineTail, "\r\n"); nl != -1 {
			lineTail = lineTail[:nl]
		}
		sizeBytes := parseAutoIndexSize(CleanPlainText(lineTail))

		fileName := path.Base(resolved.Path)
		ext := ExtractFileExtension(resolved.Path)
		mimeType, mediaType := InferMimeAndMediaType(ext)

		relPath := resolved.Path
		if upIdx := strings.Index(relPath, "/wp-content/uploads/"); upIdx != -1 {
			relPath = strings.TrimPrefix(relPath[upIdx:], "/wp-content/uploads/")
		}

		files = append(files, MediaItem{
			ID:        syntheticMediaID(resolved.String()),
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

func parseAutoIndexSize(tail string) int64 {
	m := reRowSize.FindStringSubmatch(tail)
	if len(m) < 3 {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0
	}
	unit := strings.ToUpper(m[2])
	switch unit {
	case "K", "KB":
		return int64(val * 1024)
	case "M", "MB":
		return int64(val * 1024 * 1024)
	case "G", "GB":
		return int64(val * 1024 * 1024 * 1024)
	case "T", "TB":
		return int64(val * 1024 * 1024 * 1024 * 1024)
	case "B", "":
		// Avoid mistaking a year like "2025" in a date for 2025 bytes unless it's at the end
		if val >= 1990 && val <= 2035 && unit == "" {
			return 0
		}
		return int64(val)
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

// harvestWooCommerceAssets discovers WooCommerce products and downloadable/attached files via:
//   - /wp-json/wc/store/v1/products (WooCommerce Store API)
//   - /wp-json/wc/v3/products (WooCommerce REST API v3 when authenticated)
//   - /wp-json/wp/v2/product (WooCommerce Custom Post Type)
func (c *Client) harvestWooCommerceAssets(ctx context.Context, search string) []MediaItem {
	var out []MediaItem

	// 1. WooCommerce Store API (/wc/store/v1/products)
	params := url.Values{"per_page": {"100"}}
	if strings.TrimSpace(search) != "" {
		params.Set("search", strings.TrimSpace(search))
	}
	var storeProducts FlexSlice[struct {
		ID          FlexInt        `json:"id"`
		Name        FlexString     `json:"name"`
		Slug        FlexString     `json:"slug"`
		Permalink   FlexString     `json:"permalink"`
		Description FlexString     `json:"description"`
		ShortDesc   FlexString     `json:"short_description"`
		SKU         FlexString     `json:"sku"`
		Images      FlexSlice[struct {
			ID   FlexInt    `json:"id"`
			Src  FlexString `json:"src"`
			Name FlexString `json:"name"`
			Alt  FlexString `json:"alt"`
		}] `json:"images"`
	}]
	if _, err := c.GetJSON(ctx, "/wc/store/v1/products", params, &storeProducts); err == nil {
		for _, p := range storeProducts {
			title := string(p.Name)
			for _, img := range p.Images {
				if img.Src != "" {
					ext := ExtractFileExtension(string(img.Src))
					mime, mtype := InferMimeAndMediaType(ext)
					out = append(out, MediaItem{
						ID:        int(img.ID),
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
	}

	// 2. Authenticated WooCommerce v3 API (/wc/v3/products) which exposes `downloads` array directly
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
	if _, err := c.GetJSON(ctx, "/wc/v3/products", params, &v3Products); err == nil {
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
	}

	// 3. Standard WP REST v2 CPT (/wp/v2/product)
	pq := DefaultQueryFilter("product")
	pq.Search = search
	pq.PerPage = 100
	if posts, _, err := c.FetchPosts(ctx, pq); err == nil {
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

	return out
}

// harvestEDDAssets discovers Easy Digital Downloads items and files via:
//   - /wp-json/wp/v2/download (EDD Custom Post Type)
//   - /?edd-api=products (EDD native JSON API)
func (c *Client) harvestEDDAssets(ctx context.Context, search string) []MediaItem {
	var out []MediaItem

	// 1. WP REST v2 CPT (/wp/v2/download)
	dq := DefaultQueryFilter("download")
	dq.Search = search
	dq.PerPage = 100
	if posts, _, err := c.FetchPosts(ctx, dq); err == nil {
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
	}

	// 2. Native EDD API (/?edd-api=products)
	eddURL := c.baseURL + "/?edd-api=products&number=100"
	if strings.TrimSpace(search) != "" {
		eddURL += "&s=" + url.QueryEscape(strings.TrimSpace(search))
	}
	if resp, _, err := c.doWithRetry(ctx, eddURL); err == nil && resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024)); err == nil {
				var payload struct {
					Products FlexSlice[struct {
						Info struct {
							ID        FlexInt    `json:"id"`
							Slug      FlexString `json:"slug"`
							Title     FlexString `json:"title"`
							Permalink FlexString `json:"permalink"`
							Thumbnail FlexString `json:"thumbnail"`
							Content   FlexString `json:"content"`
						} `json:"info"`
						Files FlexSlice[struct {
							Name FlexString `json:"name"`
							File FlexString `json:"file"`
						}] `json:"files"`
					}] `json:"products"`
				}
				if json.Unmarshal(body, &payload) == nil {
					for _, prod := range payload.Products {
						for _, f := range prod.Files {
							if f.File == "" {
								continue
							}
							ext := ExtractFileExtension(string(f.File))
							mime, mtype := InferMimeAndMediaType(ext)
							out = append(out, MediaItem{
								ID:        syntheticMediaID(string(f.File)),
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
						if prod.Info.Thumbnail != "" {
							ext := ExtractFileExtension(string(prod.Info.Thumbnail))
							mime, mtype := InferMimeAndMediaType(ext)
							out = append(out, MediaItem{
								ID:        syntheticMediaID(string(prod.Info.Thumbnail)),
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
				}
			}
		}
	}

	return out
}
