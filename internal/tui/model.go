package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/AryanHamedani/quarry/internal/downloader"
	"github.com/AryanHamedani/quarry/internal/exporter"
	"github.com/AryanHamedani/quarry/internal/wp"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ResourceTab int

const (
	TabPosts ResourceTab = iota
	TabPages
	TabMedia
	TabCategories
	TabTags
	TabUsers
	TabComments
	TabCustomTypes
	TabCommerceDirs
)

var tabLabels = []struct {
	tab      ResourceTab
	key      string
	label    string
	endpoint string
}{
	{TabPosts, "1", "Posts", "posts"},
	{TabPages, "2", "Pages", "pages"},
	{TabMedia, "3", "Media", "media"},
	{TabCategories, "4", "Categories", "categories"},
	{TabTags, "5", "Tags", "tags"},
	{TabUsers, "6", "Users", "users"},
	{TabComments, "7", "Comments", "comments"},
	{TabCustomTypes, "8", "Schema/CPTs", "types"},
	{TabCommerceDirs, "9", "WC/EDD Dirs", "woocommerce"},
}

type FocusPane int

const (
	FocusList FocusPane = iota
	FocusInspector
	FocusSearchInput
	FocusFilterInput
	FocusCookieInput
)

type InspectorFormat int

const (
	InspectMarkdown InspectorFormat = iota
	InspectHTML
	InspectJSON
)

// SiteTarget holds state for a single WordPress site in the multi-site switcher.
type SiteTarget struct {
	RawURL  string
	Client  *wp.Client
	Profile *wp.SiteProfile
	Err     error
}

// Messages for async Bubble Tea commands
type siteDiscoveredMsg struct {
	siteIdx int
	profile *wp.SiteProfile
	err     error
}

type dataLoadedMsg struct {
	siteIdx  int
	tab      ResourceTab
	posts    []wp.Post
	media    []wp.MediaItem
	terms    []wp.TermItem
	users    []wp.User
	comments []wp.Comment
	meta     wp.PageMeta
	err      error
}

type exportFinishedMsg struct {
	description string
	outputPath  string
	err         error
}

type downloadProgressMsg struct {
	update downloader.ProgressUpdate
}

type downloadFinishedMsg struct {
	summary downloader.Summary
	outDir  string
	err     error
}

type sizeEstimatedMsg struct {
	estimate downloader.SizeEstimate
	err      error
}

// Model is the root Bubble Tea state machine for Quarry's interactive TUI.
type Model struct {
	ctx        context.Context
	styles     Styles
	sites      []SiteTarget
	activeSite int
	activeTab  ResourceTab
	focus      FocusPane
	inspFormat InspectorFormat

	width  int
	height int

	loading      bool
	loadingLabel string
	statusMsg    string
	isError      bool

	query wp.QueryFilter
	meta  wp.PageMeta

	posts    []wp.Post
	media    []wp.MediaItem
	terms    []wp.TermItem
	users    []wp.User
	comments []wp.Comment

	cursor   int
	selected map[int]bool // selected item IDs in current tab

	searchInput textinput.Model
	filterInput textinput.Model
	cookieInput textinput.Model
	inspector   viewport.Model
	spin        spinner.Model

	showHelp     bool
	outputDir    string
	concurrent   int
	cookieHeader string
}

