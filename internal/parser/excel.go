// Package parser
// excel.go 使用 xuri/excelize/v2 读取 .xlsx 第一个 sheet 的表头 + 行数据。
// 对齐 parsers/excelParser.ts 的键映射与类型归一化。
package parser

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
)

// ParseExcel 解析 xlsx 字节流。失败统一返回带业务语义的错误。
func ParseExcel(data []byte) ([]dto.ImportItem, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("Excel 内容为空")
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法解析 Excel 文件，请检查文件格式")
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("读取 Excel 第 1 个 sheet 失败: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil
	}

	headers := make([]string, len(rows[0]))
	for i, h := range rows[0] {
		headers[i] = strings.TrimSpace(h)
	}

	out := make([]dto.ImportItem, 0, len(rows)-1)
	for _, row := range rows[1:] {
		m := rowToMap(headers, row)
		if len(m) == 0 {
			continue
		}
		out = append(out, dto.ImportItem{
			Title:        pickStr(m, "title", "标题", "Title"),
			M3u8URL:      pickStr(m, "m3u8Url", "m3u8_url", "url", "URL", "链接"),
			PosterURL:    pickStrPtr(m, "posterUrl", "poster_url", "poster", "海报"),
			Description:  pickStrPtr(m, "description", "描述", "Description"),
			Year:         pickIntPtr(m, "year", "年份", "Year"),
			Artist:       pickStrPtr(m, "artist", "作者", "演员", "Artist"),
			CategoryName: pickStrPtr(m, "category", "分类", "Category"),
			TagNames:     splitTags(pickStr(m, "tags", "标签", "Tags")),
		})
	}
	return out, nil
}

func rowToMap(headers, row []string) map[string]string {
	m := make(map[string]string, len(headers))
	nonEmpty := false
	for i, h := range headers {
		if h == "" || i >= len(row) {
			continue
		}
		v := strings.TrimSpace(row[i])
		if v != "" {
			nonEmpty = true
		}
		m[h] = v
	}
	if !nonEmpty {
		return nil
	}
	return m
}

func pickStr(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func pickStrPtr(m map[string]string, keys ...string) *string {
	s := pickStr(m, keys...)
	if s == "" {
		return nil
	}
	return &s
}

func pickIntPtr(m map[string]string, keys ...string) *int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == "" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return &n
		}
	}
	return nil
}
