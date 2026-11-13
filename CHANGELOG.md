# Changelog

All notable changes to **Quarry** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1] - 2026-11-14

### Added

- **Raw `Cookie` Header Authentication + Automatic WordPress REST Nonce Resolution (`--cookie` / `-b`, TUI `b`)**:
  - Pass or paste a raw `Cookie` header value via `--cookie` / `-b` on the CLI or by pressing `b` live inside the interactive TUI.
  - Automatically exchanges logged-in WordPress cookies (`wordpress_logged_in_*`) for a `wp_rest` `X-WP-Nonce` token via `/wp-admin/admin-ajax.php?action=rest-nonce` (with HTML `wpApiSettings.nonce` fallback) so WordPress Core `rest_cookie_check_errors()` authenticates REST API requests without requiring manual nonce extraction.
  - Propagates `Cookie` and `Referer` headers to the concurrent downloader pool and resolves `Content-Disposition` filenames for dynamic WooCommerce/EDD download URLs (`?download_file=...`, `?eddfile=...`).
- **WooCommerce & Easy Digital Downloads (EDD) Directory + API Indexing & Search (`internal/wp/directories.go`, `quarry dirs`, TUI Tab `9`)**:
  - Recursive autoindex directory crawler for `/wp-content/uploads/woocommerce_uploads/`, `/wp-content/uploads/edd/`, and `/wp-content/uploads/` (including active `YYYY/MM/` subfolder probing, modification timestamp parsing, and file size extraction).
  - Multi-page REST API harvesting across WooCommerce Store API (`/wc/store/v1/products`), WooCommerce v3 API (`/wc/v3/products`), WooCommerce CPT (`/wp/v2/product`), EDD CPT (`/wp/v2/download`), EDD native API (`/?edd-api=products`), and customer account downloads (`/my-account/downloads/`).
  - Targeted `_wp_attached_file` path queries on `/wp/v2/media` (`search=woocommerce_uploads`, `search=edd`).
  - Automatic fallback in `FetchPosts` for `product` and `download` resources when `/wp/v2/product` or `/wp/v2/download` is disabled.
- **Media Exclusion Filters, Presets & Pre-Download Size Estimation (`--ext`, `--exclude-type`, `--exclude-ext`, `--dry-run`, TUI `D`)**:
  - Disjoint multi-stream media query planner (`ExpandMediaQueryPlan`) and polymorphic PHP-to-Go JSON boundary (`internal/wp/flex.go`).

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