// NewModel creates a new Quarry TUI model for one or more WordPress sites.
func NewModel(ctx context.Context, urls []string, clientOpts wp.ClientOptions, outDir string, concurrency int) (*Model, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("at least one WordPress site URL is required")
	}
	if outDir == "" {
		outDir = "./quarry-output"
	}
	if concurrency <= 0 {
		concurrency = 6
	}

	sites := make([]SiteTarget, 0, len(urls))
	for _, u := range urls {
		cli, err := wp.NewClient(u, clientOpts)
		if err != nil {
			return nil, err
		}
		sites = append(sites, SiteTarget{
			RawURL: cli.BaseURL(),
			Client: cli,
		})
	}

	si := textinput.New()
	si.Placeholder = "Search keyword (press Enter to run, Esc to cancel)..."
	si.CharLimit = 120
	si.Width = 42

	fi := textinput.New()
	fi.Placeholder = "Filter: ext:zip,csv,excel exclude-type:image dir:wc,edd after:2025-01-01 per_page:50"
	fi.CharLimit = 240
	fi.Width = 65

	ci := textinput.New()
	ci.Placeholder = "Paste raw Cookie header value (e.g. wordpress_logged_in_...=...; wp_woocommerce_session_...=...)"
	ci.CharLimit = 4096
	ci.Width = 68
	if clientOpts.CookieHeader != "" {
		ci.SetValue(wp.NormalizeCookieValue(clientOpts.CookieHeader))
	}

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(ColorPrimary)

	vp := viewport.New(50, 20)

	m := &Model{
		ctx:          ctx,
		styles:       DefaultStyles(),
		sites:        sites,
		activeSite:   0,
		activeTab:    TabPosts,
		focus:        FocusList,
		inspFormat:   InspectMarkdown,
		loading:      true,
		loadingLabel: "Discovering WordPress REST API & fetching posts...",
		query:        wp.DefaultQueryFilter("posts"),
		selected:     make(map[int]bool),
		searchInput:  si,
		filterInput:  fi,
		cookieInput:  ci,
		inspector:    vp,
		spin:         sp,
		outputDir:    outDir,
		concurrent:   concurrency,
		cookieHeader: wp.NormalizeCookieValue(clientOpts.CookieHeader),
	}
	return m, nil
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spin.Tick,
		m.discoverSiteCmd(m.activeSite),
		m.fetchCurrentTabCmd(),
	}
	return tea.Batch(cmds...)
}

func (m *Model) discoverSiteCmd(siteIdx int) tea.Cmd {
	client := m.sites[siteIdx].Client
	ctx := m.ctx
	return func() tea.Msg {
		prof, err := client.Discover(ctx)
		return siteDiscoveredMsg{siteIdx: siteIdx, profile: prof, err: err}
	}
}

func (m *Model) fetchCurrentTabCmd() tea.Cmd {
	siteIdx := m.activeSite
	client := m.sites[siteIdx].Client
	tab := m.activeTab
	q := m.query
	ctx := m.ctx

	return func() tea.Msg {
		switch tab {
		case TabPosts:
			q.Endpoint = "posts"
			items, meta, err := client.FetchPosts(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, posts: items, meta: meta, err: err}
		case TabPages:
			q.Endpoint = "pages"
			items, meta, err := client.FetchPosts(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, posts: items, meta: meta, err: err}
		case TabMedia:
			items, meta, err := client.FetchMedia(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, media: items, meta: meta, err: err}
		case TabCategories:
			q.Endpoint = "categories"
			items, meta, err := client.FetchTerms(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, terms: items, meta: meta, err: err}
		case TabTags:
			q.Endpoint = "tags"
			items, meta, err := client.FetchTerms(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, terms: items, meta: meta, err: err}
		case TabUsers:
			items, meta, err := client.FetchUsers(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, users: items, meta: meta, err: err}
		case TabComments:
			items, meta, err := client.FetchComments(ctx, q)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, comments: items, meta: meta, err: err}
		case TabCustomTypes:
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab}
		case TabCommerceDirs:
			items, meta, err := client.ScanDirectories(ctx, q, nil)
			return dataLoadedMsg{siteIdx: siteIdx, tab: tab, media: items, meta: meta, err: err}
		}
		return dataLoadedMsg{siteIdx: siteIdx, tab: tab}
	}
}

