package wp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeSiteURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"wordpress.org/news", "https://wordpress.org/news"},
		{"https://example.com/wp-json/wp/v2/posts", "https://example.com"},
		{"http://blog.example.org/", "http://blog.example.org"},
	}
	for _, tc := range cases {
		got, err := NormalizeSiteURL(tc.in)
		if err != nil {
			t.Fatalf("NormalizeSiteURL(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeSiteURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHTMLToMarkdownAndMediaExtraction(t *testing.T) {
	raw := `
<h2>Architecture &amp; Design</h2>
<p>Welcome to <strong>Quarry</strong>, an <em>interactive</em> WordPress extractor. Visit <a href="https://example.com/docs">our docs</a>.</p>
<pre><code>quarry export https://example.com --include-media</code></pre>
<ul>
  <li>Fast discovery</li>
  <li>Concurrent downloads</li>
</ul>
<img src="https://example.com/wp-content/uploads/2026/11/diagram.png" alt="System Diagram" />
`
	md := HTMLToMarkdown(raw)
	if !strings.Contains(md, "## Architecture & Design") {
		t.Errorf("expected H2 heading in markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "**Quarry**") || !strings.Contains(md, "[our docs](https://example.com/docs)") {
		t.Errorf("expected bold and link formatting in markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "![System Diagram](https://example.com/wp-content/uploads/2026/11/diagram.png)") {
		t.Errorf("expected image markdown, got:\n%s", md)
	}

	urls := ExtractMediaURLsFromHTML(raw)
	if len(urls) != 1 || urls[0] != "https://example.com/wp-content/uploads/2026/11/diagram.png" {
		t.Errorf("ExtractMediaURLsFromHTML = %v", urls)
	}
}

func TestClientDiscoverAndPagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wp-json/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":        "Acme Engineering Blog",
			"description": "Deep dives into distributed systems",
			"url":         "https://acme.example.com",
			"namespaces":  []string{"wp/v2", "oembed/1.0"},
		})
	})
	mux.HandleFunc("/wp-json/wp/v2/types", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"post": map[string]any{"name": "Posts", "slug": "post", "rest_base": "posts", "rest_namespace": "wp/v2"},
			"page": map[string]any{"name": "Pages", "slug": "page", "rest_base": "pages", "rest_namespace": "wp/v2"},
		})
	})
	mux.HandleFunc("/wp-json/wp/v2/taxonomies", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"category": map[string]any{"name": "Categories", "slug": "category", "rest_base": "categories"},
		})
	})
	mux.HandleFunc("/wp-json/wp/v2/posts", func(w http.ResponseWriter, r *http.Request) {
		pageStr := r.URL.Query().Get("page")
		page := 1
		if pageStr != "" {
			page, _ = strconv.Atoi(pageStr)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-Total", "3")
		w.Header().Set("X-WP-TotalPages", "2")

		if page == 1 {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":      101,
					"date":    "2026-11-10T12:00:00",
					"slug":    "first-post",
					"status":  "publish",
					"type":    "post",
					"title":   map[string]any{"rendered": "First Post &amp; Launch"},
					"content": map[string]any{"rendered": "<p>Hello world</p>"},
				},
				{
					"id":      102,
					"date":    "2026-11-11T12:00:00",
					"slug":    "second-post",
					"status":  "publish",
					"type":    "post",
					"title":   map[string]any{"rendered": "Second Post"},
					"content": map[string]any{"rendered": "<p>Second body</p>"},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":      103,
				"date":    "2026-11-12T12:00:00",
				"slug":    "third-post",
				"status":  "publish",
				"type":    "post",
				"title":   map[string]any{"rendered": "Third Post"},
				"content": map[string]any{"rendered": "<p>Third body</p>"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := DefaultClientOptions()
	opts.RequestsPerSec = 0 // disable rate limiter in unit test
	client, err := NewClient(srv.URL, opts)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx := context.Background()
	prof, err := client.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if prof.Name != "Acme Engineering Blog" {
		t.Errorf("prof.Name = %q, want Acme Engineering Blog", prof.Name)
	}
	if prof.EntityCounts["posts"] != 3 {
		t.Errorf("prof.EntityCounts[posts] = %d, want 3", prof.EntityCounts["posts"])
	}

	allPosts, err := client.FetchAllPosts(ctx, DefaultQueryFilter("posts"), nil)
	if err != nil {
		t.Fatalf("FetchAllPosts failed: %v", err)
	}
	if len(allPosts) != 3 {
		t.Fatalf("len(allPosts) = %d, want 3", len(allPosts))
	}
	if CleanPlainText(allPosts[0].Title.Rendered) != "First Post & Launch" {
		t.Errorf("unexpected first post title: %q", CleanPlainText(allPosts[0].Title.Rendered))
	}
}

func TestExpandMediaFilterZipCSVExcel(t *testing.T) {
	q := QueryFilter{Extensions: []string{"zip", "csv", "excel"}}
	mimes, matcher := ExpandMediaFilter(q)
	if len(mimes) < 5 {
		t.Fatalf("expected expanded MIME types for zip,csv,excel, got %v", mimes)
	}
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	items := []struct {
		item MediaItem
		want bool
	}{
		{MediaItem{ID: 1, MimeType: "application/zip", SourceURL: "https://example.com/uploads/backup.zip"}, true},
		{MediaItem{ID: 2, MimeType: "text/csv", SourceURL: "https://example.com/uploads/report.csv"}, true},
		{MediaItem{ID: 3, MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", SourceURL: "https://example.com/uploads/q4.xlsx"}, true},
		{MediaItem{ID: 4, MimeType: "application/octet-stream", SourceURL: "https://example.com/uploads/legacy.xls"}, true},
		{MediaItem{ID: 5, MimeType: "image/jpeg", SourceURL: "https://example.com/uploads/photo.jpg"}, false},
	}

	for _, tc := range items {
		if got := matcher(tc.item); got != tc.want {
			t.Errorf("matcher(%+v) = %v, want %v", tc.item, got, tc.want)
		}
	}
}

func TestExcludeMediaTypesAndRemainingTypes(t *testing.T) {
	rem := ResolveRemainingMediaTypes("", []string{"image"})
	if len(rem) != 5 {
		t.Fatalf("expected 5 non-image media_type values, got %v", rem)
	}
	for _, r := range rem {
		if r == "image" {
			t.Errorf("did not expect 'image' in remaining types: %v", rem)
		}
	}

	q := QueryFilter{ExcludeMediaTypes: []string{"image"}, ExcludeExtensions: []string{"mp4"}}
	_, matcher := ExpandMediaFilter(q)
	if matcher == nil {
		t.Fatal("expected non-nil exclusion matcher")
	}
	if matcher(MediaItem{ID: 1, MediaType: "image", MimeType: "image/png", SourceURL: "https://example.com/a.png"}) {
		t.Error("expected image/png to be excluded")
	}
	if matcher(MediaItem{ID: 2, MediaType: "video", MimeType: "video/mp4", SourceURL: "https://example.com/v.mp4"}) {
		t.Error("expected .mp4 to be excluded")
	}
	if !matcher(MediaItem{ID: 3, MediaType: "file", MimeType: "application/pdf", SourceURL: "https://example.com/doc.pdf"}) {
		t.Error("expected application/pdf to be included")
	}
}

func TestFetchAllMediaIndependentStreamPagination(t *testing.T) {
	var invalidPageRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/wp-json/wp/v2/media", func(w http.ResponseWriter, r *http.Request) {
		mt := r.URL.Query().Get("media_type")
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		w.Header().Set("Content-Type", "application/json")

		switch mt {
		case "file":
			// 2 pages of files
			w.Header().Set("X-WP-Total", "3")
			w.Header().Set("X-WP-TotalPages", "2")
			if page == 1 {
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": 1, "media_type": "file", "mime_type": "application/zip", "source_url": "https://example.com/1.zip"},
					{"id": 2, "media_type": "file", "mime_type": "application/pdf", "source_url": "https://example.com/2.pdf"},
				})
				return
			}
			if page == 2 {
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": 3, "media_type": "file", "mime_type": "text/csv", "source_url": "https://example.com/3.csv"},
				})
				return
			}
			invalidPageRequests++
			w.WriteHeader(http.StatusBadRequest)
		case "video":
			// Only 1 page of videos! Requesting page 2 here would be a bug.
			w.Header().Set("X-WP-Total", "1")
			w.Header().Set("X-WP-TotalPages", "1")
			if page == 1 {
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": 4, "media_type": "video", "mime_type": "video/mp4", "source_url": "https://example.com/4.mp4"},
				})
				return
			}
			invalidPageRequests++
			w.WriteHeader(http.StatusBadRequest)
		default:
			// 0 items for audio, text, application
			w.Header().Set("X-WP-Total", "0")
			w.Header().Set("X-WP-TotalPages", "0")
			if page > 1 {
				invalidPageRequests++
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	opts := DefaultClientOptions()
	opts.RequestsPerSec = 0
	cli, err := NewClient(srv.URL, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	qf := DefaultQueryFilter("media")
	qf.ExcludeMediaTypes = []string{"image"}

	items, err := cli.FetchAllMedia(context.Background(), qf, nil)
	if err != nil {
		t.Fatalf("FetchAllMedia failed: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 items across disjoint streams, got %d", len(items))
	}
	if invalidPageRequests > 0 {
		t.Errorf("expected 0 out-of-bounds page requests across sub-streams, got %d", invalidPageRequests)
	}
}

func TestPHPPolymorphicJSONResilience(t *testing.T) {
	// Simulates real-world WordPress PHP quirks:
	// - media_details.sizes.<size>.mime_type is `false` (boolean) instead of string
	// - media_details.width / height / filesize are strings ("1200") or `false`
	// - second attachment has `media_details: []` (empty PHP array instead of JSON object)
	rawJSON := `[
		{
			"id": "771",
			"date": "2026-11-10T10:00:00",
			"slug": "broken-plugin-size",
			"status": "inherit",
			"type": "attachment",
			"title": {"rendered": "Broken Size Mime", "protected": 0},
			"author": "5",
			"alt_text": false,
			"media_type": "image",
			"mime_type": "image/jpeg",
			"post": false,
			"source_url": "https://example.com/wp-content/uploads/2026/11/broken.jpg",
			"media_details": {
				"width": "1920",
				"height": false,
				"file": "2026/11/broken.jpg",
				"filesize": "482910",
				"sizes": {
					"sp-found-01": {
						"file": "broken-300x200.jpg",
						"width": "300",
						"height": 200,
						"mime_type": false,
						"source_url": "https://example.com/wp-content/uploads/2026/11/broken-300x200.jpg"
					},
					"corrupt-entry": false
				}
			}
		},
		{
			"id": 772,
			"slug": "empty-array-details",
			"media_type": "file",
			"mime_type": "application/zip",
			"source_url": "https://example.com/wp-content/uploads/2026/11/archive.zip",
			"media_details": []
		}
	]`

	var items FlexSlice[MediaItem]
	if err := json.Unmarshal([]byte(rawJSON), &items); err != nil {
		t.Fatalf("expected polymorphic WP JSON to unmarshal cleanly, got error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 decoded MediaItems, got %d", len(items))
	}
	if items[0].ID != 771 || items[0].MediaDetails.FileSize != 482910 {
		t.Errorf("unexpected item[0] coercion: ID=%d FileSize=%d", items[0].ID, items[0].MediaDetails.FileSize)
	}
	sz, ok := items[0].MediaDetails.Sizes["sp-found-01"]
	if !ok || sz.Width != 300 || sz.MimeType != "" {
		t.Errorf("unexpected size sp-found-01 coercion: %+v", sz)
	}
}

func TestCookieHeaderAuthenticationAndDirectories(t *testing.T) {
	if got := NormalizeCookieValue("Cookie: wordpress_logged_in_abc=123; wp-settings=1"); got != "wordpress_logged_in_abc=123; wp-settings=1" {
		t.Fatalf("NormalizeCookieValue failed: got %q", got)
	}

	const expectedCookie = "wordpress_logged_in_xyz=secret_session; wp_woocommerce_session_1=abc"
	const expectedNonce = "a9b8c7d6e5"
	var sawCookieOnAPI, sawNonceOnAPI, sawCookieOnDir bool

	mux := http.NewServeMux()
	var baseURL string

	// WordPress core rest-nonce endpoint
	mux.HandleFunc("/wp-admin/admin-ajax.php", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") == "rest-nonce" && r.Header.Get("Cookie") == expectedCookie {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(expectedNonce))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("0"))
	})

	mux.HandleFunc("/wp-content/uploads/woocommerce_uploads/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == expectedCookie {
			sawCookieOnDir = true
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Index of /wp-content/uploads/woocommerce_uploads</title></head>
<body><h1>Index of /wp-content/uploads/woocommerce_uploads</h1>
<pre>
<a href="../">Parent Directory</a>
<a href="2026/">2026/</a>                       2026-11-14 01:00    -
<a href="wc-catalog-export.xlsx">wc-catalog-export.xlsx</a>  2026-11-14 01:10  2.5M
<a href="banner.jpg">banner.jpg</a>              2026-11-14 01:11  120K
</pre></body></html>`))
	})

	mux.HandleFunc("/wp-content/uploads/woocommerce_uploads/2026/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Index of /wp-content/uploads/woocommerce_uploads/2026</title></head>
<body><a href="../">Parent Directory</a>
<a href="vip-customers.csv">vip-customers.csv</a> 2026-11-14 01:15 512K
</body></html>`))
	})

	mux.HandleFunc("/wp-content/uploads/edd/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Index of /wp-content/uploads/edd</title></head>
<body><h1>Index of /wp-content/uploads/edd</h1>
<a href="ebook-bundle.zip">ebook-bundle.zip</a> 2026-11-14 01:20 15M
</body></html>`))
	})

	mux.HandleFunc("/wp-json/wc/store/v1/products", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == expectedCookie {
			sawCookieOnAPI = true
		}
		if r.Header.Get("X-WP-Nonce") == expectedNonce {
			sawNonceOnAPI = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-Total", "1")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":          501,
				"name":        "Enterprise Database Dump",
				"slug":        "enterprise-db-dump",
				"permalink":   baseURL + "/product/enterprise-db-dump",
				"description": `<p>Download sample <a href="` + baseURL + `/wp-content/uploads/woocommerce_uploads/sample-data.zip">sample-data.zip</a></p>`,
			},
		})
	})

	mux.HandleFunc("/wp-json/wp/v2/edd-downloads", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-Total", "1")
		w.Header().Set("X-WP-TotalPages", "1")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":      601,
				"slug":    "supermarket-directory",
				"status":  "publish",
				"type":    "download",
				"link":    baseURL + "/downloads/supermarket-directory/",
				"title":   map[string]any{"rendered": "بانک اطلاعات سوپرمارکت ها"},
				"content": map[string]any{"rendered": `<p>Demo file: <a href="` + baseURL + `/wp-content/uploads/edd/2026/02/demo-supermarket.xlsx">demo-supermarket.xlsx</a></p>`},
				"excerpt": map[string]any{"rendered": ""},
			},
		})
	})

	mux.HandleFunc("/wp-json/wp/v2/media", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simulate WordPress Core behavior: ?search=edd only searches post_title/post_content, NOT source_url or _wp_attached_file!
		if s := r.URL.Query().Get("search"); s == "edd" || s == "woocommerce_uploads" {
			w.Header().Set("X-WP-Total", "0")
			w.Header().Set("X-WP-TotalPages", "0")
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		if r.URL.Query().Get("media_type") == "video" {
			w.Header().Set("X-WP-Total", "1")
			w.Header().Set("X-WP-TotalPages", "1")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":         701,
					"slug":       "video-supermarket",
					"status":     "inherit",
					"type":       "attachment",
					"link":       baseURL + "/downloads/supermarket-directory/video-supermarket/",
					"title":      map[string]any{"rendered": "ویدیو بانک اطلاعات سوپرمارکت"},
					"media_type": "video",
					"mime_type":  "video/mp4",
					"post":       601,
					"source_url": baseURL + "/wp-content/uploads/2026/02/Video-Supermarket.mp4",
					"media_details": map[string]any{
						"file":     "2026/02/Video-Supermarket.mp4",
						"filesize": 4194304,
					},
				},
			})
			return
		}
		w.Header().Set("X-WP-Total", "0")
		w.Header().Set("X-WP-TotalPages", "0")
		_ = json.NewEncoder(w).Encode([]any{})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	baseURL = srv.URL

	opts := DefaultClientOptions()
	opts.RequestsPerSec = 0
	opts.CookieHeader = "Cookie: " + expectedCookie
	cli, err := NewClient(srv.URL, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// 1. Test ProbeDirectories
	probes := cli.ProbeDirectories(context.Background())
	if len(probes) < 2 || !probes[0].AutoIndex || !probes[0].APIAvailable || !probes[1].APIAvailable || probes[1].APIEndpoint != "/wp/v2/edd-downloads" {
		t.Fatalf("expected WooCommerce & EDD directory probes to report AutoIndex=true and APIAvailable=true (/wp/v2/edd-downloads), got %+v", probes)
	}

	// 2. Test ScanDirectories with ExcludeMediaTypes=["image"] (should return 6 non-image files: xlsx, csv, zip from EDD autoindex, zip from WC product, xlsx from EDD CPT, mp4 attached to EDD download)
	qf := DefaultQueryFilter("woocommerce")
	qf.DirectoryScopes = []string{"wc", "edd"}
	qf.ExcludeMediaTypes = []string{"image"}

	items, _, err := cli.ScanDirectories(context.Background(), qf, nil)
	if err != nil {
		t.Fatalf("ScanDirectories: %v", err)
	}
	if !sawCookieOnAPI || !sawNonceOnAPI || !sawCookieOnDir {
		t.Errorf("expected Cookie + auto-resolved X-WP-Nonce on REST API (cookie=%v, nonce=%v) and Cookie on directory crawler (%v)",
			sawCookieOnAPI, sawNonceOnAPI, sawCookieOnDir)
	}
	if len(items) != 6 {
		t.Fatalf("expected 6 non-image files across WooCommerce & EDD directories/APIs, got %d: %+v", len(items), items)
	}

	// 3. Test Search filter inside ScanDirectories
	qf.Search = "vip-customers"
	searched, _, err := cli.ScanDirectories(context.Background(), qf, nil)
	if err != nil || len(searched) != 1 || !strings.HasSuffix(searched[0].SourceURL, "vip-customers.csv") {
		t.Fatalf("expected search 'vip-customers' to return 1 CSV item, got %+v (err=%v)", searched, err)
	}
	if searched[0].MediaDetails.FileSize != 512*1024 {
		t.Errorf("expected parsed autoindex size 524288 bytes (512K), got %d", searched[0].MediaDetails.FileSize)
	}
	if searched[0].Date.Time.Year() != 2026 {
		t.Errorf("expected parsed autoindex modification date in 2026, got %v", searched[0].Date.Time)
	}

	// 4. Test FetchPosts fallback for 'product' when /wp/v2/product returns 404
	prodPosts, _, err := cli.FetchPosts(context.Background(), DefaultQueryFilter("product"))
	if err != nil || len(prodPosts) != 1 || prodPosts[0].ID != 501 {
		t.Fatalf("expected FetchPosts('product') to fall back to /wc/store/v1/products, got %+v (err=%v)", prodPosts, err)
	}
}
