package wp

import (
	"net/url"
	"path"
	"strings"
)

// AllWPMediaTypes lists the 6 official `media_type` enum values accepted by WordPress `/wp/v2/media`.
// Note: WordPress categorizes archives (.zip), PDFs, Word docs, and Excel spreadsheets under `file` or `application` or `text`.
var AllWPMediaTypes = []string{"image", "file", "video", "audio", "text", "application"}

// mimePresets maps friendly extension/category names to the MIME types WordPress uses in `/wp/v2/media?mime_type=...`
// and the file extensions they correspond to.
var mimePresets = map[string]struct {
	mimes []string
	exts  []string
}{
	"image": {
		mimes: []string{"image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "image/svg+xml"},
		exts:  []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif", ".svg", ".bmp", ".ico"},
	},
	"zip": {
		mimes: []string{"application/zip", "application/x-zip-compressed"},
		exts:  []string{".zip"},
	},
	"rar": {
		mimes: []string{"application/x-rar-compressed", "application/vnd.rar"},
		exts:  []string{".rar"},
	},
	"7z": {
		mimes: []string{"application/x-7z-compressed"},
		exts:  []string{".7z"},
	},
	"tar": {
		mimes: []string{"application/x-tar", "application/gzip", "application/x-gzip"},
		exts:  []string{".tar", ".gz", ".tgz"},
	},
	"archive": {
		mimes: []string{"application/zip", "application/x-zip-compressed", "application/x-rar-compressed", "application/vnd.rar", "application/x-7z-compressed", "application/x-tar", "application/gzip"},
		exts:  []string{".zip", ".rar", ".7z", ".tar", ".gz", ".tgz"},
	},
	"csv": {
		mimes: []string{"text/csv", "application/csv", "text/comma-separated-values"},
		exts:  []string{".csv"},
	},
	"tsv": {
		mimes: []string{"text/tab-separated-values"},
		exts:  []string{".tsv"},
	},
	"xls": {
		mimes: []string{"application/vnd.ms-excel"},
		exts:  []string{".xls"},
	},
	"xlsx": {
		mimes: []string{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		exts:  []string{".xlsx"},
	},
	"excel": {
		mimes: []string{
			"application/vnd.ms-excel",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"application/vnd.ms-excel.sheet.macroEnabled.12",
			"application/vnd.oasis.opendocument.spreadsheet",
		},
		exts: []string{".xls", ".xlsx", ".xlsm", ".ods"},
	},
	"spreadsheet": {
		mimes: []string{
			"text/csv",
			"application/csv",
			"text/tab-separated-values",
			"application/vnd.ms-excel",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"application/vnd.oasis.opendocument.spreadsheet",
		},
		exts: []string{".csv", ".tsv", ".xls", ".xlsx", ".xlsm", ".ods"},
	},
	"pdf": {
		mimes: []string{"application/pdf"},
		exts:  []string{".pdf"},
	},
	"doc": {
		mimes: []string{"application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		exts:  []string{".doc", ".docx"},
	},
	"json": {
		mimes: []string{"application/json"},
		exts:  []string{".json"},
	},
}

// ExpandMediaFilter translates user-supplied `Extensions` (like `zip,csv,excel`) and `MimeType`
// into:
//  1. A list of concrete `mime_type` query values for the WordPress REST API.
//  2. A matcher function to verify a `MediaItem` matches the inclusion and exclusion rules.
func ExpandMediaFilter(q QueryFilter) (apiMimes []string, matcher func(MediaItem) bool) {
	seenMime := make(map[string]struct{})
	seenExt := make(map[string]struct{})

	addMime := func(m string) {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == "" {
			return
		}
		if _, ok := seenMime[m]; !ok {
			seenMime[m] = struct{}{}
			apiMimes = append(apiMimes, m)
		}
	}
	var allowedExts []string
	addExt := func(e string) {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			return
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if _, ok := seenExt[e]; !ok {
			seenExt[e] = struct{}{}
			allowedExts = append(allowedExts, e)
		}
	}

	// Parse MimeType
	for _, token := range strings.Split(q.MimeType, ",") {
		tok := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(token, ".")))
		if tok == "" {
			continue
		}
		if preset, ok := mimePresets[tok]; ok {
			for _, m := range preset.mimes {
				addMime(m)
			}
			for _, e := range preset.exts {
				addExt(e)
			}
		} else {
			addMime(tok)
		}
	}

	// Parse Extensions
	for _, rawItem := range q.Extensions {
		for _, token := range strings.Split(rawItem, ",") {
			tok := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(token, ".")))
			if tok == "" {
				continue
			}
			if preset, ok := mimePresets[tok]; ok {
				for _, m := range preset.mimes {
					addMime(m)
				}
				for _, e := range preset.exts {
					addExt(e)
				}
			} else {
				addExt(tok)
			}
		}
	}

	// Parse Exclusions (ExcludeMediaTypes and ExcludeExtensions)
	exclTypes := make(map[string]struct{})
	exclMimes := make(map[string]struct{})
	exclExts := make(map[string]struct{})

	for _, raw := range q.ExcludeMediaTypes {
		for _, token := range strings.Split(raw, ",") {
			tok := strings.ToLower(strings.TrimSpace(token))
			if tok == "" {
				continue
			}
			exclTypes[tok] = struct{}{}
			if preset, ok := mimePresets[tok]; ok {
				for _, m := range preset.mimes {
					exclMimes[m] = struct{}{}
				}
				for _, e := range preset.exts {
					exclExts[e] = struct{}{}
				}
			}
		}
	}

	for _, raw := range q.ExcludeExtensions {
		for _, token := range strings.Split(raw, ",") {
			tok := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(token, ".")))
			if tok == "" {
				continue
			}
			if preset, ok := mimePresets[tok]; ok {
				for _, m := range preset.mimes {
					exclMimes[m] = struct{}{}
				}
				for _, e := range preset.exts {
					exclExts[e] = struct{}{}
				}
			} else {
				exclExts["."+tok] = struct{}{}
			}
		}
	}

	hasInclude := len(apiMimes) > 0 || len(allowedExts) > 0
	hasExclude := len(exclTypes) > 0 || len(exclMimes) > 0 || len(exclExts) > 0
	var dirScopes []DirectoryScope
	if len(q.DirectoryScopes) > 0 {
		dirScopes = NormalizeDirectoryScopes(q.DirectoryScopes)
	}

	if !hasInclude && !hasExclude && len(dirScopes) == 0 {
		return nil, nil
	}

	matcher = func(item MediaItem) bool {
		if len(dirScopes) > 0 {
			if !MatchesDirectoryScope(item.SourceURL, item.MediaDetails.File, item.Type+" "+item.AltText, dirScopes) {
				return false
			}
		}
		itemType := strings.ToLower(strings.TrimSpace(item.MediaType))
		itemMime := strings.ToLower(strings.TrimSpace(item.MimeType))
		ext := extractURLFileExt(item.SourceURL)
		if ext == "" && item.MediaDetails.File != "" {
			ext = strings.ToLower(path.Ext(item.MediaDetails.File))
		}

		// 1. Check Exclusions first
		if hasExclude {
			if _, blocked := exclTypes[itemType]; blocked {
				return false
			}
			for t := range exclTypes {
				if strings.HasPrefix(itemMime, t+"/") {
					return false
				}
			}
			if _, blocked := exclMimes[itemMime]; blocked {
				return false
			}
			if ext != "" {
				if _, blocked := exclExts[ext]; blocked {
					return false
				}
			}
		}

		// 2. Check Inclusions if any were specified
		if !hasInclude {
			return true
		}
		for m := range seenMime {
			if itemMime == m || strings.HasPrefix(itemMime, m+"/") {
				return true
			}
		}
		if ext != "" {
			if _, ok := seenExt[ext]; ok {
				return true
			}
		}
		return false
	}

	return apiMimes, matcher
}

