package wp

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	reHeading1    = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reHeading2    = regexp.MustCompile(`(?is)<h2[^>]*>(.*?)</h2>`)
	reHeading3    = regexp.MustCompile(`(?is)<h3[^>]*>(.*?)</h3>`)
	reHeading4    = regexp.MustCompile(`(?is)<h4[^>]*>(.*?)</h4>`)
	reHeading56   = regexp.MustCompile(`(?is)<h[56][^>]*>(.*?)</h[56]>`)
	rePreCode     = regexp.MustCompile(`(?is)<pre[^>]*>\s*<code[^>]*>(.*?)</code>\s*</pre>`)
	rePre         = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	reInlineCode  = regexp.MustCompile(`(?is)<code[^>]*>(.*?)</code>`)
	reStrong      = regexp.MustCompile(`(?is)<(strong|b)(?:\s[^>]*)?>(.*?)</(strong|b)>`)
	reEm          = regexp.MustCompile(`(?is)<(em|i)(?:\s[^>]*)?>(.*?)</(em|i)>`)
	reBlockquote  = regexp.MustCompile(`(?is)<blockquote[^>]*>(.*?)</blockquote>`)
	reLink        = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reImage       = regexp.MustCompile(`(?is)<img[^>]*src=["']([^"']+)["'][^>]*alt=["']([^"']*)["'][^>]*/?>`)
	reImageNoAlt  = regexp.MustCompile(`(?is)<img[^>]*src=["']([^"']+)["'][^>]*/?>`)
	reListItem    = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	reParagraph   = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	reLineBreak   = regexp.MustCompile(`(?is)<br\s*/?>`)
	reHR          = regexp.MustCompile(`(?is)<hr\s*/?>`)
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reAnyTag      = regexp.MustCompile(`(?is)<[^>]+>`)
	reMultiBlank  = regexp.MustCompile(`\n{3,}`)
)

// CleanPlainText strips all HTML tags and decodes HTML entities into a single clean text string.
func CleanPlainText(rawHTML string) string {
	if rawHTML == "" {
		return ""
	}
	s := reScriptStyle.ReplaceAllString(rawHTML, "")
	s = reLineBreak.ReplaceAllString(s, " ")
	s = reParagraph.ReplaceAllString(s, "$1 ")
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// HTMLToMarkdown converts WordPress Gutenberg / Classic HTML into clean, readable Markdown.
func HTMLToMarkdown(rawHTML string) string {
	if strings.TrimSpace(rawHTML) == "" {
		return ""
	}
	s := reScriptStyle.ReplaceAllString(rawHTML, "")

	// Handle code blocks first
	s = rePreCode.ReplaceAllStringFunc(s, func(m string) string {
		sub := rePreCode.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		code := html.UnescapeString(reAnyTag.ReplaceAllString(sub[1], ""))
		return "\n\n```\n" + strings.TrimSpace(code) + "\n```\n\n"
	})
	s = rePre.ReplaceAllStringFunc(s, func(m string) string {
		sub := rePre.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		code := html.UnescapeString(reAnyTag.ReplaceAllString(sub[1], ""))
		return "\n\n```\n" + strings.TrimSpace(code) + "\n```\n\n"
	})

	// Headings
	s = reHeading1.ReplaceAllStringFunc(s, func(m string) string {
		sub := reHeading1.FindStringSubmatch(m)
		return "\n\n# " + CleanPlainText(sub[1]) + "\n\n"
	})
	s = reHeading2.ReplaceAllStringFunc(s, func(m string) string {
		sub := reHeading2.FindStringSubmatch(m)
		return "\n\n## " + CleanPlainText(sub[1]) + "\n\n"
	})
	s = reHeading3.ReplaceAllStringFunc(s, func(m string) string {
		sub := reHeading3.FindStringSubmatch(m)
		return "\n\n### " + CleanPlainText(sub[1]) + "\n\n"
	})
	s = reHeading4.ReplaceAllStringFunc(s, func(m string) string {
		sub := reHeading4.FindStringSubmatch(m)
		return "\n\n#### " + CleanPlainText(sub[1]) + "\n\n"
	})
	s = reHeading56.ReplaceAllStringFunc(s, func(m string) string {
		sub := reHeading56.FindStringSubmatch(m)
		return "\n\n##### " + CleanPlainText(sub[1]) + "\n\n"
	})

	// Images & Links
	s = reImage.ReplaceAllStringFunc(s, func(m string) string {
		sub := reImage.FindStringSubmatch(m)
		if len(sub) < 3 {
			return ""
		}
		return fmt.Sprintf("\n![%s](%s)\n", html.UnescapeString(sub[2]), sub[1])
	})
	s = reImageNoAlt.ReplaceAllStringFunc(s, func(m string) string {
		sub := reImageNoAlt.FindStringSubmatch(m)
		if len(sub) < 2 {
			return ""
		}
		return fmt.Sprintf("\n![](%s)\n", sub[1])
	})
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := reLink.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		label := CleanPlainText(sub[2])
		if label == "" {
			label = sub[1]
		}
		return fmt.Sprintf("[%s](%s)", label, sub[1])
	})

	// Inline formatting
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		sub := reInlineCode.FindStringSubmatch(m)
		return "`" + html.UnescapeString(reAnyTag.ReplaceAllString(sub[1], "")) + "`"
	})
	s = reStrong.ReplaceAllString(s, "**$2**")
	s = reEm.ReplaceAllString(s, "*$2*")

	// Blockquote
	s = reBlockquote.ReplaceAllStringFunc(s, func(m string) string {
		sub := reBlockquote.FindStringSubmatch(m)
		text := CleanPlainText(sub[1])
		return "\n\n> " + text + "\n\n"
	})

	// Lists
	s = reListItem.ReplaceAllStringFunc(s, func(m string) string {
		sub := reListItem.FindStringSubmatch(m)
		return "\n- " + strings.TrimSpace(reAnyTag.ReplaceAllString(sub[1], ""))
	})

	// Paragraphs & Breaks
	s = reHR.ReplaceAllString(s, "\n\n---\n\n")
	s = reLineBreak.ReplaceAllString(s, "\n")
	s = reParagraph.ReplaceAllString(s, "\n\n$1\n\n")

	// Strip remaining tags & decode entities
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	// Normalize whitespace lines
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	s = strings.Join(lines, "\n")
	s = reMultiBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// ExtractMediaURLsFromHTML finds all `<img src="...">`, `<video src="...">`, `<audio src="...">`, and `href` pointing to media/archive/spreadsheet/document assets in HTML.
func ExtractMediaURLsFromHTML(rawHTML string) []string {
	if rawHTML == "" {
		return nil
	}
	reSrc := regexp.MustCompile(`(?i)(?:src|href)=["'](https?://[^"'?#]+\.(?:jpg|jpeg|png|gif|webp|avif|svg|mp4|webm|mp3|wav|ogg|pdf|zip|rar|7z|tar|gz|tgz|csv|tsv|xls|xlsx|ods|doc|docx|ppt|pptx|json|xml|sql|txt)(?:\?[^"']*)?)["']`)
	matches := reSrc.FindAllStringSubmatch(rawHTML, -1)
	seen := make(map[string]struct{}, len(matches))
	var out []string
	for _, m := range matches {
		if len(m) > 1 {
			u := m[1]
			if _, ok := seen[u]; !ok {
				seen[u] = struct{}{}
				out = append(out, u)
			}
		}
	}
	return out
}
