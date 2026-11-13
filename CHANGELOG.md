# Changelog

All notable changes to **Quarry** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-13

### Added

- **WordPress REST API v2 Discovery & Client (`internal/wp`)**:
  - Automatic discovery probing `/wp-json/`, fallback `/?rest_route=/`, HTTP `Link` headers, and HTML `<link rel="https://api.w.org/">` tags.
  - Full support for Posts, Pages, Media Library, Categories, Tags, Users, Comments, and dynamically discovered Custom Post Types (`/wp/v2/types`) and Taxonomies (`/wp/v2/taxonomies`).
  - Built-in rate limiting, exponential backoff, `Retry-After` handling, and optional Bearer token / WordPress Application Password authentication.
  - Gutenberg and Classic HTML-to-Markdown converter and inline media URL extractor.
- **Interactive Split-Pane Terminal UI (`internal/tui`)**:
  - Built with Charmbracelet Bubble Tea, Bubbles, and Lip Gloss.
  - Multi-site workspace switcher (`S`), 8 resource tabs (`1`–`8`), full-text search (`/`), advanced filter DSL (`f`), sort/order cycling (`s`/`o`), and relational drill-down (`Enter` on Categories, Tags, or Users).
  - Multi-format live Inspector (`v` to switch between Markdown, Raw HTML, and REST JSON).
  - Interactive item selection (`Space`, `a`) with one-key export to Markdown (`x`), JSON (`J`), CSV (`C`), and background media download (`d`).
- **Non-Interactive CLI (`cmd/quarry`, `internal/cli`)**:
  - `quarry inspect`, `quarry query`, `quarry export`, `quarry media`, and `quarry tui` subcommands.
- **Concurrent Media Downloader Pool (`internal/downloader`)**:
  - Bounded worker pool with atomic `.part` file writes, SHA-256 checksum computation, upload directory hierarchy preservation, and automatic resume.
