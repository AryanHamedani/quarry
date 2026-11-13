package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/AryanHamedani/quarry/internal/wp"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing Quarry TUI..."
	}

	if m.showHelp {
		return m.renderHelpModal()
	}

	header := m.renderHeader()
	tabsAndControls := m.renderTabsAndControls()
	body := m.renderSplitBody()
	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, header, tabsAndControls, body, footer)
}

func (m *Model) renderHeader() string {
	brand := m.styles.BrandBadge.Render("⛏ QUARRY")

	var sitePills []string
	for i, s := range m.sites {
		label := strings.TrimPrefix(strings.TrimPrefix(s.RawURL, "https://"), "http://")
		if s.Profile != nil && s.Profile.Name != "" && s.Profile.Name != s.RawURL {
			label = fmt.Sprintf("%s (%s)", truncateStr(s.Profile.Name, 24), label)
		}
		if i == m.activeSite {
			sitePills = append(sitePills, m.styles.SiteBadgeActive.Render("● "+label))
		} else {
			sitePills = append(sitePills, m.styles.SiteBadgeIdle.Render("○ "+label))
		}
	}

	var metaInfo string
	if prof := m.sites[m.activeSite].Profile; prof != nil {
		nsCount := len(prof.Namespaces)
		cptCount := len(prof.PostTypes)
		metaInfo = m.styles.MetricChip.Render(
			fmt.Sprintf("REST API ✓ · %d namespaces · %d post types · %dms",
				nsCount, cptCount, prof.ResponseLatency.Milliseconds()),
		)
	} else if m.sites[m.activeSite].Err != nil {
		metaInfo = lipgloss.NewStyle().Foreground(ColorDanger).Render("API Error: " + truncateStr(m.sites[m.activeSite].Err.Error(), 40))
	} else {
		metaInfo = m.styles.DescChip.Render("Probing /wp-json/...")
	}

	row := lipgloss.JoinHorizontal(lipgloss.Center,
		brand,
		"  ",
		strings.Join(sitePills, " "),
		"  ",
		metaInfo,
	)
	return m.styles.AppHeader.Render(row)
}

func (m *Model) renderTabsAndControls() string {
	var renderedTabs []string
	prof := m.sites[m.activeSite].Profile
	for _, t := range tabLabels {
		label := fmt.Sprintf("%s:%s", t.key, t.label)
		if prof != nil {
			if cnt, ok := prof.EntityCounts[t.endpoint]; ok && cnt > 0 {
				label = fmt.Sprintf("%s:%s (%d)", t.key, t.label, cnt)
			}
		}
		if m.activeTab == t.tab {
			renderedTabs = append(renderedTabs, m.styles.TabActive.Render(label))
		} else {
			renderedTabs = append(renderedTabs, m.styles.TabInactive.Render(label))
		}
	}
	tabLine := strings.Join(renderedTabs, " ")

	// Search & Sort status strip
	var filterParts []string
	filterParts = append(filterParts, fmt.Sprintf("sort:%s/%s", m.query.OrderBy, strings.ToUpper(m.query.Order)))
	if m.query.Search != "" {
		filterParts = append(filterParts, fmt.Sprintf("search:%q", m.query.Search))
	}
	if len(m.query.Categories) > 0 {
		filterParts = append(filterParts, fmt.Sprintf("cat:%v", m.query.Categories))
	}
	if len(m.query.Tags) > 0 {
		filterParts = append(filterParts, fmt.Sprintf("tag:%v", m.query.Tags))
	}
	if len(m.query.Author) > 0 {
		filterParts = append(filterParts, fmt.Sprintf("author:%v", m.query.Author))
	}
	if m.query.After != "" {
		filterParts = append(filterParts, "after:"+m.query.After)
	}
	if m.query.Before != "" {
		filterParts = append(filterParts, "before:"+m.query.Before)
	}
	if m.query.MimeType != "" {
		filterParts = append(filterParts, "mime:"+m.query.MimeType)
	}
	if len(m.query.Extensions) > 0 {
		filterParts = append(filterParts, "ext:"+strings.Join(m.query.Extensions, ","))
	}
	if len(m.query.ExcludeMediaTypes) > 0 {
		filterParts = append(filterParts, "exclude-type:"+strings.Join(m.query.ExcludeMediaTypes, ","))
	}
	if len(m.query.ExcludeExtensions) > 0 {
		filterParts = append(filterParts, "exclude-ext:"+strings.Join(m.query.ExcludeExtensions, ","))
	}
	if len(m.query.DirectoryScopes) > 0 {
		filterParts = append(filterParts, "dir:"+strings.Join(m.query.DirectoryScopes, ","))
	}
	if m.cookieHeader != "" {
		filterParts = append(filterParts, "cookie:✓")
	}
	if len(m.selected) > 0 {
		filterParts = append(filterParts, fmt.Sprintf("selected:%d", len(m.selected)))
	}

	var controlLine string
	if m.focus == FocusSearchInput {
		controlLine = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("Search / ") + m.searchInput.View()
	} else if m.focus == FocusFilterInput {
		controlLine = lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render("Filter f ") + m.filterInput.View()
	} else if m.focus == FocusCookieInput {
		controlLine = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true).Render("Cookie b ") + m.cookieInput.View()
	} else {
		controlLine = m.styles.DescChip.Render("  Active Query: [ " + strings.Join(filterParts, " · ") + " ]")
	}

	return lipgloss.JoinVertical(lipgloss.Left, " "+tabLine, controlLine)
}

