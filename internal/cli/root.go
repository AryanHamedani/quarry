package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AryanHamedani/quarry/internal/downloader"
	"github.com/AryanHamedani/quarry/internal/exporter"
	"github.com/AryanHamedani/quarry/internal/tui"
	"github.com/AryanHamedani/quarry/internal/wp"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Version is injected at build time via -ldflags.
var Version = "0.1.0"

type GlobalFlags struct {
	SitesFile   string
	OutputDir   string
	Format      string
	Concurrency int
	RateLimit   float64
	Timeout     time.Duration
	BearerToken string
	User        string
	AppPassword string
	Insecure    bool
	Quiet       bool
}

// NewRootCommand constructs the complete Cobra CLI tree for Quarry.
func NewRootCommand() *cobra.Command {
	var gf GlobalFlags

	rootCmd := &cobra.Command{
		Use:   "quarry [site-url...]",
		Short: "Excavate, explore, search, and mass-extract data & media from WordPress sites via REST API",
		Long: `Quarry is a fast, craft-engineered WordPress REST API explorer, scraper, and media archiver.

Pass one or more WordPress site URLs directly (or via --sites-file) to launch the interactive
Terminal UI (TUI), or use subcommands (inspect, query, export, media) for non-interactive
automation, scripting, and full-site archiving.`,
		Version:      Version,
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := collectSiteURLs(args, gf.SitesFile)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return cmd.Help()
			}
			return runInteractiveTUI(cmd.Context(), sites, gf)
		},
	}

	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&gf.SitesFile, "sites-file", "L", "", "File containing WordPress site URLs (one per line)")
	pf.StringVarP(&gf.OutputDir, "output", "o", "./quarry-output", "Output directory or file path for extracted data and media")
	pf.StringVarP(&gf.Format, "format", "f", "markdown", "Output format: markdown, json, jsonl, csv, html")
	pf.IntVarP(&gf.Concurrency, "concurrency", "c", 6, "Concurrent worker pool size for media downloads")
	pf.Float64Var(&gf.RateLimit, "rate-limit", 8.0, "Max REST API requests per second per site")
	pf.DurationVar(&gf.Timeout, "timeout", 25*time.Second, "HTTP request timeout")
	pf.StringVar(&gf.BearerToken, "token", "", "Optional JWT / Bearer token for authenticated REST API endpoints")
	pf.StringVar(&gf.User, "user", "", "Optional WordPress username for Application Password auth")
	pf.StringVar(&gf.AppPassword, "app-password", "", "Optional WordPress Application Password")
	pf.BoolVarP(&gf.Insecure, "insecure", "k", false, "Skip TLS certificate verification")
	pf.BoolVarP(&gf.Quiet, "quiet", "q", false, "Suppress progress logs on stderr (for clean stdout piping)")

	rootCmd.AddCommand(
		newTuiCmd(&gf),
		newInspectCmd(&gf),
		newQueryCmd(&gf),
		newExportCmd(&gf),
		newMediaCmd(&gf),
	)

	return rootCmd
}

func buildClientOptions(gf GlobalFlags) wp.ClientOptions {
	opts := wp.DefaultClientOptions()
	opts.Timeout = gf.Timeout
	opts.RequestsPerSec = gf.RateLimit
	opts.BearerToken = gf.BearerToken
	opts.BasicUser = gf.User
	opts.BasicAppPass = gf.AppPassword
	opts.InsecureSkipTLS = gf.Insecure
	return opts
}

