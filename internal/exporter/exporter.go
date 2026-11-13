package exporter

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AryanHamedani/quarry/internal/wp"
	"gopkg.in/yaml.v3"
)

// Format specifies the output serialization format.
type Format string

const (
	FormatJSON     Format = "json"
	FormatJSONL    Format = "jsonl"
	FormatMarkdown Format = "markdown"
	FormatCSV      Format = "csv"
	FormatHTML     Format = "html"
)

var reUnsafePathChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// SafeSlug converts any title or slug into a clean filesystem-safe filename stem.
func SafeSlug(s string, fallbackID int) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = reUnsafePathChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		return fmt.Sprintf("item-%d", fallbackID)
	}
	return s
}

// ExportPosts writes a slice of WordPress posts to disk in the requested format.
// For FormatMarkdown and FormatHTML, outPath is treated as a directory where each post gets its own file.
// For FormatJSON, FormatJSONL, and FormatCSV, outPath is treated as a file path (or directory if it ends with /).
func ExportPosts(posts []wp.Post, format Format, outPath string, siteURL string) (string, error) {
	switch format {
	case FormatJSON:
		target := resolveFileTarget(outPath, "posts.json")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		data, err := json.MarshalIndent(posts, "", "  ")
		if err != nil {
			return "", err
		}
		return target, os.WriteFile(target, data, 0o644)

	case FormatJSONL:
		target := resolveFileTarget(outPath, "posts.jsonl")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		f, err := os.Create(target)
		if err != nil {
			return "", err
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		for _, p := range posts {
			if err := enc.Encode(p); err != nil {
				return target, err
			}
		}
		return target, nil

	case FormatCSV:
		target := resolveFileTarget(outPath, "posts.csv")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		f, err := os.Create(target)
		if err != nil {
			return "", err
		}
		defer f.Close()
		w := csv.NewWriter(f)
		defer w.Flush()

		_ = w.Write([]string{
			"id", "type", "status", "date", "modified", "slug",
			"title", "author", "categories", "tags", "link", "featured_media_url", "excerpt",
		})
		for _, p := range posts {
			_ = w.Write([]string{
				strconv.Itoa(p.ID),
				p.Type,
				p.Status,
				p.Date.FormatShort(),
				p.Modified.FormatShort(),
				p.Slug,
				wp.CleanPlainText(p.Title.Rendered),
				p.ResolveAuthorName(),
				strings.Join(p.ResolveTermNames("category"), "; "),
				strings.Join(p.ResolveTermNames("post_tag"), "; "),
				p.Link,
				p.ResolveFeaturedURL(),
				wp.CleanPlainText(p.Excerpt.Rendered),
			})
		}
		return target, w.Error()

	case FormatMarkdown:
		dir := outPath
		if strings.HasSuffix(dir, ".md") && len(posts) == 1 {
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				return "", err
			}
			return dir, os.WriteFile(dir, []byte(RenderPostMarkdown(posts[0], siteURL)), 0o644)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		for _, p := range posts {
			datePrefix := ""
			if !p.Date.IsZero() {
				datePrefix = p.Date.Format("2006-01-02") + "-"
			}
			fname := fmt.Sprintf("%s%s.md", datePrefix, SafeSlug(p.Slug, p.ID))
			fullPath := filepath.Join(dir, fname)
			if err := os.WriteFile(fullPath, []byte(RenderPostMarkdown(p, siteURL)), 0o644); err != nil {
				return dir, err
			}
		}
		return dir, nil

	case FormatHTML:
		dir := outPath
		if strings.HasSuffix(dir, ".html") && len(posts) == 1 {
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				return "", err
			}
			return dir, os.WriteFile(dir, []byte(RenderPostHTML(posts[0], siteURL)), 0o644)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		for _, p := range posts {
			datePrefix := ""
			if !p.Date.IsZero() {
				datePrefix = p.Date.Format("2006-01-02") + "-"
			}
			fname := fmt.Sprintf("%s%s.html", datePrefix, SafeSlug(p.Slug, p.ID))
			fullPath := filepath.Join(dir, fname)
			if err := os.WriteFile(fullPath, []byte(RenderPostHTML(p, siteURL)), 0o644); err != nil {
				return dir, err
			}
		}
		return dir, nil

	default:
		return "", fmt.Errorf("unsupported export format %q", format)
	}
}

// Frontmatter represents YAML frontmatter for exported Markdown files.
type Frontmatter struct {
	ID            int      `yaml:"id"`
	Title         string   `yaml:"title"`
	Slug          string   `yaml:"slug"`
	Type          string   `yaml:"type"`
	Status        string   `yaml:"status"`
	Date          string   `yaml:"date,omitempty"`
	Modified      string   `yaml:"modified,omitempty"`
	Author        string   `yaml:"author,omitempty"`
	Categories    []string `yaml:"categories,omitempty"`
	Tags          []string `yaml:"tags,omitempty"`
	FeaturedImage string   `yaml:"featured_image,omitempty"`
	CanonicalURL  string   `yaml:"canonical_url"`
	SiteURL       string   `yaml:"site_url,omitempty"`
	ExtractedAt   string   `yaml:"extracted_at"`
}

// RenderPostMarkdown converts a WordPress Post into a complete Markdown document with YAML frontmatter.
func RenderPostMarkdown(p wp.Post, siteURL string) string {
	fm := Frontmatter{
		ID:            p.ID,
		Title:         wp.CleanPlainText(p.Title.Rendered),
		Slug:          p.Slug,
		Type:          p.Type,
		Status:        p.Status,
		Date:          p.Date.FormatFull(),
		Modified:      p.Modified.FormatFull(),
		Author:        p.ResolveAuthorName(),
		Categories:    p.ResolveTermNames("category"),
		Tags:          p.ResolveTermNames("post_tag"),
		FeaturedImage: p.ResolveFeaturedURL(),
		CanonicalURL:  p.Link,
		SiteURL:       siteURL,
		ExtractedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	yamlBytes, _ := yaml.Marshal(fm)

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---\n\n")
	sb.WriteString("# " + fm.Title + "\n\n")

	if fm.FeaturedImage != "" {
		sb.WriteString(fmt.Sprintf("![Featured Image](%s)\n\n", fm.FeaturedImage))
	}

	bodyMD := wp.HTMLToMarkdown(p.Content.Rendered)
	if bodyMD == "" && p.Excerpt.Rendered != "" {
		bodyMD = wp.HTMLToMarkdown(p.Excerpt.Rendered)
	}
	sb.WriteString(bodyMD)
	sb.WriteString("\n")
	return sb.String()
}

// RenderPostHTML produces a clean, self-contained HTML5 document for offline archiving.
func RenderPostHTML(p wp.Post, siteURL string) string {
	title := wp.CleanPlainText(p.Title.Rendered)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <meta name="generator" content="Quarry WordPress Extractor">
  <link rel="canonical" href="%s">
  <style>
    :root { color-scheme: light dark; }
    body { max-width: 46rem; margin: 2.5rem auto; padding: 0 1.25rem; font-family: system-ui, -apple-system, sans-serif; line-height: 1.65; }
    header { border-bottom: 1px solid rgba(128,128,128,0.3); padding-bottom: 1rem; margin-bottom: 2rem; }
    .meta { font-size: 0.9rem; opacity: 0.75; }
    img, video { max-width: 100%%; height: auto; border-radius: 6px; }
    pre { padding: 1rem; overflow-x: auto; background: rgba(128,128,128,0.12); border-radius: 6px; }
  </style>
</head>
<body>
  <header>
    <h1>%s</h1>
    <div class="meta">
      Published %s · Author: %s · <a href="%s">Original URL</a>
    </div>
  </header>
  <article>
    %s
  </article>
</body>
</html>
`, title, p.Link, title, p.Date.FormatShort(), p.ResolveAuthorName(), p.Link, p.Content.Rendered)
}

// ExportMediaCatalog exports media items metadata to JSON, JSONL, or CSV.
func ExportMediaCatalog(items []wp.MediaItem, format Format, outPath string) (string, error) {
	switch format {
	case FormatJSON, FormatMarkdown, FormatHTML:
		target := resolveFileTarget(outPath, "media.json")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		data, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return "", err
		}
		return target, os.WriteFile(target, data, 0o644)

	case FormatJSONL:
		target := resolveFileTarget(outPath, "media.jsonl")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		f, err := os.Create(target)
		if err != nil {
			return "", err
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		for _, m := range items {
			if err := enc.Encode(m); err != nil {
				return target, err
			}
		}
		return target, nil

	case FormatCSV:
		target := resolveFileTarget(outPath, "media.csv")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		f, err := os.Create(target)
		if err != nil {
			return "", err
		}
		defer f.Close()
		w := csv.NewWriter(f)
		defer w.Flush()

		_ = w.Write([]string{"id", "date", "slug", "title", "media_type", "mime_type", "dimensions", "filesize", "alt_text", "source_url"})
		for _, m := range items {
			_ = w.Write([]string{
				strconv.Itoa(m.ID),
				m.Date.FormatShort(),
				m.Slug,
				wp.CleanPlainText(m.Title.Rendered),
				m.MediaType,
				m.MimeType,
				m.DimensionsLabel(),
				strconv.FormatInt(m.MediaDetails.FileSize, 10),
				m.AltText,
				m.SourceURL,
			})
		}
		return target, w.Error()

	default:
		return "", fmt.Errorf("unsupported media export format %q", format)
	}
}

func resolveFileTarget(outPath, defaultFilename string) string {
	info, err := os.Stat(outPath)
	if (err == nil && info.IsDir()) || strings.HasSuffix(outPath, "/") || strings.HasSuffix(outPath, string(os.PathSeparator)) {
		return filepath.Join(outPath, defaultFilename)
	}
	return outPath
}