func (m *Model) exportCurrentSelectionCmd(format exporter.Format) tea.Cmd {
	siteURL := m.sites[m.activeSite].RawURL
	siteHost := exporter.SafeSlug(strings.TrimPrefix(strings.TrimPrefix(siteURL, "https://"), "http://"), 1)
	outBase := filepath.Join(m.outputDir, siteHost)
	tab := m.activeTab

	// Collect selected or all current page items
	postsToExport := m.selectedOrCurrentPosts()
	mediaToExport := m.selectedOrCurrentMedia()

	return func() tea.Msg {
		if tab == TabMedia || tab == TabCommerceDirs {
			out, err := exporter.ExportMediaCatalog(mediaToExport, format, filepath.Join(outBase, "media-catalog."+string(format)))
			return exportFinishedMsg{
				description: fmt.Sprintf("Exported %d media/directory records (%s)", len(mediaToExport), format),
				outputPath:  out,
				err:         err,
			}
		}
		if len(postsToExport) == 0 {
			return exportFinishedMsg{err: fmt.Errorf("no posts/pages available to export on this tab")}
		}
		var target string
		if format == exporter.FormatMarkdown || format == exporter.FormatHTML {
			target = filepath.Join(outBase, string(format))
		} else {
			target = filepath.Join(outBase, fmt.Sprintf("export-%d.%s", time.Now().Unix(), format))
		}
		out, err := exporter.ExportPosts(postsToExport, format, target, siteURL)
		return exportFinishedMsg{
			description: fmt.Sprintf("Exported %d items as %s", len(postsToExport), strings.ToUpper(string(format))),
			outputPath:  out,
			err:         err,
		}
	}
}

func (m *Model) downloadMediaCmd() tea.Cmd {
	siteURL := m.sites[m.activeSite].RawURL
	siteHost := exporter.SafeSlug(strings.TrimPrefix(strings.TrimPrefix(siteURL, "https://"), "http://"), 1)
	mediaDir := filepath.Join(m.outputDir, siteHost, "media")
	ctx := m.ctx
	conc := m.concurrent
	tasks := m.collectMediaTasksForSelection()

	return func() tea.Msg {
		if len(tasks) == 0 {
			return downloadFinishedMsg{err: fmt.Errorf("no downloadable media URLs found in current selection")}
		}
		opts := downloader.DefaultPoolOptions(mediaDir)
		opts.Concurrency = conc
		opts.CookieHeader = m.cookieHeader
		summary, err := downloader.DownloadBatch(ctx, tasks, opts, nil)
		return downloadFinishedMsg{
			summary: summary,
			outDir:  mediaDir,
			err:     err,
		}
	}
}

func (m *Model) estimateMediaSizeCmd() tea.Cmd {
	ctx := m.ctx
	conc := m.concurrent
	tasks := m.collectMediaTasksForSelection()

	return func() tea.Msg {
		if len(tasks) == 0 {
			return sizeEstimatedMsg{err: fmt.Errorf("no media URLs found in current selection")}
		}
		opts := downloader.DefaultPoolOptions(m.outputDir)
		opts.Concurrency = conc
		opts.CookieHeader = m.cookieHeader
		est := downloader.EstimateBatchSize(ctx, tasks, opts, true, nil)
		return sizeEstimatedMsg{estimate: est}
	}
}

func (m *Model) collectMediaTasksForSelection() []downloader.AssetTask {
	var tasks []downloader.AssetTask
	switch m.activeTab {
	case TabMedia, TabCommerceDirs:
		items := m.selectedOrCurrentMedia()
		for _, item := range items {
			if item.SourceURL != "" {
				tasks = append(tasks, downloader.AssetTask{
					ID:        item.ID,
					SourceURL: item.SourceURL,
					Title:     wp.CleanPlainText(item.Title.Rendered),
					MimeType:  item.MimeType,
					KnownSize: item.MediaDetails.FileSize,
					Date:      item.Date.Time,
				})
			}
		}
	case TabPosts, TabPages:
		posts := m.selectedOrCurrentPosts()
		for _, p := range posts {
			if feat := p.ResolveFeaturedURL(); feat != "" {
				tasks = append(tasks, downloader.AssetTask{
					ID:        p.FeaturedMedia,
					SourceURL: feat,
					Title:     wp.CleanPlainText(p.Title.Rendered),
					Date:      p.Date.Time,
				})
			}
			for _, u := range wp.ExtractMediaURLsFromHTML(p.Content.Rendered) {
				tasks = append(tasks, downloader.AssetTask{
					SourceURL: u,
					Date:      p.Date.Time,
				})
			}
		}
	}
	return tasks
}

func (m *Model) selectedOrCurrentPosts() []wp.Post {
	if len(m.posts) == 0 {
		return nil
	}
	var picked []wp.Post
	for _, p := range m.posts {
		if m.selected[p.ID] {
			picked = append(picked, p)
		}
	}
	if len(picked) > 0 {
		return picked
	}
	if m.cursor >= 0 && m.cursor < len(m.posts) {
		return []wp.Post{m.posts[m.cursor]}
	}
	return m.posts
}

