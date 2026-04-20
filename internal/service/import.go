// Package service
// import_.go 对齐 importService.ts：检测格式 → 解析 → 预览校验 → 事务批量写入 + 外部封面预下载。
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/parser"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// 最多支持的单次导入条目数。
const maxImportItems = 1000

// ImportFormat 可用枚举（仅用于字符串 normalization）。
const (
	fmtCSV   = "CSV"
	fmtExcel = "EXCEL"
	fmtJSON  = "JSON"
	fmtText  = "TEXT"
)

// xlsx magic bytes — 对齐 importController.ts 的 PK 前缀校验。
var xlsxMagicPrefixes = [][]byte{
	{0x50, 0x4b, 0x03, 0x04},
	{0x50, 0x4b, 0x05, 0x06},
	{0x50, 0x4b, 0x07, 0x08},
}

// ImportService 封装批量导入业务。
type ImportService struct {
	db     *gorm.DB
	thumb  ThumbnailEnqueuer
	poster PosterMigrator
}

// NewImportService 构造。
// poster 采用 PosterMigrator（异步入队）而非 PosterResolver（同步下载）：
// 批量导入时外部 posterUrl 下载不再阻塞响应，worker 池在后台按速率限制顺次处理。
func NewImportService(db *gorm.DB, thumb ThumbnailEnqueuer, poster PosterMigrator) *ImportService {
	if thumb == nil {
		thumb = NoopThumbnailEnqueuer{}
	}
	if poster == nil {
		poster = NoopPosterMigrator{}
	}
	return &ImportService{db: db, thumb: thumb, poster: poster}
}

// DetectAndParseFile 从文件扩展名判定格式并解析。
func (s *ImportService) DetectAndParseFile(filename string, content []byte) ([]dto.ImportItem, string, error) {
	ext := strings.ToLower(extFromName(filename))
	switch ext {
	case ".csv":
		items, err := parser.ParseCSV(string(content))
		if err != nil {
			return nil, "", middleware.WrapAppError(http.StatusBadRequest, "CSV 解析失败", err)
		}
		return items, fmtCSV, nil
	case ".xlsx":
		if !magicMatches(content, xlsxMagicPrefixes) {
			return nil, "", middleware.NewAppError(http.StatusBadRequest, "文件内容与扩展名不匹配，拒绝处理")
		}
		items, err := parser.ParseExcel(content)
		if err != nil {
			return nil, "", middleware.WrapAppError(http.StatusBadRequest, err.Error(), err)
		}
		return items, fmtExcel, nil
	case ".json":
		items, err := parser.ParseJSON(string(content))
		if err != nil {
			return nil, "", middleware.WrapAppError(http.StatusBadRequest, "JSON 解析失败", err)
		}
		return items, fmtJSON, nil
	case ".txt":
		return parser.ParseText(string(content)), fmtText, nil
	default:
		return nil, "", middleware.NewAppError(http.StatusBadRequest, fmt.Sprintf("Unsupported file format: %s", strings.TrimPrefix(ext, ".")))
	}
}

// DetectAndParseBody 从 JSON body 的 { format, content } 解析内容。
func (s *ImportService) DetectAndParseBody(format, content string) ([]dto.ImportItem, string, error) {
	if content == "" {
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "No file or content provided")
	}
	switch strings.ToUpper(format) {
	case fmtCSV:
		items, err := parser.ParseCSV(content)
		if err != nil {
			return nil, "", middleware.WrapAppError(http.StatusBadRequest, "CSV 解析失败", err)
		}
		return items, fmtCSV, nil
	case fmtJSON:
		items, err := parser.ParseJSON(content)
		if err != nil {
			return nil, "", middleware.WrapAppError(http.StatusBadRequest, "JSON 解析失败", err)
		}
		return items, fmtJSON, nil
	default: // 包含 TEXT 与空串
		return parser.ParseText(content), fmtText, nil
	}
}