func (m *Model) renderSplitBody() string {
	bodyHeight := max(10, m.height-9)
	leftWidth := max(36, (m.width*46)/100)
	rightWidth := max(38, m.width-leftWidth-4)

	leftStyle := m.styles.PanelBlurred
	rightStyle := m.styles.PanelBlurred
	if m.focus == FocusList {
		leftStyle = m.styles.PanelFocused
	} else if m.focus == FocusInspector {
		rightStyle = m.styles.PanelFocused
	}

	leftContent := m.renderLeftList(leftWidth-2, bodyHeight-2)
	leftPane := leftStyle.Width(leftWidth).Height(bodyHeight).Render(leftContent)

	inspModeLabel := "Markdown"
	switch m.inspFormat {
	case InspectHTML:
		inspModeLabel = "Raw HTML"
	case InspectJSON:
		inspModeLabel = "REST JSON"
	}
	rightHeader := m.styles.PanelTitle.Render(fmt.Sprintf("Inspector [%s · press v to switch]", inspModeLabel))
	rightBody := rightHeader + "\n" + m.inspector.View()
	rightPane := rightStyle.Width(rightWidth).Height(bodyHeight).Render(rightBody)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}

func (m *Model) renderLeftList(width, height int) string {
	pageInfo := fmt.Sprintf("Page %d/%d (%d total)", max(1, m.meta.Page), max(1, m.meta.TotalPages), m.meta.TotalItems)
	header := m.styles.PanelTitle.Render(fmt.Sprintf("%-20s", tabLabels[m.activeTab].label)) +
		m.styles.DescChip.Render(pageInfo)

	if m.loading {
		return header + "\n\n  " + m.spin.View() + " " + m.loadingLabel
	}

	maxRows := max(3, height-2)
	var rows []string

	switch m.activeTab {
	case TabPosts, TabPages:
		if len(m.posts) == 0 {
			return header + "\n\n  No items match current query."
		}
		start, end := visibleWindow(m.cursor, len(m.posts), maxRows)
		for i := start; i < end; i++ {
			p := m.posts[i]
			check := " "
			if m.selected[p.ID] {
				check = m.styles.RowChecked.Render("✓")
			}
			title := wp.CleanPlainText(p.Title.Rendered)
			if title == "" {
				title = fmt.Sprintf("(Untitled #%d)", p.ID)
			}
			line := fmt.Sprintf("%s #%-5d %s  %s",
				check, p.ID, p.Date.FormatShort(), truncateStr(title, max(15, width-24)))
			if i == m.cursor {
				rows = append(rows, m.styles.RowSelected.Width(width).Render("▸ "+line))
			} else {
				rows = append(rows, m.styles.RowNormal.Width(width).Render("  "+line))
			}
		}

	case TabMedia, TabCommerceDirs:
		if len(m.media) == 0 {
			if m.activeTab == TabCommerceDirs {
				return header + "\n\n  No WooCommerce / EDD files or products matched (use f -> dir:wc,edd,uploads)."
			}
			return header + "\n\n  No media attachments found."
		}
		start, end := visibleWindow(m.cursor, len(m.media), maxRows)
		for i := start; i < end; i++ {
			item := m.media[i]
			check := " "
			if m.selected[item.ID] {
				check = m.styles.RowChecked.Render("✓")
			}
			title := wp.CleanPlainText(item.Title.Rendered)
			if title == "" {
				title = item.Slug
			}
			line := fmt.Sprintf("%s #%-5d [%-10s] %-9s %s",
				check, item.ID, truncateStr(item.MimeType, 10), item.DimensionsLabel(), truncateStr(title, max(12, width-34)))
			if i == m.cursor {
				rows = append(rows, m.styles.RowSelected.Width(width).Render("▸ "+line))
			} else {
				rows = append(rows, m.styles.RowNormal.Width(width).Render("  "+line))
			}
		}

	case TabCategories, TabTags:
		if len(m.terms) == 0 {
			return header + "\n\n  No taxonomy terms found."
		}
		start, end := visibleWindow(m.cursor, len(m.terms), maxRows)
		for i := start; i < end; i++ {
			t := m.terms[i]
			line := fmt.Sprintf("#%-5d (%4d posts)  %-22s /%s",
				t.ID, t.Count, truncateStr(t.Name, 22), truncateStr(t.Slug, 18))
			if i == m.cursor {
				rows = append(rows, m.styles.RowSelected.Width(width).Render("▸ "+line))
			} else {
				rows = append(rows, m.styles.RowNormal.Width(width).Render("  "+line))
			}
		}

	case TabUsers:
		if len(m.users) == 0 {
			return header + "\n\n  No public users exposed via /wp/v2/users."
		}
		start, end := visibleWindow(m.cursor, len(m.users), maxRows)
		for i := start; i < end; i++ {
			u := m.users[i]
			line := fmt.Sprintf("#%-4d %-24s (@%s)", u.ID, truncateStr(u.Name, 24), truncateStr(u.Slug, 18))
			if i == m.cursor {
				rows = append(rows, m.styles.RowSelected.Width(width).Render("▸ "+line))
			} else {
				rows = append(rows, m.styles.RowNormal.Width(width).Render("  "+line))
			}
		}

	case TabComments:
		if len(m.comments) == 0 {
			return header + "\n\n  No public comments found."
		}
		start, end := visibleWindow(m.cursor, len(m.comments), maxRows)
		for i := start; i < end; i++ {
			c := m.comments[i]
			snippet := wp.CleanPlainText(c.Content.Rendered)
			line := fmt.Sprintf("#%-5d %s %-14s %s",
				c.ID, c.Date.FormatShort(), truncateStr(c.AuthorName, 14), truncateStr(snippet, max(12, width-34)))
			if i == m.cursor {
				rows = append(rows, m.styles.RowSelected.Width(width).Render("▸ "+line))
			} else {
				rows = append(rows, m.styles.RowNormal.Width(width).Render("  "+line))
			}
		}

	case TabCustomTypes:
		prof := m.sites[m.activeSite].Profile
		if prof == nil {
			return header + "\n\n  Discovering site schema..."
		}
		rows = append(rows, m.styles.CategoryChip.Render("Discovered Namespaces:"))
		for _, ns := range prof.Namespaces {
			rows = append(rows, "  • "+ns)
		}
		rows = append(rows, "")
		rows = append(rows, m.styles.CategoryChip.Render("Registered Post Types (/wp/v2/types):"))
		var slugs []string
		for k := range prof.PostTypes {
			slugs = append(slugs, k)
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			pt := prof.PostTypes[slug]
			rows = append(rows, fmt.Sprintf("  • %-14s → /%s/%s (%s)", pt.Slug, pt.RESTNamespace, pt.RESTBase, pt.Name))
		}
		if len(prof.Directories) > 0 {
			rows = append(rows, "")
			rows = append(rows, m.styles.CategoryChip.Render("WooCommerce / EDD / Upload Directories:"))
			for _, d := range prof.Directories {
				rows = append(rows, fmt.Sprintf("  • %-12s HTTP %d (autoindex:%v, api:%v %s)",
					d.Scope, d.HTTPStatus, d.AutoIndex, d.APIAvailable, d.APIEndpoint))
			}
		}
	}

	return header + "\n" + strings.Join(rows, "\n")
}

