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
	mimes, matcher := ExpandMediaFilter("", []string{"zip", "csv", "excel"})
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