func collectSiteURLs(args []string, sitesFile string) ([]string, error) {
	var raw []string
	for _, a := range args {
		for _, part := range strings.Split(a, ",") {
			if s := strings.TrimSpace(part); s != "" {
				raw = append(raw, s)
			}
		}
	}
	if sitesFile != "" {
		f, err := os.Open(sitesFile)
		if err != nil {
			return nil, fmt.Errorf("opening sites file %s: %w", sitesFile, err)
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			raw = append(raw, line)
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	seen := make(map[string]struct{}, len(raw))
	var out []string
	for _, r := range raw {
		norm, err := wp.NormalizeSiteURL(r)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[norm]; !ok {
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out, nil
}

func runInteractiveTUI(ctx context.Context, sites []string, gf GlobalFlags) error {
	model, err := tui.NewModel(ctx, sites, buildClientOptions(gf), gf.OutputDir, gf.Concurrency)
	if err != nil {
		return err
	}
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}

func newTuiCmd(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "tui <site-url...>",
		Short: "Launch the interactive Terminal UI (TUI) explorer for one or more WordPress sites",
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := collectSiteURLs(args, gf.SitesFile)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return fmt.Errorf("provide at least one WordPress site URL or --sites-file")
			}
			return runInteractiveTUI(cmd.Context(), sites, *gf)
		},
	}
}

func newInspectCmd(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <site-url...>",
		Short: "Discover WordPress REST API capabilities, namespaces, custom post types, taxonomies, and counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := collectSiteURLs(args, gf.SitesFile)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return fmt.Errorf("provide at least one WordPress site URL or --sites-file")
			}

			profiles := make([]*wp.SiteProfile, 0, len(sites))
			for _, u := range sites {
				cli, err := wp.NewClient(u, buildClientOptions(*gf))
				if err != nil {
					return err
				}
				if !gf.Quiet {
					fmt.Fprintf(os.Stderr, "⛏  Probing %s ...\n", cli.BaseURL())
				}
				prof, err := cli.Discover(cmd.Context())
				if err != nil {
					return err
				}
				profiles = append(profiles, prof)
			}

			switch strings.ToLower(gf.Format) {
			case "json":
				var data []byte
				if len(profiles) == 1 {
					data, _ = json.MarshalIndent(profiles[0], "", "  ")
				} else {
					data, _ = json.MarshalIndent(profiles, "", "  ")
				}
				fmt.Println(string(data))
			case "yaml":
				data, _ := yaml.Marshal(profiles)
				fmt.Print(string(data))
			default:
				for _, p := range profiles {
					printProfileSummary(p)
				}
			}
			return nil
		},
	}
}

func printProfileSummary(p *wp.SiteProfile) {
	fmt.Printf("━━━ %s (%s) ━━━\n", p.Name, p.URL)
	if p.Description != "" {
		fmt.Printf("  Description : %s\n", p.Description)
	}
	fmt.Printf("  API Root    : %s (pretty routes: %v, latency: %dms)\n",
		p.APIRootURL, p.SupportsPretty, p.ResponseLatency.Milliseconds())
	if p.TimezoneString != "" {
		fmt.Printf("  Timezone    : %s (GMT %+.1f)\n", p.TimezoneString, p.GMTOffset)
	}
	fmt.Printf("  Namespaces  : %s\n", strings.Join(p.Namespaces, ", "))
	if len(p.EntityCounts) > 0 {
		var parts []string
		for _, k := range []string{"posts", "pages", "media", "categories", "tags", "users", "comments"} {
			if c, ok := p.EntityCounts[k]; ok {
				parts = append(parts, fmt.Sprintf("%s=%d", k, c))
			}
		}
		fmt.Printf("  Counts      : %s\n", strings.Join(parts, " · "))
	}
	if len(p.PostTypes) > 0 {
		var cpts []string
		for slug, pt := range p.PostTypes {
			cpts = append(cpts, fmt.Sprintf("%s (/wp/v2/%s)", slug, pt.RESTBase))
		}
		fmt.Printf("  Post Types  : %s\n", strings.Join(cpts, ", "))
	}
	fmt.Println()
}