// Preview 校验所有 item 并输出统计。
func (s *ImportService) Preview(items []dto.ImportItem) dto.ImportPreviewResponse {
	if len(items) > maxImportItems {
		// caller 应提前 reject；这里保守截断
		items = items[:maxImportItems]
	}
	errs := make([]dto.ImportError, 0)
	valid := 0
	for i, it := range items {
		itemErrs := validateImportItem(i+1, it)
		if len(itemErrs) == 0 {
			valid++
			continue
		}
		errs = append(errs, itemErrs...)
	}
	return dto.ImportPreviewResponse{
		Items:        items,
		TotalCount:   len(items),
		ValidCount:   valid,
		InvalidCount: len(items) - valid,
		Errors:       errs,
	}
}

// Execute 将校验通过的条目写入 DB（事务），再在事务外触发缩略图生成 / 海报异步迁移。
// 性能优化：不再在事务前同步下载外部海报（原串行循环，单张超时 15s，1000 条最差可达 4+ 小时），
// 改为事务内保留原 URL → 事务外按 URL 路径分流入两条后台队列：
//
//	posterUrl == nil/""     → ThumbnailEnqueuer（ffmpeg 截帧生成缩略图）
//	posterUrl ~ http(s)://  → PosterMigrator（worker 池下载，完成后回写 poster_url）
//	posterUrl 本地 /uploads/ → 不处理
//
// 这样导入响应时间与条目数量基本解耦，后台并发 + 限流由 PosterDownloader 统一控制。
func (s *ImportService) Execute(userID string, items []dto.ImportItem, format, fileName string) (dto.ImportResult, error) {
	if len(items) > maxImportItems {
		return dto.ImportResult{}, middleware.NewAppError(http.StatusBadRequest, fmt.Sprintf("Maximum %d items per import", maxImportItems))
	}

	errs := make([]dto.ImportError, 0)
	valid := make([]dto.ImportItem, 0, len(items))
	rowIdx := make([]int, 0, len(items))
	for i, it := range items {
		ve := validateImportItem(i+1, it)
		if len(ve) > 0 {
			errs = append(errs, ve...)
			continue
		}
		valid = append(valid, it)
		rowIdx = append(rowIdx, i+1)
	}

	uniqueCategories := uniqueNonEmpty(func(yield func(string)) {
		for _, it := range valid {
			if it.CategoryName != nil && *it.CategoryName != "" {
				yield(*it.CategoryName)
			}
		}
	})
	uniqueTags := uniqueNonEmpty(func(yield func(string)) {
		for _, it := range valid {
			for _, t := range it.TagNames {
				if t != "" {
					yield(t)
				}
			}
		}
	})

	successCount := 0
	createdForThumbs := make([]struct {
		MediaID string
		URL     string
	}, 0)
	createdForPosterMigrate := make([]struct {
		MediaID string
		URL     string
	}, 0)

	err := s.db.Transaction(func(tx *gorm.DB) error {
		catMap, err := upsertCategories(tx, uniqueCategories)
		if err != nil {
			return err
		}
		tagMap, err := upsertTags(tx, uniqueTags)
		if err != nil {
			return err
		}
		for i, it := range valid {
			var categoryID *string
			if it.CategoryName != nil {
				if id, ok := catMap[*it.CategoryName]; ok {
					cid := id
					categoryID = &cid
				}
			}
			sp := fmt.Sprintf("item_%d", i)
			if err := tx.SavePoint(sp).Error; err != nil {
				errs = append(errs, dto.ImportError{Row: rowIdx[i], Field: "general", Message: err.Error()})
				continue
			}
			m := model.Media{
				Title:       it.Title,
				M3u8URL:     it.M3u8URL,
				PosterURL:   it.PosterURL,
				Description: it.Description,
				Year:        it.Year,
				Artist:      it.Artist,
				CategoryID:  categoryID,
				Status:      model.MediaStatusActive,
			}
			if err := tx.Create(&m).Error; err != nil {
				_ = tx.RollbackTo(sp).Error
				errs = append(errs, dto.ImportError{Row: rowIdx[i], Field: "general", Message: err.Error()})
				continue
			}
			if len(it.TagNames) > 0 {
				links := make([]model.MediaTag, 0, len(it.TagNames))
				for _, tname := range it.TagNames {
					if tid, ok := tagMap[tname]; ok {
						links = append(links, model.MediaTag{MediaID: m.ID, TagID: tid})
					}
				}
				if len(links) > 0 {
					if err := tx.Create(&links).Error; err != nil {
						_ = tx.RollbackTo(sp).Error
						errs = append(errs, dto.ImportError{Row: rowIdx[i], Field: "tags", Message: err.Error()})
						continue
					}
				}
			}
			switch {
			case m.PosterURL == nil || *m.PosterURL == "":
				createdForThumbs = append(createdForThumbs, struct {
					MediaID string
					URL     string
				}{MediaID: m.ID, URL: m.M3u8URL})
			case isExternalPosterURL(*m.PosterURL):
				createdForPosterMigrate = append(createdForPosterMigrate, struct {
					MediaID string
					URL     string
				}{MediaID: m.ID, URL: *m.PosterURL})
			}
			successCount++
		}
		return nil
	})

	if err != nil {
		// 事务失败：全量视为失败
		createdForThumbs = createdForThumbs[:0]
		createdForPosterMigrate = createdForPosterMigrate[:0]
		successCount = 0
		errs = append(errs, dto.ImportError{Row: 0, Field: "transaction", Message: err.Error()})
	} else {
		for _, e := range createdForThumbs {
			s.thumb.Enqueue(e.MediaID, e.URL)
		}
		for _, e := range createdForPosterMigrate {
			s.poster.EnqueueMigrate(e.MediaID, e.URL)
		}
	}

	status := importStatus(successCount, len(items))
	errBlob := encodeErrors(errs)
	var fileNamePtr *string
	if fileName != "" {
		fn := fileName
		fileNamePtr = &fn
	}
	logRec := model.ImportLog{
		UserID:       userID,
		Format:       format,
		FileName:     fileNamePtr,
		TotalCount:   len(items),
		SuccessCount: successCount,
		FailedCount:  len(items) - successCount,
		Status:       status,
		Errors:       errBlob,
	}
	_ = s.db.Create(&logRec).Error // 日志写失败不影响主响应

	return dto.ImportResult{
		TotalCount:   len(items),
		SuccessCount: successCount,
		FailedCount:  len(items) - successCount,
		Errors:       errs,
	}, nil
}