// MatchesMediaFilter evaluates whether a single MediaItem satisfies the QueryFilter's
// inclusion, exclusion, and directory scope rules.
func MatchesMediaFilter(item MediaItem, q QueryFilter) bool {
	if strings.TrimSpace(q.MediaType) != "" {
		allowed := false
		itemMT := strings.ToLower(strings.TrimSpace(item.MediaType))
		for _, mt := range strings.Split(q.MediaType, ",") {
			mt = strings.ToLower(strings.TrimSpace(mt))
			if mt != "" && (itemMT == mt || strings.HasPrefix(strings.ToLower(item.MimeType), mt+"/")) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	_, matcher := ExpandMediaFilter(q)
	if matcher == nil {
		return true
	}
	return matcher(item)
}

// ExtractFileExtension extracts the lowercase file extension (without leading dot) from a URL or file path.
func ExtractFileExtension(rawURL string) string {
	return strings.TrimPrefix(extractURLFileExt(rawURL), ".")
}

// ExpandMediaQueryPlan compiles a QueryFilter into:
//  1. A slice of disjoint sub-queries (`subQueries`) where each sub-query maps 1-to-1 to a single
//     WordPress REST API collection stream with its own independent pagination (`X-WP-TotalPages`).
//  2. A deterministic `matcher` predicate that enforces all inclusion and exclusion rules on returned items.
func ExpandMediaQueryPlan(q QueryFilter) (subQueries []QueryFilter, matcher func(MediaItem) bool) {
	apiMimes, matcher := ExpandMediaFilter(q)
	mediaTypes := ResolveRemainingMediaTypes(q.MediaType, q.ExcludeMediaTypes)

	base := q
	base.Endpoint = "media"
	base.Embed = false

	if len(apiMimes) > 1 {
		for _, mime := range apiMimes {
			sub := base
			sub.MimeType = mime
			sub.Extensions = nil
			if len(mediaTypes) == 1 {
				sub.MediaType = mediaTypes[0]
			} else {
				sub.MediaType = ""
			}
			subQueries = append(subQueries, sub)
		}
		return subQueries, matcher
	}

	if len(mediaTypes) > 1 {
		for _, mt := range mediaTypes {
			sub := base
			sub.MediaType = mt
			if len(apiMimes) == 1 {
				sub.MimeType = apiMimes[0]
			}
			subQueries = append(subQueries, sub)
		}
		return subQueries, matcher
	}

	single := base
	if len(apiMimes) == 1 {
		single.MimeType = apiMimes[0]
	}
	if len(mediaTypes) == 1 {
		single.MediaType = mediaTypes[0]
	}
	return []QueryFilter{single}, matcher
}

// ResolveRemainingMediaTypes returns the subset of AllWPMediaTypes not excluded by excludeTypes,
// allowing Quarry to query non-excluded `media_type` endpoints directly on WordPress without scanning excluded types (e.g. skipping 10,000 images).
func ResolveRemainingMediaTypes(includeMediaType string, excludeTypes []string) []string {
	if strings.TrimSpace(includeMediaType) != "" {
		var out []string
		for _, t := range strings.Split(includeMediaType, ",") {
			if s := strings.ToLower(strings.TrimSpace(t)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if len(excludeTypes) == 0 {
		return nil
	}
	excluded := make(map[string]bool)
	for _, raw := range excludeTypes {
		for _, tok := range strings.Split(raw, ",") {
			tok = strings.ToLower(strings.TrimSpace(tok))
			if tok != "" {
				excluded[tok] = true
			}
		}
	}
	var remaining []string
	for _, mt := range AllWPMediaTypes {
		if !excluded[mt] {
			remaining = append(remaining, mt)
		}
	}
	return remaining
}

func extractURLFileExt(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(path.Ext(rawURL))
	}
	return strings.ToLower(path.Ext(u.Path))
}