func newQueryCmd(gf *GlobalFlags) *cobra.Command {
	var (
		resource   string
		search     string
		orderBy    string
		order      string
		after      string
		before     string
		authorID   int
		categoryID int
		tagID      int
		slug       string
		mimeType   string
		extFilter  string
		perPage    int
		maxPages   int
		maxItems   int
		allPages   bool
	)

	cmd := &cobra.Command{
		Use:   "query <site-url...>",
		Short: "Search, filter, and sort any WordPress REST API endpoint non-interactively",
		Example: `  quarry query https://wordpress.org/news --resource posts --search "release" --format json
  quarry query https://example.com --resource media --ext zip,csv,excel --all -f csv -o files.csv
  quarry query https://example.com --resource posts --after 2025-01-01 --orderby modified --order desc`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := collectSiteURLs(args, gf.SitesFile)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return fmt.Errorf("provide at least one WordPress site URL or --sites-file")
			}

			qf := wp.DefaultQueryFilter(resource)
			qf.Search = search
			if orderBy != "" {
				qf.OrderBy = orderBy
			}
			if order != "" {
				qf.Order = order
			}
			qf.After = after
			qf.Before = before
			qf.Slug = slug
			qf.MimeType = mimeType
			if extFilter != "" {
				qf.Extensions = strings.Split(extFilter, ",")
			}
			if authorID > 0 {
				qf.Author = []int{authorID}
			}
			if categoryID > 0 {
				qf.Categories = []int{categoryID}
			}
			if tagID > 0 {
				qf.Tags = []int{tagID}
			}
			if perPage > 0 {
				qf.PerPage = perPage
			}
			qf.MaxPages = maxPages
			qf.MaxItems = maxItems

			for _, u := range sites {
				cli, err := wp.NewClient(u, buildClientOptions(*gf))
				if err != nil {
					return err
				}
				progress := func(page, totalPages, fetched, total int) {
					if !gf.Quiet {
						fmt.Fprintf(os.Stderr, "\r⛏  [%s] Page %d/%d (%d/%d items)...",
							cli.BaseURL(), page, totalPages, fetched, total)
					}
				}

				switch resource {
				case "media":
					var items []wp.MediaItem
					if allPages {
						items, err = cli.FetchAllMedia(cmd.Context(), qf, progress)
					} else {
						items, _, err = cli.FetchMedia(cmd.Context(), qf)
					}
					if !gf.Quiet && allPages {
						fmt.Fprintln(os.Stderr)
					}
					if err != nil {
						return err
					}
					if err := outputMediaResult(items, exporter.Format(strings.ToLower(gf.Format)), gf.OutputDir, cmd.Flags().Changed("output")); err != nil {
						return err
					}

				case "categories", "tags":
					var terms []wp.TermItem
					if allPages {
						terms, err = cli.FetchAllTerms(cmd.Context(), qf, progress)
					} else {
						terms, _, err = cli.FetchTerms(cmd.Context(), qf)
					}
					if !gf.Quiet && allPages {
						fmt.Fprintln(os.Stderr)
					}
					if err != nil {
						return err
					}
					data, _ := json.MarshalIndent(terms, "", "  ")
					fmt.Println(string(data))

				case "users":
					var users []wp.User
					if allPages {
						users, err = cli.FetchAllUsers(cmd.Context(), qf, progress)
					} else {
						users, _, err = cli.FetchUsers(cmd.Context(), qf)
					}
					if !gf.Quiet && allPages {
						fmt.Fprintln(os.Stderr)
					}
					if err != nil {
						return err
					}
					data, _ := json.MarshalIndent(users, "", "  ")
					fmt.Println(string(data))

				case "comments":
					var comments []wp.Comment
					if allPages {
						comments, err = cli.FetchAllComments(cmd.Context(), qf, progress)
					} else {
						comments, _, err = cli.FetchComments(cmd.Context(), qf)
					}
					if !gf.Quiet && allPages {
						fmt.Fprintln(os.Stderr)
					}
					if err != nil {
						return err
					}
					data, _ := json.MarshalIndent(comments, "", "  ")
					fmt.Println(string(data))

				default:
					var posts []wp.Post
					if allPages {
						posts, err = cli.FetchAllPosts(cmd.Context(), qf, progress)
					} else {
						posts, _, err = cli.FetchPosts(cmd.Context(), qf)
					}
					if !gf.Quiet && allPages {
						fmt.Fprintln(os.Stderr)
					}
					if err != nil {
						return err
					}
					if err := outputPostsResult(posts, exporter.Format(strings.ToLower(gf.Format)), gf.OutputDir, cmd.Flags().Changed("output"), cli.BaseURL()); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&resource, "resource", "r", "posts", "REST endpoint resource (posts, pages, media, categories, tags, users, comments, or custom type slug)")
	f.StringVarP(&search, "search", "s", "", "Full-text search query")
	f.StringVar(&orderBy, "orderby", "", "Sort field (date, modified, title, id, slug, count, name)")
	f.StringVar(&order, "order", "", "Sort direction (desc, asc)")
	f.StringVar(&after, "after", "", "Filter items published after ISO-8601 date (e.g. 2025-01-01)")
	f.StringVar(&before, "before", "", "Filter items published before ISO-8601 date (e.g. 2026-12-31)")
	f.IntVar(&authorID, "author", 0, "Filter by author ID")
	f.IntVar(&categoryID, "category", 0, "Filter by category ID")
	f.IntVar(&tagID, "tag", 0, "Filter by tag ID")
	f.StringVar(&slug, "slug", "", "Filter by exact item slug")
	f.StringVar(&mimeType, "mime", "", "Filter media by MIME type or comma-separated list (e.g. application/zip,text/csv)")
	f.StringVarP(&extFilter, "ext", "e", "", "Filter media by extension/preset: zip,csv,excel,xls,xlsx,archive,spreadsheet,pdf,doc,json")
	f.IntVar(&perPage, "per-page", 25, "Items per page (1..100)")
	f.BoolVar(&allPages, "all", false, "Automatically paginate through all available pages")
	f.IntVar(&maxPages, "max-pages", 0, "Maximum number of pages to fetch when --all is set (0 = unlimited)")
	f.IntVar(&maxItems, "max-items", 0, "Maximum number of items to fetch (0 = unlimited)")

	return cmd
}

func newExportCmd(gf *GlobalFlags) *cobra.Command {
	var (
		includeMedia bool
		maxPages     int
		maxItems     int
		typesFlag    string
	)

	cmd := &cobra.Command{
		Use:   "export <site-url...>",
		Short: "Mass-extract posts, pages, taxonomies, and optionally all media from WordPress sites",
		Example: `  quarry export https://wordpress.org/news --format markdown --output ./archive
  quarry export --sites-file sites.txt --include-media --format jsonl -o ./dataset`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := collectSiteURLs(args, gf.SitesFile)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return fmt.Errorf("provide at least one WordPress site URL or --sites-file")
			}

			format := exporter.Format(strings.ToLower(gf.Format))
			endpoints := strings.Split(typesFlag, ",")

			for _, u := range sites {
				cli, err := wp.NewClient(u, buildClientOptions(*gf))
				if err != nil {
					return err
				}
				hostSlug := exporter.SafeSlug(strings.TrimPrefix(strings.TrimPrefix(cli.BaseURL(), "https://"), "http://"), 1)
				siteOutDir := filepath.Join(gf.OutputDir, hostSlug)
				if err := os.MkdirAll(siteOutDir, 0o755); err != nil {
					return err
				}

				if !gf.Quiet {
					fmt.Fprintf(os.Stderr, "⛏  Discovering %s ...\n", cli.BaseURL())
				}
				if prof, err := cli.Discover(cmd.Context()); err == nil {
					profBytes, _ := json.MarshalIndent(prof, "", "  ")
					_ = os.WriteFile(filepath.Join(siteOutDir, "site-profile.json"), profBytes, 0o644)
				}

				var inlineMediaTasks []downloader.AssetTask

				for _, ep := range endpoints {
					ep = strings.TrimSpace(ep)
					if ep == "" {
						continue
					}
					qf := wp.DefaultQueryFilter(ep)
					qf.PerPage = 100
					qf.MaxPages = maxPages
					qf.MaxItems = maxItems

					posts, err := cli.FetchAllPosts(cmd.Context(), qf, func(page, totalPages, fetched, total int) {
						if !gf.Quiet {
							fmt.Fprintf(os.Stderr, "\r   • Fetching %s: page %d/%d (%d/%d)...", ep, page, totalPages, fetched, total)
						}
					})
					if !gf.Quiet {
						fmt.Fprintln(os.Stderr)
					}
					if err != nil {
						fmt.Fprintf(os.Stderr, "   ! Warning: could not fetch %s: %v\n", ep, err)
						continue
					}
					if len(posts) == 0 {
						continue
					}

					targetPath := filepath.Join(siteOutDir, ep)
					if format == exporter.FormatJSON || format == exporter.FormatJSONL || format == exporter.FormatCSV {
						targetPath = filepath.Join(siteOutDir, fmt.Sprintf("%s.%s", ep, format))
					}
					savedPath, err := exporter.ExportPosts(posts, format, targetPath, cli.BaseURL())
					if err != nil {
						return err
					}
					if !gf.Quiet {
						fmt.Fprintf(os.Stderr, "   ✓ Saved %d %s → %s\n", len(posts), ep, savedPath)
					}

					if includeMedia {
						for _, p := range posts {
							if feat := p.ResolveFeaturedURL(); feat != "" {
								inlineMediaTasks = append(inlineMediaTasks, downloader.AssetTask{
									ID:        p.FeaturedMedia,
									SourceURL: feat,
									Date:      p.Date.Time,
								})
							}
							for _, mu := range wp.ExtractMediaURLsFromHTML(p.Content.Rendered) {
								inlineMediaTasks = append(inlineMediaTasks, downloader.AssetTask{
									SourceURL: mu,
									Date:      p.Date.Time,
								})
							}
						}
					}
				}

				if includeMedia {
					mqf := wp.DefaultQueryFilter("media")
					mqf.PerPage = 100
					mqf.MaxPages = maxPages
					mqf.MaxItems = maxItems
					mediaItems, err := cli.FetchAllMedia(cmd.Context(), mqf, func(page, totalPages, fetched, total int) {
						if !gf.Quiet {
							fmt.Fprintf(os.Stderr, "\r   • Cataloging media library: page %d/%d (%d/%d)...", page, totalPages, fetched, total)
						}
					})
					if !gf.Quiet {
						fmt.Fprintln(os.Stderr)
					}
					if err == nil && len(mediaItems) > 0 {
						_, _ = exporter.ExportMediaCatalog(mediaItems, exporter.FormatJSON, filepath.Join(siteOutDir, "media-catalog.json"))
						for _, m := range mediaItems {
							if m.SourceURL != "" {
								inlineMediaTasks = append(inlineMediaTasks, downloader.AssetTask{
									ID:        m.ID,
									SourceURL: m.SourceURL,
									Title:     wp.CleanPlainText(m.Title.Rendered),
									MimeType:  m.MimeType,
									Date:      m.Date.Time,
								})
							}
						}
					}

					if len(inlineMediaTasks) > 0 {
						mediaOut := filepath.Join(siteOutDir, "media")
						poolOpts := downloader.DefaultPoolOptions(mediaOut)
						poolOpts.Concurrency = gf.Concurrency
						summary, err := downloader.DownloadBatch(cmd.Context(), inlineMediaTasks, poolOpts, func(u downloader.ProgressUpdate) {
							if !gf.Quiet {
								mb := float64(u.TotalBytes) / (1024 * 1024)
								fmt.Fprintf(os.Stderr, "\r   • Downloading media [%d/%d] ✓%d ↷%d ✗%d (%.2f MB)...",
									u.Completed, u.Total, u.Succeeded, u.Skipped, u.Failed, mb)
							}
						})
						if !gf.Quiet {
							fmt.Fprintln(os.Stderr)
							fmt.Fprintf(os.Stderr, "   ✓ Media archive complete: %d downloaded, %d skipped, %d failed → %s\n",
								summary.Succeeded, summary.Skipped, summary.Failed, mediaOut)
						}
						if err != nil {
							return err
						}
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&includeMedia, "include-media", "m", false, "Also download all media library attachments and inline post images")
	cmd.Flags().StringVar(&typesFlag, "types", "posts,pages", "Comma-separated post types to export (e.g. posts,pages,product)")
	cmd.Flags().IntVar(&maxPages, "max-pages", 0, "Limit maximum pages per endpoint (0 = all)")
	cmd.Flags().IntVar(&maxItems, "max-items", 0, "Limit maximum items per endpoint (0 = all)")

	return cmd
}

func newMediaCmd(gf *GlobalFlags) *cobra.Command {
	var (
		mimeType   string
		extFilter  string
		mediaType  string
		search     string
		flatLayout bool
		noResume   bool
		maxPages   int
		maxItems   int
	)

	cmd := &cobra.Command{
		Use:   "media <site-url...>",
		Short: "Bulk-download media attachments (images, archives, spreadsheets, videos, documents) via concurrent worker pool",
		Example: `  quarry media https://example.com --ext zip,csv,excel --output ./downloads
  quarry media https://example.com --concurrency 8 --output ./downloads
  quarry media https://example.com --mime image/webp --max-items 100`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := collectSiteURLs(args, gf.SitesFile)
			if err != nil {
				return err
			}
			if len(sites) == 0 {
				return fmt.Errorf("provide at least one WordPress site URL or --sites-file")
			}

			for _, u := range sites {
				cli, err := wp.NewClient(u, buildClientOptions(*gf))
				if err != nil {
					return err
				}
				hostSlug := exporter.SafeSlug(strings.TrimPrefix(strings.TrimPrefix(cli.BaseURL(), "https://"), "http://"), 1)
				mediaDir := filepath.Join(gf.OutputDir, hostSlug, "media")

				qf := wp.DefaultQueryFilter("media")
				qf.PerPage = 100
				qf.MimeType = mimeType
				if extFilter != "" {
					qf.Extensions = strings.Split(extFilter, ",")
				}
				qf.MediaType = mediaType
				qf.Search = search
				qf.MaxPages = maxPages
				qf.MaxItems = maxItems

				items, err := cli.FetchAllMedia(cmd.Context(), qf, func(page, totalPages, fetched, total int) {
					if !gf.Quiet {
						fmt.Fprintf(os.Stderr, "\r⛏  [%s] Discovering media page %d/%d (%d/%d)...",
							cli.BaseURL(), page, totalPages, fetched, total)
					}
				})
				if !gf.Quiet {
					fmt.Fprintln(os.Stderr)
				}
				if err != nil {
					return err
				}

				tasks := make([]downloader.AssetTask, 0, len(items))
				for _, item := range items {
					if item.SourceURL != "" {
						tasks = append(tasks, downloader.AssetTask{
							ID:        item.ID,
							SourceURL: item.SourceURL,
							Title:     wp.CleanPlainText(item.Title.Rendered),
							MimeType:  item.MimeType,
							Date:      item.Date.Time,
						})
					}
				}

				opts := downloader.DefaultPoolOptions(mediaDir)
				opts.Concurrency = gf.Concurrency
				opts.FlatLayout = flatLayout
				opts.Resume = !noResume

				summary, err := downloader.DownloadBatch(cmd.Context(), tasks, opts, func(u downloader.ProgressUpdate) {
					if !gf.Quiet {
						mb := float64(u.TotalBytes) / (1024 * 1024)
						fmt.Fprintf(os.Stderr, "\r   • Downloading [%d/%d] ✓%d ↷%d ✗%d (%.2f MB)...",
							u.Completed, u.Total, u.Succeeded, u.Skipped, u.Failed, mb)
					}
				})
				if !gf.Quiet {
					fmt.Fprintln(os.Stderr)
					fmt.Fprintf(os.Stderr, "✓ Completed %s: %d downloaded, %d skipped, %d failed (%.2f MB in %s) → %s\n",
						cli.BaseURL(), summary.Succeeded, summary.Skipped, summary.Failed,
						float64(summary.TotalBytes)/(1024*1024), summary.Duration.Round(time.Millisecond), mediaDir)
				}
				if err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&mimeType, "mime", "", "Filter by MIME type or comma-separated list (e.g. application/zip,text/csv)")
	cmd.Flags().StringVarP(&extFilter, "ext", "e", "", "Filter by extension/preset: zip,csv,excel,xls,xlsx,archive,spreadsheet,pdf,doc,json")
	cmd.Flags().StringVar(&mediaType, "type", "", "Filter by general media type (image, video, audio, application, text)")
	cmd.Flags().StringVarP(&search, "search", "s", "", "Search media titles/captions")
	cmd.Flags().BoolVar(&flatLayout, "flat", false, "Save all files in a single flat folder instead of preserving year/month paths")
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Re-download and overwrite existing files")
	cmd.Flags().IntVar(&maxPages, "max-pages", 0, "Maximum pages of media metadata to fetch (0 = all)")
	cmd.Flags().IntVar(&maxItems, "max-items", 0, "Maximum number of media items to download (0 = all)")

	return cmd
}

func outputPostsResult(posts []wp.Post, format exporter.Format, outPath string, writeToFile bool, siteURL string) error {
	if writeToFile || format == exporter.FormatMarkdown || format == exporter.FormatHTML {
		if !writeToFile && format == exporter.FormatMarkdown && len(posts) == 1 {
			fmt.Print(exporter.RenderPostMarkdown(posts[0], siteURL))
			return nil
		}
		target, err := exporter.ExportPosts(posts, format, outPath, siteURL)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "✓ Wrote %d items → %s\n", len(posts), target)
		return nil
	}

	switch format {
	case exporter.FormatJSONL:
		enc := json.NewEncoder(os.Stdout)
		for _, p := range posts {
			if err := enc.Encode(p); err != nil {
				return err
			}
		}
	default:
		data, err := json.MarshalIndent(posts, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	}
	return nil
}

func outputMediaResult(items []wp.MediaItem, format exporter.Format, outPath string, writeToFile bool) error {
	if writeToFile {
		target, err := exporter.ExportMediaCatalog(items, format, outPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "✓ Wrote %d media records → %s\n", len(items), target)
		return nil
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
