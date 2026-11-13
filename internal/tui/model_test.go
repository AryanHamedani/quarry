package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AryanHamedani/quarry/internal/wp"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIModelRenderingAndNavigation(t *testing.T) {
	m, err := NewModel(context.Background(), []string{"https://wordpress.org/news"}, wp.DefaultClientOptions(), t.TempDir(), 4)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}

	// Simulate window size
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	m = updated.(*Model)

	// Simulate site discovery & data load
	m.Update(siteDiscoveredMsg{
		siteIdx: 0,
		profile: &wp.SiteProfile{
			Name:            "WordPress News",
			URL:             "https://wordpress.org/news",
			Namespaces:      []string{"wp/v2", "oembed/1.0"},
			ResponseLatency: 42 * time.Millisecond,
			EntityCounts:    map[string]int{"posts": 128, "pages": 12, "media": 640},
		},
	})

	m.Update(dataLoadedMsg{
		siteIdx: 0,
		tab:     TabPosts,
		posts: []wp.Post{
			{
				ID:      901,
				Date:    wp.WPTime{Time: time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)},
				Slug:    "wordpress-6-9-released",
				Status:  "publish",
				Type:    "post",
				Link:    "https://wordpress.org/news/2026/11/wordpress-6-9-released/",
				Title:   wp.RenderedField{Rendered: "WordPress 6.9 Released"},
				Content: wp.RenderedField{Rendered: "<h2>Highlights</h2><p>Faster REST API and block bindings.</p>"},
			},
			{
				ID:      902,
				Date:    wp.WPTime{Time: time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)},
				Slug:    "state-of-the-word",
				Status:  "publish",
				Type:    "post",
				Link:    "https://wordpress.org/news/2026/11/state-of-the-word/",
				Title:   wp.RenderedField{Rendered: "State of the Word 2026"},
				Content: wp.RenderedField{Rendered: "<p>Annual keynote announcement.</p>"},
			},
		},
		meta: wp.PageMeta{
			Page:       1,
			PerPage:    25,
			TotalItems: 128,
			TotalPages: 6,
			Latency:    45 * time.Millisecond,
		},
	})

	view := m.View()
	if !strings.Contains(view, "QUARRY") || !strings.Contains(view, "WordPress 6.9 Released") {
		t.Errorf("expected TUI view to render header and post list, got:\n%s", view)
	}

	// Move cursor down and toggle selection
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 1 {
		t.Errorf("expected cursor=1 after 'j', got %d", m.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if !m.selected[902] {
		t.Errorf("expected post 902 to be selected after Space")
	}
}