// GetLogs 最近 limit 条导入日志，按 createdAt DESC。
func (s *ImportService) GetLogs(limit int) ([]dto.ImportLogResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var rows []model.ImportLog
	if err := s.db.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.ImportLogResponse, 0, len(rows))
	for i := range rows {
		out = append(out, dto.ImportLogResponse{
			ID:           rows[i].ID,
			UserID:       rows[i].UserID,
			Format:       rows[i].Format,
			FileName:     rows[i].FileName,
			TotalCount:   rows[i].TotalCount,
			SuccessCount: rows[i].SuccessCount,
			FailedCount:  rows[i].FailedCount,
			Status:       rows[i].Status,
			Errors:       rows[i].Errors,
			CreatedAt:    util.FormatISO(rows[i].CreatedAt),
		})
	}
	return out, nil
}

// ---- helpers ----

func validateImportItem(row int, it dto.ImportItem) []dto.ImportError {
	errs := make([]dto.ImportError, 0)
	if strings.TrimSpace(it.Title) == "" {
		errs = append(errs, dto.ImportError{Row: row, Field: "title", Message: "标题不能为空"})
	}
	url := strings.TrimSpace(it.M3u8URL)
	if url == "" {
		errs = append(errs, dto.ImportError{Row: row, Field: "m3u8Url", Message: "m3u8Url 不能为空"})
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		errs = append(errs, dto.ImportError{Row: row, Field: "m3u8Url", Message: "m3u8Url 必须是 http(s) 链接"})
	}
	if it.Year != nil && (*it.Year < 1800 || *it.Year > 2100) {
		errs = append(errs, dto.ImportError{Row: row, Field: "year", Message: "year 超出合法区间 1800-2100"})
	}
	return errs
}

