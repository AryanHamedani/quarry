# Contributing to Quarry

Thank you for your interest in contributing to **Quarry**!

## Development Setup

Requirements:
- Go 1.23 or newer

```bash
git clone https://github.com/AryanHamedani/quarry.git
cd quarry
go test -race ./...
go build -o bin/quarry ./cmd/quarry
```

## Project Layout

- `cmd/quarry/` — Main binary entrypoint and signal handling.
- `internal/wp/` — WordPress REST API v2 discovery, rate-limited HTTP client, pagination engine, and HTML-to-Markdown converter.
- `internal/tui/` — Bubble Tea interactive split-pane Terminal UI.
- `internal/exporter/` — Exporters for Markdown (with YAML frontmatter), JSON, JSONL, CSV, and HTML5.
- `internal/downloader/` — Concurrent media worker pool with SHA-256 verification and resume support.
- `internal/cli/` — Cobra CLI subcommands (`inspect`, `query`, `export`, `media`, `tui`).

## Pull Request Checklist

1. Ensure `go fmt ./...` and `go vet ./...` pass cleanly.
2. Add or update unit tests in `*_test.go` and verify `go test -race ./...` passes.
3. Keep commits focused and descriptive.
