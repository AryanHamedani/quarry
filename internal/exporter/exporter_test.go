package exporter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AryanHamedani/quarry/internal/wp"
)

func TestExportPostsAllFormats(t *testing.T) {
	tmp := t.TempDir()
	posts := []wp.Post{
		{
			ID:     42,
			Date:   wp.WPTime{Time: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)},
			Slug:   "engineering-craftsmanship",
			Status: "publish",
			Type:   "post",
			Link:   "https://example.com/engineering-craftsmanship",
			Title:  wp.RenderedField{Rendered: "Engineering Craftsmanship &amp; Reliability"},
			Content: wp.RenderedField{
				Rendered: "<h2>Introduction</h2><p>Clean code and <strong>solid</strong> design.</p>",
			},
		},
	}

	// Markdown
	mdDir := filepath.Join(tmp, "md")
	if _, err := ExportPosts(posts, FormatMarkdown, mdDir, "https://example.com"); err != nil {
		t.Fatalf("ExportPosts(markdown) failed: %v", err)
	}
	mdFile := filepath.Join(mdDir, "2026-11-13-engineering-craftsmanship.md")
	data, err := os.ReadFile(mdFile)
	if err != nil {
		t.Fatalf("reading exported markdown: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "title: Engineering Craftsmanship & Reliability") || !strings.Contains(content, "## Introduction") {
		t.Errorf("unexpected markdown content:\n%s", content)
	}

	// JSON
	jsonFile := filepath.Join(tmp, "posts.json")
	if _, err := ExportPosts(posts, FormatJSON, jsonFile, "https://example.com"); err != nil {
		t.Fatalf("ExportPosts(json) failed: %v", err)
	}
	if st, err := os.Stat(jsonFile); err != nil || st.Size() == 0 {
		t.Errorf("expected non-empty JSON export at %s", jsonFile)
	}

	// CSV
	csvFile := filepath.Join(tmp, "posts.csv")
	if _, err := ExportPosts(posts, FormatCSV, csvFile, "https://example.com"); err != nil {
		t.Fatalf("ExportPosts(csv) failed: %v", err)
	}
	if st, err := os.Stat(csvFile); err != nil || st.Size() == 0 {
		t.Errorf("expected non-empty CSV export at %s", csvFile)
	}
}
