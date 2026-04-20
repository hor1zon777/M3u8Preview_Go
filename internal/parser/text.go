// Package parser
// text.go 对齐 parsers/textParser.ts：
//   - 行首 # 注释
//   - URL only：从文件名推 title（decodeURIComponent）
//   - pipe 分隔 2 段：title|url
//   - 3 段：title|url|artist
//   - 4 段：title|url|category|tags(逗号分隔)
//   - 5 段及以上：title|url|category|tags|artist
package parser

import (
	"net/url"
	"path"
	"strings"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
)

// ParseText 解析 text 行。
func ParseText(content string) []dto.ImportItem {
	lines := strings.Split(content, "\n")
	out := make([]dto.ImportItem, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.Contains(line, "|") {
			parts := strings.Split(line, "|")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			item := dto.ImportItem{Title: safeIdx(parts, 0), M3u8URL: safeIdx(parts, 1)}
			switch {
			case len(parts) == 2:
				// only title|url
			case len(parts) == 3:
				item.Artist = optStr(safeIdx(parts, 2))
			case len(parts) == 4:
				item.CategoryName = optStr(safeIdx(parts, 2))
				item.TagNames = splitTags(safeIdx(parts, 3))
			default: // >=5
				item.CategoryName = optStr(safeIdx(parts, 2))
				item.TagNames = splitTags(safeIdx(parts, 3))
				item.Artist = optStr(safeIdx(parts, 4))
			}
			out = append(out, item)
			continue
		}

		// URL-only：从最后一段 path 推 title，去掉 .m3u8 及其 query/fragment
		u := line
		base := lastPathSegment(u)
		base = stripM3U8Suffix(base)
		title := base
		if decoded, err := url.QueryUnescape(base); err == nil && decoded != "" {
			title = decoded
		}
		if title == "" {
			title = "Untitled"
		}
		out = append(out, dto.ImportItem{Title: title, M3u8URL: u})
	}
	return out
}

func safeIdx(a []string, i int) string {
	if i < 0 || i >= len(a) {
		return ""
	}
	return a[i]
}

func lastPathSegment(s string) string {
	if s == "" {
		return ""
	}
	s = strings.SplitN(s, "#", 2)[0]
	s = strings.SplitN(s, "?", 2)[0]
	return path.Base(s)
}

func stripM3U8Suffix(s string) string {
	idx := strings.Index(strings.ToLower(s), ".m3u8")
	if idx >= 0 {
		return s[:idx]
	}
	return s
}