func (m *Model) refreshInspectorContent() {
	var content string

	switch m.activeTab {
	case TabPosts, TabPages:
		if m.cursor >= 0 && m.cursor < len(m.posts) {
			p := m.posts[m.cursor]
			switch m.inspFormat {
			case InspectJSON:
				b, _ := json.MarshalIndent(p, "", "  ")
				content = string(b)
			case InspectHTML:
				content = fmt.Sprintf("Title: %s\nSlug:  %s\nLink:  %s\n\n%s",
					wp.CleanPlainText(p.Title.Rendered), p.Slug, p.Link, p.Content.Rendered)
			default:
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("# %s\n\n", wp.CleanPlainText(p.Title.Rendered)))
				sb.WriteString(fmt.Sprintf("• ID:         #%d (%s / %s)\n", p.ID, p.Type, p.Status))
				sb.WriteString(fmt.Sprintf("• Published:  %s (Modified: %s)\n", p.Date.FormatFull(), p.Modified.FormatShort()))
				sb.WriteString(fmt.Sprintf("• Author:     %s\n", p.ResolveAuthorName()))
				sb.WriteString(fmt.Sprintf("• Slug:       %s\n", p.Slug))
				sb.WriteString(fmt.Sprintf("• Permalink:  %s\n", p.Link))
				if cats := p.ResolveTermNames("category"); len(cats) > 0 {
					sb.WriteString(fmt.Sprintf("• Categories: %s\n", strings.Join(cats, ", ")))
				}
				if tags := p.ResolveTermNames("post_tag"); len(tags) > 0 {
					sb.WriteString(fmt.Sprintf("• Tags:       %s\n", strings.Join(tags, ", ")))
				}
				if feat := p.ResolveFeaturedURL(); feat != "" {
					sb.WriteString(fmt.Sprintf("• Featured:   %s\n", feat))
				}
				inlineMedia := wp.ExtractMediaURLsFromHTML(p.Content.Rendered)
				if len(inlineMedia) > 0 {
					sb.WriteString(fmt.Sprintf("• Embedded Media Assets: %d files (press 'd' to download)\n", len(inlineMedia)))
				}
				sb.WriteString("\n" + strings.Repeat("─", max(20, m.inspector.Width-4)) + "\n\n")
				md := wp.HTMLToMarkdown(p.Content.Rendered)
				if md == "" {
					md = wp.HTMLToMarkdown(p.Excerpt.Rendered)
				}
				sb.WriteString(wordWrap(md, max(30, m.inspector.Width-4)))
				content = sb.String()
			}
		}

	case TabMedia, TabCommerceDirs:
		if m.cursor >= 0 && m.cursor < len(m.media) {
			item := m.media[m.cursor]
			if m.inspFormat == InspectJSON {
				b, _ := json.MarshalIndent(item, "", "  ")
				content = string(b)
			} else {
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("# Media #%d: %s\n\n", item.ID, wp.CleanPlainText(item.Title.Rendered)))
				sb.WriteString(fmt.Sprintf("• MIME Type:   %s (%s)\n", item.MimeType, item.MediaType))
				sb.WriteString(fmt.Sprintf("• Dimensions:  %s\n", item.DimensionsLabel()))
				if item.MediaDetails.FileSize > 0 {
					sb.WriteString(fmt.Sprintf("• File Size:   %.2f KB\n", float64(item.MediaDetails.FileSize)/1024.0))
				}
				sb.WriteString(fmt.Sprintf("• Uploaded:    %s\n", item.Date.FormatFull()))
				sb.WriteString(fmt.Sprintf("• Alt Text:    %s\n", item.AltText))
				sb.WriteString(fmt.Sprintf("• Source URL:  %s\n\n", item.SourceURL))
				if len(item.MediaDetails.Sizes) > 0 {
					sb.WriteString("## Generated WordPress Image Variants:\n")
					var sizeNames []string
					for k := range item.MediaDetails.Sizes {
						sizeNames = append(sizeNames, k)
					}
					sort.Strings(sizeNames)
					for _, k := range sizeNames {
						sz := item.MediaDetails.Sizes[k]
						sb.WriteString(fmt.Sprintf("  • %-15s %4dx%-4d  %s\n", k, sz.Width, sz.Height, sz.SourceURL))
					}
				}
				sb.WriteString("\nPress 'd' to download original high-resolution asset.")
				content = sb.String()
			}
		}

	case TabCategories, TabTags:
		if m.cursor >= 0 && m.cursor < len(m.terms) {
			t := m.terms[m.cursor]
			if m.inspFormat == InspectJSON {
				b, _ := json.MarshalIndent(t, "", "  ")
				content = string(b)
			} else {
				content = fmt.Sprintf("# %s (%s)\n\n• ID:          #%d\n• Taxonomy:    %s\n• Slug:        %s\n• Post Count:  %d\n• Link:        %s\n\nDescription:\n%s\n\nTip: Press Enter on this row to filter Posts by %q!",
					t.Name, t.Taxonomy, t.ID, t.Taxonomy, t.Slug, t.Count, t.Link, t.Description, t.Name)
			}
		}

	case TabUsers:
		if m.cursor >= 0 && m.cursor < len(m.users) {
			u := m.users[m.cursor]
			if m.inspFormat == InspectJSON {
				b, _ := json.MarshalIndent(u, "", "  ")
				content = string(b)
			} else {
				content = fmt.Sprintf("# Author: %s (@%s)\n\n• ID:    #%d\n• Slug:  %s\n• Link:  %s\n• URL:   %s\n\nBio:\n%s\n\nTip: Press Enter on this author to view all posts by %s!",
					u.Name, u.Slug, u.ID, u.Slug, u.Link, u.URL, u.Description, u.Name)
			}
		}

	case TabComments:
		if m.cursor >= 0 && m.cursor < len(m.comments) {
			c := m.comments[m.cursor]
			if m.inspFormat == InspectJSON {
				b, _ := json.MarshalIndent(c, "", "  ")
				content = string(b)
			} else {
				content = fmt.Sprintf("# Comment #%d on Post #%d\n\n• Author: %s (%s)\n• Date:   %s\n• Link:   %s\n\n%s",
					c.ID, c.Post, c.AuthorName, c.AuthorURL, c.Date.FormatFull(), c.Link, wp.HTMLToMarkdown(c.Content.Rendered))
			}
		}

	case TabCustomTypes:
		prof := m.sites[m.activeSite].Profile
		if prof != nil {
			b, _ := json.MarshalIndent(prof, "", "  ")
			content = string(b)
		}
	}

	m.inspector.SetContent(content)
	m.inspector.GotoTop()
}

