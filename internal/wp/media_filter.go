package wp

import (
	"net/url"
	"path"
	"strings"
)

// mimePresets maps friendly extension/category names to the MIME types WordPress uses in `/wp/v2/media?mime_type=...`
// and the file extensions they correspond to.
var mimePresets = map[string]struct {
	mimes []string
	exts  []string
}{
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
//  2. A matcher function to verify a `MediaItem` matches either the requested MIME types or file extensions.
func ExpandMediaFilter(mimeRaw string, extsRaw []string) (apiMimes []string, matcher func(MediaItem) bool) {
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

	// Parse mimeRaw (can be comma-separated or presets like "zip,csv,excel")
	for _, token := range strings.Split(mimeRaw, ",") {
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

	// Parse extsRaw
	for _, rawItem := range extsRaw {
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

	if len(apiMimes) == 0 && len(allowedExts) == 0 {
		return nil, nil
	}

	matcher = func(item MediaItem) bool {
		itemMime := strings.ToLower(strings.TrimSpace(item.MimeType))
		for m := range seenMime {
			if itemMime == m || strings.HasPrefix(itemMime, m+"/") {
				return true
			}
		}
		ext := extractURLFileExt(item.SourceURL)
		if ext == "" && item.MediaDetails.File != "" {
			ext = strings.ToLower(path.Ext(item.MediaDetails.File))
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