func (m *Model) selectedOrCurrentMedia() []wp.MediaItem {
	if len(m.media) == 0 {
		return nil
	}
	var picked []wp.MediaItem
	for _, item := range m.media {
		if m.selected[item.ID] {
			picked = append(picked, item)
		}
	}
	if len(picked) > 0 {
		return picked
	}
	if m.cursor >= 0 && m.cursor < len(m.media) {
		return []wp.MediaItem{m.media[m.cursor]}
	}
	return m.media
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		m.refreshInspectorContent()
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case siteDiscoveredMsg:
		if msg.siteIdx >= 0 && msg.siteIdx < len(m.sites) {
			m.sites[msg.siteIdx].Profile = msg.profile
			m.sites[msg.siteIdx].Err = msg.err
		}
		if msg.siteIdx == m.activeSite && m.activeTab == TabCustomTypes {
			m.refreshInspectorContent()
		}
		return m, nil

	case dataLoadedMsg:
		if msg.siteIdx != m.activeSite || msg.tab != m.activeTab {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.isError = true
			m.statusMsg = fmt.Sprintf("API Error: %v", msg.err)
			return m, nil
		}
		m.isError = false
		m.posts = msg.posts
		m.media = msg.media
		m.terms = msg.terms
		m.users = msg.users
		m.comments = msg.comments
		m.meta = msg.meta
		m.cursor = 0
		m.statusMsg = fmt.Sprintf("Loaded page %d/%d (%d total items in %dms)",
			max(1, m.meta.Page), max(1, m.meta.TotalPages), m.meta.TotalItems, m.meta.Latency.Milliseconds())
		m.refreshInspectorContent()
		return m, nil

	case exportFinishedMsg:
		m.loading = false
		if msg.err != nil {
			m.isError = true
			m.statusMsg = fmt.Sprintf("Export failed: %v", msg.err)
		} else {
			m.isError = false
			m.statusMsg = fmt.Sprintf("✓ %s → %s", msg.description, msg.outputPath)
		}
		return m, nil

	case downloadFinishedMsg:
		m.loading = false
		if msg.err != nil {
			m.isError = true
			m.statusMsg = fmt.Sprintf("Media download error: %v", msg.err)
		} else {
			m.isError = false
			mb := float64(msg.summary.TotalBytes) / (1024 * 1024)
			m.statusMsg = fmt.Sprintf("✓ Downloaded %d files (%d skipped, %d failed, %.2f MB) → %s",
				msg.summary.Succeeded, msg.summary.Skipped, msg.summary.Failed, mb, msg.outDir)
		}
		return m, nil

	case sizeEstimatedMsg:
		m.loading = false
		if msg.err != nil {
			m.isError = true
			m.statusMsg = fmt.Sprintf("Size estimate error: %v", msg.err)
		} else {
			m.isError = false
			est := msg.estimate
			m.statusMsg = fmt.Sprintf("Σ Download Estimate: %s across %d files (%d known, %d unknown size)",
				downloader.FormatBytes(est.TotalBytes), est.TotalFiles, est.KnownSizeFiles, est.UnknownFiles)
		}
		return m, nil

	case tea.KeyMsg:
		// Handle search input mode
		if m.focus == FocusSearchInput {
			switch msg.String() {
			case "esc":
				m.focus = FocusList
				m.searchInput.Blur()
				return m, nil
			case "enter":
				m.focus = FocusList
				m.searchInput.Blur()
				m.query.Search = strings.TrimSpace(m.searchInput.Value())
				m.query.Page = 1
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Searching %q...", m.query.Search)
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			default:
				var cmd tea.Cmd
				m.searchInput, cmd = m.searchInput.Update(msg)
				return m, cmd
			}
		}

		// Handle advanced filter input mode
		if m.focus == FocusFilterInput {
			switch msg.String() {
			case "esc":
				m.focus = FocusList
				m.filterInput.Blur()
				return m, nil
			case "enter":
				m.focus = FocusList
				m.filterInput.Blur()
				m.applyFilterDSL(m.filterInput.Value())
				m.query.Page = 1
				m.loading = true
				m.loadingLabel = "Applying filters..."
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			default:
				var cmd tea.Cmd
				m.filterInput, cmd = m.filterInput.Update(msg)
				return m, cmd
			}
		}

		// Handle Cookie header input mode
		if m.focus == FocusCookieInput {
			switch msg.String() {
			case "esc":
				m.focus = FocusList
				m.cookieInput.Blur()
				return m, nil
			case "enter":
				m.focus = FocusList
				m.cookieInput.Blur()
				m.cookieHeader = wp.NormalizeCookieValue(m.cookieInput.Value())
				for i := range m.sites {
					m.sites[i].Client.SetCookieHeader(m.cookieHeader)
				}
				m.query.Page = 1
				m.loading = true
				if m.cookieHeader != "" {
					m.loadingLabel = "Applied Cookie header — refreshing authenticated view..."
				} else {
					m.loadingLabel = "Cleared Cookie header — refreshing view..."
				}
				return m, tea.Batch(m.spin.Tick, m.discoverSiteCmd(m.activeSite), m.fetchCurrentTabCmd())
			default:
				var cmd tea.Cmd
				m.cookieInput, cmd = m.cookieInput.Update(msg)
				return m, cmd
			}
		}

		// Global shortcuts
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "esc":
			if m.showHelp {
				m.showHelp = false
				return m, nil
			}
			if m.query.Search != "" {
				m.query.Search = ""
				m.searchInput.SetValue("")
				m.query.Page = 1
				m.loading = true
				m.loadingLabel = "Cleared search filter..."
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			}
		case "/":
			m.focus = FocusSearchInput
			m.searchInput.Focus()
			return m, textinput.Blink
		case "f":
			m.focus = FocusFilterInput
			m.filterInput.Focus()
			return m, textinput.Blink
		case "b":
			m.focus = FocusCookieInput
			m.cookieInput.Focus()
			return m, textinput.Blink
		case "tab", "l", "right":
			if m.focus == FocusList {
				m.focus = FocusInspector
			} else {
				m.focus = FocusList
			}
			return m, nil
		case "h", "left":
			m.focus = FocusList
			return m, nil
		case "S":
			if len(m.sites) > 1 {
				m.activeSite = (m.activeSite + 1) % len(m.sites)
				m.selected = make(map[int]bool)
				m.query.Page = 1
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Switching to %s...", m.sites[m.activeSite].RawURL)
				cmds = append(cmds, m.spin.Tick, m.fetchCurrentTabCmd())
				if m.sites[m.activeSite].Profile == nil {
					cmds = append(cmds, m.discoverSiteCmd(m.activeSite))
				}
				return m, tea.Batch(cmds...)
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx >= 0 && idx < len(tabLabels) {
				newTab := tabLabels[idx].tab
				if newTab != m.activeTab {
					m.activeTab = newTab
					m.query = wp.DefaultQueryFilter(tabLabels[idx].endpoint)
					m.searchInput.SetValue("")
					m.cursor = 0
					if newTab == TabCustomTypes {
						m.loading = false
						m.refreshInspectorContent()
						return m, nil
					}
					m.loading = true
					m.loadingLabel = fmt.Sprintf("Loading %s...", tabLabels[idx].label)
					return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
				}
			}
		case "s":
			m.cycleOrderBy()
			m.query.Page = 1
			m.loading = true
			m.loadingLabel = fmt.Sprintf("Sorting by %s (%s)...", m.query.OrderBy, m.query.Order)
			return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
		case "o":
			if m.query.Order == "desc" {
				m.query.Order = "asc"
			} else {
				m.query.Order = "desc"
			}
			m.query.Page = 1
			m.loading = true
			m.loadingLabel = fmt.Sprintf("Order %s (%s)...", m.query.OrderBy, strings.ToUpper(m.query.Order))
			return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
		case "n", "]":
			if m.meta.Page < m.meta.TotalPages {
				m.query.Page++
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Fetching page %d/%d...", m.query.Page, m.meta.TotalPages)
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			}
		case "p", "[":
			if m.query.Page > 1 {
				m.query.Page--
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Fetching page %d/%d...", m.query.Page, m.meta.TotalPages)
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			}
		case "r":
			m.loading = true
			m.loadingLabel = "Refreshing current view..."
			return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
		case "v":
			m.inspFormat = (m.inspFormat + 1) % 3
			m.refreshInspectorContent()
			return m, nil
		case " ":
			m.toggleCurrentItemSelection()
			return m, nil
		case "a":
			m.toggleSelectAllOnPage()
			return m, nil
		case "x":
			m.loading = true
			m.loadingLabel = "Exporting to Markdown..."
			return m, tea.Batch(m.spin.Tick, m.exportCurrentSelectionCmd(exporter.FormatMarkdown))
		case "J":
			m.loading = true
			m.loadingLabel = "Exporting to JSON..."
			return m, tea.Batch(m.spin.Tick, m.exportCurrentSelectionCmd(exporter.FormatJSON))
		case "C":
			m.loading = true
			m.loadingLabel = "Exporting to CSV..."
			return m, tea.Batch(m.spin.Tick, m.exportCurrentSelectionCmd(exporter.FormatCSV))
		case "d":
			m.loading = true
			m.loadingLabel = "Downloading media assets in background worker pool..."
			return m, tea.Batch(m.spin.Tick, m.downloadMediaCmd())
		case "D":
			m.loading = true
			m.loadingLabel = "Calculating total download size (checking metadata & HTTP HEAD)..."
			return m, tea.Batch(m.spin.Tick, m.estimateMediaSizeCmd())
		}

		// Navigation inside focused pane
		if m.focus == FocusInspector {
			var cmd tea.Cmd
			m.inspector, cmd = m.inspector.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "j", "down":
			if m.cursor < m.currentListLen()-1 {
				m.cursor++
				m.refreshInspectorContent()
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
				m.refreshInspectorContent()
			}
		case "g", "home":
			m.cursor = 0
			m.refreshInspectorContent()
		case "G", "end":
			if l := m.currentListLen(); l > 0 {
				m.cursor = l - 1
				m.refreshInspectorContent()
			}
		case "enter":
			// On Categories or Tags tab, pressing Enter filters Posts by that category/tag!
			if m.activeTab == TabCategories && m.cursor < len(m.terms) {
				cat := m.terms[m.cursor]
				m.activeTab = TabPosts
				m.query = wp.DefaultQueryFilter("posts")
				m.query.Categories = []int{cat.ID}
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Filtering posts by category %q (#%d)...", cat.Name, cat.ID)
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			}
			if m.activeTab == TabTags && m.cursor < len(m.terms) {
				tag := m.terms[m.cursor]
				m.activeTab = TabPosts
				m.query = wp.DefaultQueryFilter("posts")
				m.query.Tags = []int{tag.ID}
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Filtering posts by tag %q (#%d)...", tag.Name, tag.ID)
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			}
			if m.activeTab == TabUsers && m.cursor < len(m.users) {
				u := m.users[m.cursor]
				m.activeTab = TabPosts
				m.query = wp.DefaultQueryFilter("posts")
				m.query.Author = []int{u.ID}
				m.loading = true
				m.loadingLabel = fmt.Sprintf("Filtering posts by author %q (#%d)...", u.Name, u.ID)
				return m, tea.Batch(m.spin.Tick, m.fetchCurrentTabCmd())
			}
			m.focus = FocusInspector
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) currentListLen() int {
	switch m.activeTab {
	case TabPosts, TabPages:
		return len(m.posts)
	case TabMedia, TabCommerceDirs:
		return len(m.media)
	case TabCategories, TabTags:
		return len(m.terms)
	case TabUsers:
		return len(m.users)
	case TabComments:
		return len(m.comments)
	default:
		return 0
	}
}

func (m *Model) toggleCurrentItemSelection() {
	id := m.currentItemID()
	if id <= 0 {
		return
	}
	if m.selected[id] {
		delete(m.selected, id)
	} else {
		m.selected[id] = true
	}
}

func (m *Model) toggleSelectAllOnPage() {
	var ids []int
	switch m.activeTab {
	case TabPosts, TabPages:
		for _, p := range m.posts {
			ids = append(ids, p.ID)
		}
	case TabMedia, TabCommerceDirs:
		for _, item := range m.media {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	allSelected := true
	for _, id := range ids {
		if !m.selected[id] {
			allSelected = false
			break
		}
	}
	for _, id := range ids {
		if allSelected {
			delete(m.selected, id)
		} else {
			m.selected[id] = true
		}
	}
}

func (m *Model) currentItemID() int {
	switch m.activeTab {
	case TabPosts, TabPages:
		if m.cursor >= 0 && m.cursor < len(m.posts) {
			return m.posts[m.cursor].ID
		}
	case TabMedia, TabCommerceDirs:
		if m.cursor >= 0 && m.cursor < len(m.media) {
			return m.media[m.cursor].ID
		}
	case TabCategories, TabTags:
		if m.cursor >= 0 && m.cursor < len(m.terms) {
			return m.terms[m.cursor].ID
		}
	case TabUsers:
		if m.cursor >= 0 && m.cursor < len(m.users) {
			return m.users[m.cursor].ID
		}
	case TabComments:
		if m.cursor >= 0 && m.cursor < len(m.comments) {
			return m.comments[m.cursor].ID
		}
	}
	return 0
}

func (m *Model) cycleOrderBy() {
	switch m.activeTab {
	case TabCategories, TabTags:
		options := []string{"count", "name", "id", "slug"}
		m.query.OrderBy = nextOption(m.query.OrderBy, options)
	case TabUsers:
		options := []string{"name", "id", "slug"}
		m.query.OrderBy = nextOption(m.query.OrderBy, options)
	default:
		options := []string{"date", "modified", "title", "id", "slug"}
		m.query.OrderBy = nextOption(m.query.OrderBy, options)
	}
}

func nextOption(curr string, opts []string) string {
	for i, o := range opts {
		if o == curr {
			return opts[(i+1)%len(opts)]
		}
	}
	return opts[0]
}

func (m *Model) applyFilterDSL(input string) {
	if strings.TrimSpace(input) == "" || strings.TrimSpace(input) == "clear" || strings.TrimSpace(input) == "reset" {
		m.query = wp.DefaultQueryFilter(m.query.Endpoint)
		return
	}
	tokens := strings.Fields(input)
	for _, tok := range tokens {
		parts := strings.SplitN(tok, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k, v := strings.ToLower(parts[0]), parts[1]
		switch k {
		case "after":
			m.query.After = v
		case "before":
			m.query.Before = v
		case "slug":
			m.query.Slug = v
		case "mime":
			m.query.MimeType = v
		case "ext", "exts", "extension", "preset":
			m.query.Extensions = strings.Split(v, ",")
		case "exclude-type", "not-type", "!type":
			m.query.ExcludeMediaTypes = strings.Split(v, ",")
		case "exclude-ext", "not-ext", "!ext":
			m.query.ExcludeExtensions = strings.Split(v, ",")
		case "dir", "dirs", "source", "scope":
			m.query.DirectoryScopes = strings.Split(v, ",")
		case "type":
			if strings.HasPrefix(v, "!") || strings.HasPrefix(v, "-") {
				m.query.ExcludeMediaTypes = strings.Split(strings.TrimLeft(v, "!-"), ",")
			} else {
				m.query.MediaType = v
			}
		case "orderby":
			m.query.OrderBy = v
		case "order":
			m.query.Order = v
		case "author":
			var id int
			if _, err := fmt.Sscanf(v, "%d", &id); err == nil && id > 0 {
				m.query.Author = []int{id}
			}
		case "cat", "category":
			var id int
			if _, err := fmt.Sscanf(v, "%d", &id); err == nil && id > 0 {
				m.query.Categories = []int{id}
			}
		case "tag":
			var id int
			if _, err := fmt.Sscanf(v, "%d", &id); err == nil && id > 0 {
				m.query.Tags = []int{id}
			}
		case "per_page":
			var pp int
			if _, err := fmt.Sscanf(v, "%d", &pp); err == nil && pp >= 1 && pp <= 100 {
				m.query.PerPage = pp
			}
		}
	}
}

func (m *Model) recalcLayout() {
	bodyHeight := max(10, m.height-9)
	rightWidth := max(36, (m.width*52)/100-4)
	m.inspector.Width = rightWidth
	m.inspector.Height = bodyHeight - 2
}
