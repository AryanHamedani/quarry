# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |

## Security Considerations

- **Path Traversal Protection**: All slugs, filenames, and relative `wp-content/uploads/` paths extracted from remote WordPress servers are sanitized before writing to disk.
- **Atomic Downloads**: Media assets are streamed to temporary `.part` files and renamed on completion.
- **Credentials**: When using `--token` or `--app-password`, prefer passing credentials via environment variables or short-lived tokens rather than committing them to shell scripts.

## Reporting a Vulnerability

Please report security vulnerabilities privately via [GitHub Security Advisories](https://github.com/AryanHamedani/quarry/security/advisories/new) rather than opening a public issue.
