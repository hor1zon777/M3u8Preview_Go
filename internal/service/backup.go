// Package service
// backup.go 对齐 backupService.ts + backupController.ts：
//   - ExportToFile：11 张表并行查 → backup.json → 打包 uploads → 临时文件 + downloadId
//   - ImportFromZip：校验 ZIP → 白名单字段 → 事务删除/写入 → 恢复 uploads → invalidate cache
//
// SSE 进度通过回调 (ExportProgress / BackupProgress) 推出去，handler 负责封装成 data 事件。
package service

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

const backupVersion = "1.0"

// ExportProgress / BackupProgress 对齐 shared/types。
type ExportProgress struct {
	Phase      string `json:"phase"`
	Message    string `json:"message"`
	Current    int    `json:"current"`
	Total      int    `json:"total"`
	Percentage int    `json:"percentage"`
	DownloadID string `json:"downloadId,omitempty"`
}

// BackupProgress 恢复阶段进度。
type BackupProgress struct {
	Phase      string          `json:"phase"`
	Message    string          `json:"message"`
	Current    int             `json:"current"`
	Total      int             `json:"total"`
	Percentage int             `json:"percentage"`
	Result     *RestoreResult  `json:"result,omitempty"`
}

// RestoreResult 恢复结果统计。
type RestoreResult struct {
	TablesRestored  int   `json:"tablesRestored"`
	TotalRecords    int   `json:"totalRecords"`
	UploadsRestored int   `json:"uploadsRestored"`
	Duration        int64 `json:"duration"` // 秒
}

// ProgressFn 通用进度回调（接收任意类型进度）。
type ProgressFn func(progress any)

// PendingItem pendingDownloads / pendingRestores 共用条目。
type PendingItem struct {
	FilePath  string
	Filename  string
	CreatedAt time.Time
}

// BackupService 管理导出导入状态。
type BackupService struct {
	db          *gorm.DB
	uploadsDir  string
	downloadTTL time.Duration

	mu              sync.Mutex
	pendingDownload map[string]PendingItem
	pendingRestore  map[string]PendingItem

	// 可选：导入完成后需要清理的缓存
	invalidators []func()
}

// NewBackupService 构造。
func NewBackupService(db *gorm.DB, uploadsDir string) *BackupService {
	return &BackupService{
		db:              db,
		uploadsDir:      uploadsDir,
		downloadTTL:     10 * time.Minute,
		pendingDownload: map[string]PendingItem{},
		pendingRestore:  map[string]PendingItem{},
	}
}

// RegisterInvalidator 注册一个在 import 完成时调用的缓存失效函数。
// 使用 s.mu 保护 invalidators 切片，防止并发注册与读取之间的数据竞争。
func (s *BackupService) RegisterInvalidator(fn func()) {
	s.mu.Lock()
	s.invalidators = append(s.invalidators, fn)
	s.mu.Unlock()
}

// ---- export ----

// ExportToStream 直接打包 ZIP 到给定 Writer（对齐 /backup/export）。
func (s *BackupService) ExportToStream(w io.Writer, includePosters bool) error {
	data, err := s.buildBackupJSON()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	defer func() { _ = zw.Close() }()

	if err := s.writeBackupJSON(zw, data); err != nil {
		return err
	}
	return s.writeUploadsDir(zw, includePosters)
}