func (m *Model) renderFooter() string {
	statusStyle := m.styles.StatusBar
	if m.isError {
		statusStyle = statusStyle.Foreground(ColorDanger)
	} else {
		statusStyle = statusStyle.Foreground(ColorAccent)
	}

	statusText := m.statusMsg
	if statusText == "" {
		statusText = "Ready."
	}

	keys := []struct{ k, d string }{
		{"1-9", "tabs"},
		{"/", "search"},
		{"f", "filter"},
		{"b", "cookie"},
		{"s/o", "sort"},
		{"n/p", "page"},
		{"space/a", "select"},
		{"x/J/C", "export"},
		{"D", "size"},
		{"d", "download"},
		{"v", "view"},
		{"?", "help"},
		{"q", "quit"},
	}
	var keyChips []string
	for _, kd := range keys {
		keyChips = append(keyChips, m.styles.KeyChip.Render(kd.k)+" "+m.styles.DescChip.Render(kd.d))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		statusStyle.Render(" "+statusText),
		" "+strings.Join(keyChips, " · "),
	)
}

func (m *Model) renderHelpModal() string {
	helpText := `⛏  QUARRY — KEYBOARD SHORTCUTS & FILTER GUIDE

NAVIGATION & TABS
  1..9          Switch tabs (Posts, Pages, Media, Categories, Tags, Users, Comments, Schema, WC/EDD Dirs)
  j / k / ↑ / ↓ Move cursor up / down in list or scroll inspector
  g / G         Jump to top / bottom of current page
  Tab / l / h   Switch focus between Resource List and Content Inspector
  n / p (] / [) Next page / Previous page via WordPress REST API pagination
  S             Cycle active WordPress site (when multiple URLs provided)
  Enter         Drill down (on Category/Tag/User → filters Posts) or focus Inspector

SEARCH, FILTER, COOKIE AUTH & SORT
  /             Full-text keyword search across current endpoint or WC/EDD directory
  f             Advanced filter DSL (ext:zip,csv,excel exclude-type:image dir:wc,edd,uploads after:2025-01-01)
  b             Paste raw Cookie header value for authenticated scraping & downloads
  s             Cycle sort field (date, modified, title, id, slug, count, name)
  o             Toggle sort direction (DESC / ASC)
  Esc           Clear search filter or close modal

SELECTION, EXTRACTION & MEDIA DOWNLOAD
  Space         Toggle selection on current item
  a             Select / Deselect all items on current page
  x             Export selected (or current) items to Markdown with YAML frontmatter
  J             Export selected (or current) items to formatted JSON
  C             Export selected (or current) items to CSV
  D             Estimate total download size before downloading (via WP metadata + HTTP HEAD)
  d             Download media files (featured + embedded images/files) via worker pool
  v             Cycle Inspector rendering mode (Markdown → Raw HTML → REST JSON)

Press ? or Esc to return to the workspace.`

	box := m.styles.ModalBox.Render(helpText)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func visibleWindow(cursor, total, maxRows int) (int, int) {
	if total <= maxRows {
		return 0, total
	}
	half := maxRows / 2
	start := cursor - half
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > total {
		end = total
		start = max(0, end-maxRows)
	}
	return start, end
}

func truncateStr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-1]) + "…"
}

func wordWrap(text string, width int) string {
	if width <= 10 {
		return text
	}
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if len(line) <= width || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "  •") {
			out.WriteString(line + "\n")
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			out.WriteString("\n")
			continue
		}
		curr := words[0]
		for _, w := range words[1:] {
			if len(curr)+1+len(w) > width {
				out.WriteString(curr + "\n")
				curr = w
			} else {
				curr += " " + w
			}
		}
		out.WriteString(curr + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}
