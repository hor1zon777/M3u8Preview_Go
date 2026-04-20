// Package parser
// json.go 对齐 parsers/jsonParser.ts：兼容数组 / { items } / { media } 三种外层包装。
package parser

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
)

// ParseJSON 解析 JSON 内容。
//
// 输入兼容：
//   - 根节点是数组
//   - 根节点对象里有 "items" 或 "media" 数组
//
// 字段兼容：
//   - title / m3u8Url / posterUrl / description / year / artist / categoryName / tagNames
//   - m3u8_url / poster_url / poster / url / artistName / 作者 / category
func ParseJSON(content string) ([]dto.ImportItem, error) {
	var root any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}

	raw := extractItems(root)
	out := make([]dto.ImportItem, 0, len(raw))
	for _, r := range raw {
		obj, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, dto.ImportItem{
			Title:        firstStr(obj, "title"),
			M3u8URL:      firstStr(obj, "m3u8Url", "m3u8_url", "url"),
			PosterURL:    firstStrPtr(obj, "posterUrl", "poster_url", "poster"),
			Description:  firstStrPtr(obj, "description"),
			Year:         firstIntPtr(obj, "year"),
			Artist:       firstStrPtr(obj, "artist", "artistName", "作者"),
			CategoryName: firstStrPtr(obj, "category", "categoryName"),
			TagNames:     firstStringArray(obj, "tags", "tagNames"),
		})
	}
	return out, nil
}

func extractItems(root any) []any {
	switch v := root.(type) {
	case []any:
		return v
	case map[string]any:
		if items, ok := v["items"].([]any); ok {
			return items
		}
		if media, ok := v["media"].([]any); ok {
			return media
		}
	}
	return nil
}

func firstStr(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		if raw, ok := obj[k]; ok {
			if s, ok2 := raw.(string); ok2 && s != "" {
				return s
			}
		}
	}
	return ""
}

func firstStrPtr(obj map[string]any, keys ...string) *string {
	s := firstStr(obj, keys...)
	if s == "" {
		return nil
	}
	return &s
}

func firstIntPtr(obj map[string]any, keys ...string) *int {
	for _, k := range keys {
		raw, ok := obj[k]
		if !ok {
			continue
		}
		switch vv := raw.(type) {
		case float64:
			n := int(vv)
			return &n
		case int:
			return &vv
		case string:
			if vv == "" {
				continue
			}
			n, err := strconv.Atoi(vv)
			if err == nil {
				return &n
			}
		}
	}
	return nil
}

func firstStringArray(obj map[string]any, keys ...string) []string {
	for _, k := range keys {
		raw, ok := obj[k]
		if !ok {
			continue
		}
		switch vv := raw.(type) {
		case []any:
			out := make([]string, 0, len(vv))
			for _, e := range vv {
				if s, ok2 := e.(string); ok2 && s != "" {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out
			}
		case []string:
			return vv
		}
	}
	return nil
}