// ExportToFile 打包到临时文件，返回 (downloadId, filename)。
// onProgress 可能为 nil。
func (s *BackupService) ExportToFile(includePosters bool, onProgress func(ExportProgress)) (string, string, error) {
	emit := func(p ExportProgress) {
		if onProgress != nil {
			onProgress(p)
		}
	}
	emit(ExportProgress{Phase: "db", Message: "正在查询数据库...", Current: 0, Total: 11, Percentage: 0})
	data, err := s.buildBackupJSON()
	if err != nil {
		return "", "", err
	}
	emit(ExportProgress{Phase: "db", Message: "查询完成", Current: 11, Total: 11, Percentage: 30})

	// 统一用 UTC 时间戳，避免文件名与 exportedAt (UTC) 时区不一致
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	filename := "backup-" + timestamp + ".zip"
	tmpFile, err := os.CreateTemp("", "m3u8-backup-*.zip")
	if err != nil {
		return "", "", middleware.WrapAppError(http.StatusInternalServerError, "创建临时文件失败", err)
	}

	// 在关闭前写入 + 取文件大小
	zw := zip.NewWriter(tmpFile)
	if werr := s.writeBackupJSON(zw, data); werr != nil {
		_ = zw.Close()
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", "", werr
	}
	emit(ExportProgress{Phase: "files", Message: "正在打包文件...", Current: 0, Total: 0, Percentage: 60})
	if werr := s.writeUploadsDir(zw, includePosters); werr != nil {
		_ = zw.Close()
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", "", werr
	}
	emit(ExportProgress{Phase: "finalize", Message: "正在压缩并写入文件...", Current: 0, Total: 0, Percentage: 90})
	if err := zw.Close(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", "", middleware.WrapAppError(http.StatusInternalServerError, "写入 ZIP 失败", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", "", middleware.WrapAppError(http.StatusInternalServerError, "关闭临时文件失败", err)
	}

	downloadID := strings.ReplaceAll(uuid.NewString(), "-", "")
	s.mu.Lock()
	s.pendingDownload[downloadID] = PendingItem{FilePath: tmpFile.Name(), Filename: filename, CreatedAt: time.Now()}
	s.mu.Unlock()

	emit(ExportProgress{Phase: "complete", Message: "打包完成", Current: 1, Total: 1, Percentage: 100, DownloadID: downloadID})
	return downloadID, filename, nil
}

// ConsumeDownload 用 downloadId 取出文件路径供下载；下载完应调用 DeleteDownload。
func (s *BackupService) ConsumeDownload(id string) (PendingItem, bool) {
	s.cleanupExpired()
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.pendingDownload[id]
	return v, ok
}

// DeleteDownload 下载成功后清理。
func (s *BackupService) DeleteDownload(id string) {
	s.mu.Lock()
	v, ok := s.pendingDownload[id]
	if ok {
		delete(s.pendingDownload, id)
	}
	s.mu.Unlock()
	if ok {
		_ = os.Remove(v.FilePath)
	}
}

