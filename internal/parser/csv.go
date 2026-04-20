// Package parser 把不同格式的导入文件统一解析为 dto.ImportItem。
// 键映射回退表严格对齐 TS 原版 parsers/*Parser.ts。
package parser

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
)

// ParseCSV 解析 CSV 内容，支持中英文表头 + 多列别名。
// 对齐 parsers/csvParser.ts：
//   - header 小写化 + 去空白
//   - m3u8Url: m3u8url / m3u8_url / url / 链接
//   - posterUrl: posterurl / poster_url / poster / 海报
//   - year: year / 年份
//   - artist: artist / 作者 / 演员
//   - category: category / 分类
//   - tags: tags / 标签（逗号分隔）
func ParseCSV(content string) ([]dto.ImportItem, error) {
	r := csv.NewReader(strings.NewReader(stripBOM(content)))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	headers := make([]string, len(rows[0]))
	for i, h := range rows[0] {
		headers[i] = strings.ToLower(strings.TrimSpace(h))
	}

	out := make([]dto.ImportItem, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if isEmptyRow(row) {
			continue
		}
		m := toMap(headers, row)
		out = append(out, dto.ImportItem{
			Title:        strPtrOrEmpty(firstNonEmpty(m, "title", "标题")),
			M3u8URL:      firstNonEmpty(m, "m3u8url", "m3u8_url", "url", "链接"),
			PosterURL:    optStr(firstNonEmpty(m, "posterurl", "poster_url", "poster", "海报")),
			Description:  optStr(firstNonEmpty(m, "description", "描述")),
			Year:         optInt(firstNonEmpty(m, "year", "年份")),
			Artist:       optStr(firstNonEmpty(m, "artist", "作者", "演员")),
			CategoryName: optStr(firstNonEmpty(m, "category", "分类")),
			TagNames:     splitTags(firstNonEmpty(m, "tags", "标签")),
		})
	}
	return out, nil
}

// stripBOM 去 UTF-8 BOM（前端 CSV 导出可能带）。
func stripBOM(s string) string {
	if strings.HasPrefix(s, "\ufeff") {
		return strings.TrimPrefix(s, "\ufeff")
	}
	if bytes.HasPrefix([]byte(s), []byte{0xEF, 0xBB, 0xBF}) {
		return string(bytes.TrimPrefix([]byte(s), []byte{0xEF, 0xBB, 0xBF}))
	}
	return s
}

func isEmptyRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func toMap(headers, row []string) map[string]string {
	m := make(map[string]string, len(headers))
	for i, h := range headers {
		if h == "" {
			continue
		}
		if i < len(row) {
			m[h] = strings.TrimSpace(row[i])
		}
	}
	return m
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func optStr(v string) *string {
	if v == "" {
		return nil
	}
	s := v
	return &s
}

func optInt(v string) *int {
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return nil
	}
	return &n
}

func splitTags(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// strPtrOrEmpty 返回非空字符串值本身，空值返回 ""（title 必填场景用）。
func strPtrOrEmpty(v string) string {
	return v
}