func upsertCategories(tx *gorm.DB, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		var c model.Category
		err := tx.Where("name = ?", name).Take(&c).Error
		if err == nil {
			out[name] = c.ID
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		slug := buildSlug(name)
		nc := model.Category{Name: name, Slug: slug}
		if err := tx.Create(&nc).Error; err != nil {
			return nil, err
		}
		out[name] = nc.ID
	}
	return out, nil
}

func upsertTags(tx *gorm.DB, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		var t model.Tag
		err := tx.Where("name = ?", name).Take(&t).Error
		if err == nil {
			out[name] = t.ID
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		nt := model.Tag{Name: name}
		if err := tx.Create(&nt).Error; err != nil {
			return nil, err
		}
		out[name] = nt.ID
	}
	return out, nil
}

// buildSlug 尽量贴近 TS 原版：小写 + 非 alnum 替换为 - + 去首尾 -；对全非 ASCII 名用简单哈希兜底。
func buildSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	// 压缩连续 -
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		var hash int32
		for _, r := range name {
			hash = (hash << 5) - hash + int32(r)
		}
		if hash < 0 {
			hash = -hash
		}
		s = fmt.Sprintf("cat-%x", hash)
	}
	return s
}

func uniqueNonEmpty(iter func(yield func(string))) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	iter(func(s string) {
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	})
	return out
}

func magicMatches(data []byte, prefixes [][]byte) bool {
	for _, p := range prefixes {
		if len(data) >= len(p) && bytesEqualPrefix(data, p) {
			return true
		}
	}
	return false
}

func bytesEqualPrefix(data, prefix []byte) bool {
	for i, b := range prefix {
		if data[i] != b {
			return false
		}
	}
	return true
}

func importStatus(successCount, total int) string {
	switch {
	case successCount == total && total > 0:
		return "SUCCESS"
	case successCount > 0:
		return "PARTIAL"
	default:
		return "FAILED"
	}
}

func encodeErrors(errs []dto.ImportError) *string {
	if len(errs) == 0 {
		return nil
	}
	b, err := json.Marshal(errs)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func extFromName(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		return ""
	}
	return name[idx:]
}

// isExternalPosterURL 判断 posterUrl 是否是需要异步迁移的外部 http(s) 链接。
// 本地 /uploads/ 或其它非 http(s) 值保持原样不入队。
func isExternalPosterURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// TemplateCSV 返回 BOM + 模板文本；handler 负责 response header。
func (s *ImportService) TemplateCSV() string {
	return "\ufefftitle,m3u8Url,posterUrl,description,year,artist,category,tags\n" +
		"\"示例视频\",\"https://example.com/video.m3u8\",\"https://example.com/poster.jpg\",\"视频描述\",2024,\"张三\",\"电影\",\"动作,科幻\"\n"
}

// TemplateJSON 返回模板字符串。
func (s *ImportService) TemplateJSON() string {
	example := []map[string]any{
		{
			"title":        "示例视频",
			"m3u8Url":      "https://example.com/video.m3u8",
			"posterUrl":    "https://example.com/poster.jpg",
			"description":  "视频描述",
			"year":         2024,
			"artist":       "张三",
			"categoryName": "电影",
			"tagNames":     []string{"动作", "科幻"},
		},
	}
	b, _ := json.MarshalIndent(example, "", "  ")
	return string(b)
}