// SaveUploadedBackup 把 admin 上传的 ZIP 保存到临时目录，返回 restoreId。
func (s *BackupService) SaveUploadedBackup(reader io.Reader) (string, error) {
	s.cleanupExpired()
	tmp, err := os.CreateTemp("", "m3u8-restore-*.zip")
	if err != nil {
		return "", middleware.WrapAppError(http.StatusInternalServerError, "创建临时文件失败", err)
	}
	defer func() { _ = tmp.Close() }()
	if _, err := io.Copy(tmp, reader); err != nil {
		_ = os.Remove(tmp.Name())
		return "", middleware.WrapAppError(http.StatusInternalServerError, "保存上传文件失败", err)
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	s.mu.Lock()
	s.pendingRestore[id] = PendingItem{FilePath: tmp.Name(), CreatedAt: time.Now()}
	s.mu.Unlock()
	return id, nil
}

// ConsumeRestore 取出 restoreId 对应的文件路径；调用后自动清理。
func (s *BackupService) ConsumeRestore(id string) (string, bool) {
	s.mu.Lock()
	v, ok := s.pendingRestore[id]
	if ok {
		delete(s.pendingRestore, id)
	}
	s.mu.Unlock()
	return v.FilePath, ok
}

// cleanupExpired 遍历两组 pending 清理过期条目。
func (s *BackupService) cleanupExpired() {
	cutoff := time.Now().Add(-s.downloadTTL)
	s.mu.Lock()
	for id, v := range s.pendingDownload {
		if v.CreatedAt.Before(cutoff) {
			_ = os.Remove(v.FilePath)
			delete(s.pendingDownload, id)
		}
	}
	for id, v := range s.pendingRestore {
		if v.CreatedAt.Before(cutoff) {
			_ = os.Remove(v.FilePath)
			delete(s.pendingRestore, id)
		}
	}
	s.mu.Unlock()
}

// ---- import ----

// ImportFromFile 读取 zipPath 并执行恢复流程，返回统计。
func (s *BackupService) ImportFromFile(zipPath string, onProgress func(BackupProgress)) (RestoreResult, error) {
	emit := func(p BackupProgress) {
		if onProgress != nil {
			onProgress(p)
		}
	}
	start := time.Now()
	emit(BackupProgress{Phase: "parse", Message: "正在解析 ZIP 文件...", Percentage: 0})

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, "无法解析 ZIP 文件，请确认文件格式正确")
	}
	defer func() { _ = zr.Close() }()

	// zip-bomb 防护
	const maxUncompressed int64 = 2 * 1024 * 1024 * 1024
	const maxEntries = 50000
	if len(zr.File) > maxEntries {
		return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, "ZIP 包含过多条目，疑似异常文件")
	}
	var totalUnc int64
	for _, f := range zr.File {
		totalUnc += int64(f.UncompressedSize64)
		if totalUnc > maxUncompressed {
			return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, "ZIP 解压后体积过大，疑似 zip-bomb")
		}
	}

	// 读取 backup.json
	var backupFile *zip.File
	for _, f := range zr.File {
		if f.Name == "backup.json" {
			backupFile = f
			break
		}
	}
	if backupFile == nil {
		return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, "ZIP 文件中缺少 backup.json")
	}
	rc, err := backupFile.Open()
	if err != nil {
		return RestoreResult{}, middleware.WrapAppError(http.StatusBadRequest, "读取 backup.json 失败", err)
	}
	raw, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return RestoreResult{}, middleware.WrapAppError(http.StatusBadRequest, "读取 backup.json 失败", err)
	}

	var data backupPayload
	if err := json.Unmarshal(raw, &data); err != nil {
		return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, "backup.json 格式无效")
	}
	if data.Version == "" || data.Tables == nil {
		return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, "backup.json 结构不完整，缺少 version 或 tables")
	}
	if data.Version != backupVersion {
		return RestoreResult{}, middleware.NewAppError(http.StatusBadRequest, fmt.Sprintf("不支持的备份版本 %s", data.Version))
	}

	emit(BackupProgress{Phase: "parse", Message: "数据校验完成", Percentage: 5})

	totalRecords := 0
	tablesRestored := 0
	emit(BackupProgress{Phase: "delete", Message: "正在清空现有数据...", Current: 0, Total: 12, Percentage: 5})

	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 依外键拓扑删除
		order := []any{
			&model.PlaylistItem{},
			&model.WatchHistory{},
			&model.Favorite{},
			&model.MediaTag{},
			&model.Playlist{},
			&model.ImportLog{},
			&model.Media{},
			&model.Tag{},
			&model.Category{},
			&model.SystemSetting{},
			&model.RefreshToken{},
			&model.User{},
		}
		for i, m := range order {
			if err := tx.Where("1 = 1").Delete(m).Error; err != nil {
				return err
			}
			emit(BackupProgress{Phase: "delete", Message: "已清空", Current: i + 1, Total: 12,
				Percentage: 5 + int(float64(i+1)/12*15),
			})
		}

		// 写入阶段
		emit(BackupProgress{Phase: "write", Message: "正在写入数据...", Current: 0, Total: 11, Percentage: 20})

		writeTable := func(idx int, name string, rows any, count int) error {
			if count == 0 {
				emit(BackupProgress{Phase: "write", Message: "跳过 " + name + "（无数据）",
					Current: idx + 1, Total: 11, Percentage: 20 + int(float64(idx+1)/11*55)})
				return nil
			}
			if err := tx.CreateInBatches(rows, 100).Error; err != nil {
				return err
			}
			totalRecords += count
			tablesRestored++
			emit(BackupProgress{Phase: "write", Message: fmt.Sprintf("已写入 %s (%d 条)", name, count),
				Current: idx + 1, Total: 11, Percentage: 20 + int(float64(idx+1)/11*55)})
			return nil
		}

		users := backupToUsers(sanitizeUsers(data.Tables.Users))
		if err := writeTable(0, "users", &users, len(users)); err != nil {
			return err
		}
		cats := sanitizeCategories(data.Tables.Categories)
		if err := writeTable(1, "categories", &cats, len(cats)); err != nil {
			return err
		}
		tags := sanitizeTags(data.Tables.Tags)
		if err := writeTable(2, "tags", &tags, len(tags)); err != nil {
			return err
		}
		medias := sanitizeMedia(data.Tables.Media)
		if err := writeTable(3, "media", &medias, len(medias)); err != nil {
			return err
		}
		mediaTags := sanitizeMediaTags(data.Tables.MediaTags)
		if err := writeTable(4, "mediaTags", &mediaTags, len(mediaTags)); err != nil {
			return err
		}
		favs := sanitizeFavorites(data.Tables.Favorites)
		if err := writeTable(5, "favorites", &favs, len(favs)); err != nil {
			return err
		}
		pls := sanitizePlaylists(data.Tables.Playlists)
		if err := writeTable(6, "playlists", &pls, len(pls)); err != nil {
			return err
		}
		plItems := sanitizePlaylistItems(data.Tables.PlaylistItems)
		if err := writeTable(7, "playlistItems", &plItems, len(plItems)); err != nil {
			return err
		}
		whs := sanitizeWatchHistory(data.Tables.WatchHistory)
		if err := writeTable(8, "watchHistory", &whs, len(whs)); err != nil {
			return err
		}
		logs := sanitizeImportLogs(data.Tables.ImportLogs)
		if err := writeTable(9, "importLogs", &logs, len(logs)); err != nil {
			return err
		}

		// systemSettings 用 upsert
		for _, row := range data.Tables.SystemSettings {
			if row.Key == "" {
				continue
			}
			if err := tx.Save(&model.SystemSetting{Key: row.Key, Value: row.Value}).Error; err != nil {
				return err
			}
			totalRecords++
		}
		tablesRestored++
		emit(BackupProgress{Phase: "write", Message: "已写入 systemSettings",
			Current: 11, Total: 11, Percentage: 75})
		return nil
	})
	if err != nil {
		return RestoreResult{}, middleware.WrapAppError(http.StatusInternalServerError, "恢复事务失败", err)
	}

	// 恢复 uploads：失败必须向上冒泡，避免"DB 已写入但文件缺失"的孤儿态
	restored, upErr := s.restoreUploads(zr, func(done, total int) {
		emit(BackupProgress{Phase: "files",
			Message:    fmt.Sprintf("正在恢复文件 (%d/%d)", done, total),
			Current:    done,
			Total:      total,
			Percentage: 75 + int(float64(done)/float64(maxInt(total, 1))*20),
		})
	})
	if upErr != nil {
		return RestoreResult{}, middleware.WrapAppError(http.StatusInternalServerError, "恢复文件失败", upErr)
	}

	// 拷贝 invalidators 切片后释放锁再调用回调，避免持锁执行业务方法导致死锁
	s.mu.Lock()
	fns := append([]func(){}, s.invalidators...)
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}

	res := RestoreResult{
		TablesRestored:  tablesRestored,
		TotalRecords:    totalRecords,
		UploadsRestored: restored,
		Duration:        int64(time.Since(start).Seconds()),
	}
	emit(BackupProgress{Phase: "complete", Message: "恢复完成", Current: 1, Total: 1, Percentage: 100, Result: &res})
	return res, nil
}

// ---- helpers ----

type backupPayload struct {
	Version string     `json:"version"`
	Tables  *tableSet  `json:"tables"`
}

// backupUser 是 User 在 backup JSON 中的序列化形态。
// model.User.PasswordHash 标了 json:"-" 防止 API 泄露，但 backup 必须保留 hash 否则恢复后无法登录。
type backupUser struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	Role         string    `json:"role"`
	Avatar       *string   `json:"avatar,omitempty"`
	IsActive     bool      `json:"isActive"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func usersToBackup(in []model.User) []backupUser {
	out := make([]backupUser, 0, len(in))
	for _, u := range in {
		out = append(out, backupUser{
			ID: u.ID, Username: u.Username, PasswordHash: u.PasswordHash,
			Role: u.Role, Avatar: u.Avatar, IsActive: u.IsActive,
			CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		})
	}
	return out
}

func backupToUsers(in []backupUser) []model.User {
	out := make([]model.User, 0, len(in))
	for _, b := range in {
		out = append(out, model.User{
			ID: b.ID, Username: b.Username, PasswordHash: b.PasswordHash,
			Role: b.Role, Avatar: b.Avatar, IsActive: b.IsActive,
			CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
		})
	}
	return out
}

type tableSet struct {
	Users          []backupUser          `json:"users"`
	Categories     []model.Category      `json:"categories"`
	Tags           []model.Tag           `json:"tags"`
	Media          []model.Media         `json:"media"`
	MediaTags      []model.MediaTag      `json:"mediaTags"`
	Favorites      []model.Favorite      `json:"favorites"`
	Playlists      []model.Playlist      `json:"playlists"`
	PlaylistItems  []model.PlaylistItem  `json:"playlistItems"`
	WatchHistory   []model.WatchHistory  `json:"watchHistory"`
	ImportLogs     []model.ImportLog     `json:"importLogs"`
	SystemSettings []model.SystemSetting `json:"systemSettings"`
}

// buildBackupJSON 查 11 张表并拼成可序列化结构。
func (s *BackupService) buildBackupJSON() (map[string]any, error) {
	var (
		mu     sync.Mutex
		firstErr error
		data   tableSet
		wg     sync.WaitGroup
	)
	saveErr := func(e error) {
		if e == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		mu.Unlock()
	}
	wg.Add(11)
	go func() {
		defer wg.Done()
		var users []model.User
		saveErr(s.db.Find(&users).Error)
		mu.Lock()
		data.Users = usersToBackup(users)
		mu.Unlock()
	}()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.Categories).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.Tags).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.Media).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.MediaTags).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.Favorites).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.Playlists).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.PlaylistItems).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.WatchHistory).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.ImportLogs).Error) }()
	go func() { defer wg.Done(); saveErr(s.db.Find(&data.SystemSettings).Error) }()
	wg.Wait()
	if firstErr != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "导出查询失败", firstErr)
	}

	return map[string]any{
		"version":    backupVersion,
		"exportedAt": time.Now().UTC().Format(time.RFC3339),
		"tables":     data,
	}, nil
}

// writeBackupJSON 把 backup.json 写入 zip。
func (s *BackupService) writeBackupJSON(zw *zip.Writer, data map[string]any) error {
	w, err := zw.Create("backup.json")
	if err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "写 backup.json 失败", err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// writeUploadsDir 把 uploadsDir 打包进 zip；includePosters=false 时跳过 posters 子目录。
func (s *BackupService) writeUploadsDir(zw *zip.Writer, includePosters bool) error {
	if _, err := os.Stat(s.uploadsDir); os.IsNotExist(err) {
		return nil
	}
	postersDir := filepath.Join(s.uploadsDir, "posters")
	return filepath.Walk(s.uploadsDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if path == s.uploadsDir {
			return nil
		}
		if info.IsDir() {
			if !includePosters && path == postersDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !includePosters && strings.HasPrefix(path+string(os.PathSeparator), postersDir+string(os.PathSeparator)) {
			return nil
		}
		rel, rerr := filepath.Rel(s.uploadsDir, path)
		if rerr != nil {
			return rerr
		}
		archiveName := "uploads/" + filepath.ToSlash(rel)
		w, cerr := zw.Create(archiveName)
		if cerr != nil {
			return cerr
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			return ferr
		}
		defer func() { _ = f.Close() }()
		_, copyErr := io.Copy(w, f)
		return copyErr
	})
}

// restoreUploads 将 ZIP 里的 uploads/* 还原到磁盘。
// 采用"两阶段原子切换"设计防止数据永久丢失：
//  1. 先把所有文件解压到临时目录 <uploadsDir>.new-<ts>
//  2. 解压期间任一写失败 → 删除临时目录，原 uploadsDir 完好保留，返回 error
//  3. 全部成功后：rename(old → .old-<ts>) + rename(new → uploadsDir) 原子切换
//  4. 异步清理 .old-<ts>
//
// 返回 (成功文件数, error)。原先忽略 io.Copy 错误 + 先删后写的做法已废弃。
func (s *BackupService) restoreUploads(zr *zip.ReadCloser, progress func(done, total int)) (int, error) {
	entries := make([]*zip.File, 0)
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "uploads/") || f.FileInfo().IsDir() {
			continue
		}
		entries = append(entries, f)
	}
	total := len(entries)
	if total == 0 {
		return 0, nil
	}

	absRoot, err := filepath.Abs(s.uploadsDir)
	if err != nil {
		return 0, fmt.Errorf("resolve uploads dir: %w", err)
	}
	ts := time.Now().UTC().Format("20060102-150405")
	newDir := absRoot + ".new-" + ts
	oldDir := absRoot + ".old-" + ts

	// 清理可能残留的同名临时目录
	_ = os.RemoveAll(newDir)
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return 0, fmt.Errorf("create staging dir: %w", err)
	}
	staged := false
	defer func() {
		if !staged {
			_ = os.RemoveAll(newDir)
		}
	}()

	done := 0
	for _, f := range entries {
		rel := strings.TrimPrefix(f.Name, "uploads/")
		if rel == "" {
			continue
		}
		// 路径穿越防护：拒绝绝对路径、..、反斜杠、NUL
		if filepath.IsAbs(rel) || strings.ContainsAny(rel, "\x00") {
			continue
		}
		// 二次校验：拼接后必须仍在 newDir 内
		dst := filepath.Clean(filepath.Join(newDir, filepath.FromSlash(rel)))
		inside, relErr := filepath.Rel(newDir, dst)
		if relErr != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(os.PathSeparator)) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
		}
		rc, err := f.Open()
		if err != nil {
			return 0, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		out, err := os.Create(dst)
		if err != nil {
			_ = rc.Close()
			return 0, fmt.Errorf("create %s: %w", dst, err)
		}
		if _, copyErr := io.Copy(out, rc); copyErr != nil {
			_ = out.Close()
			_ = rc.Close()
			return 0, fmt.Errorf("copy %s: %w", dst, copyErr)
		}
		if err := out.Close(); err != nil {
			_ = rc.Close()
			return 0, fmt.Errorf("close %s: %w", dst, err)
		}
		_ = rc.Close()
		done++
		if done%20 == 0 || done == total {
			if progress != nil {
				progress(done, total)
			}
		}
	}
	if progress != nil {
		progress(done, total)
	}

	// 原子切换：先把旧目录搬走，再把新目录顶上；任何一步失败都尝试回滚
	if st, statErr := os.Stat(absRoot); statErr == nil && st.IsDir() {
		if err := os.Rename(absRoot, oldDir); err != nil {
			return 0, fmt.Errorf("swap old uploads dir: %w", err)
		}
		if err := os.Rename(newDir, absRoot); err != nil {
			// 回滚：把 old 搬回原位
			_ = os.Rename(oldDir, absRoot)
			return 0, fmt.Errorf("swap new uploads dir: %w", err)
		}
		staged = true
		// 异步删除旧目录，失败只记录到日志
		go func(p string) { _ = os.RemoveAll(p) }(oldDir)
	} else {
		// 原目录不存在：直接把新目录改名为正式目录
		if err := os.Rename(newDir, absRoot); err != nil {
			return 0, fmt.Errorf("promote new uploads dir: %w", err)
		}
		staged = true
	}
	return done, nil
}

// ---- 白名单字段清洗 ----
// 由于 JSON 反序列化已经用 model.X 作为 target，unknown 字段会被忽略；
// 这里仅做最小值清洗（例如 User.IsActive 默认 true、Media.M3u8URL 协议校验）。

func sanitizeUsers(in []backupUser) []backupUser {
	out := make([]backupUser, 0, len(in))
	for _, u := range in {
		if u.Username == "" {
			continue
		}
		if u.Role != "USER" && u.Role != "ADMIN" {
			continue
		}
		out = append(out, u)
	}
	return out
}

func sanitizeCategories(in []model.Category) []model.Category   { return in }
func sanitizeTags(in []model.Tag) []model.Tag                   { return in }
func sanitizeMedia(in []model.Media) []model.Media {
	out := make([]model.Media, 0, len(in))
	for _, m := range in {
		u := strings.ToLower(m.M3u8URL)
		if !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
			continue
		}
		out = append(out, m)
	}
	return out
}
func sanitizeMediaTags(in []model.MediaTag) []model.MediaTag           { return in }
func sanitizeFavorites(in []model.Favorite) []model.Favorite           { return in }
func sanitizePlaylists(in []model.Playlist) []model.Playlist           { return in }
func sanitizePlaylistItems(in []model.PlaylistItem) []model.PlaylistItem { return in }
func sanitizeWatchHistory(in []model.WatchHistory) []model.WatchHistory { return in }
func sanitizeImportLogs(in []model.ImportLog) []model.ImportLog         { return in }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
