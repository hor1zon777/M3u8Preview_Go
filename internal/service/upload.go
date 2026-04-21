// Package service
// upload.go 负责把 multipart 上传的封面文件写入 uploads/ 目录并返回 /uploads/<id>.<ext>。
// 对齐 uploadService.ts：仅允许 jpg/jpeg/png/gif/webp；≤10MB；文件名用 UUID。
package service

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
)

// UploadService 封装封面上传。
type UploadService struct {
	uploadsDir       string
	maxFileSize      int64
	allowedMimeTypes map[string]bool
}

// NewUploadService 构造。
func NewUploadService(uploadsDir string, maxFileSize int64, allowedMimeTypes []string) *UploadService {
	m := make(map[string]bool, len(allowedMimeTypes))
	for _, v := range allowedMimeTypes {
		m[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return &UploadService{
		uploadsDir:       uploadsDir,
		maxFileSize:      maxFileSize,
		allowedMimeTypes: m,
	}
}

// allowedExt 对齐 uploadService.ts 的文件名后缀白名单。
var allowedExt = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
}

// SavePoster 把 file 写入 uploads 目录并返回供 URL 访问的相对路径（/uploads/<id>.<ext>）。
func (s *UploadService) SavePoster(header *multipart.FileHeader) (string, error) {
	if header == nil {
		return "", middleware.NewAppError(http.StatusBadRequest, "No file uploaded")
	}
	if header.Size <= 0 {
		return "", middleware.NewAppError(http.StatusBadRequest, "文件内容为空")
	}
	if header.Size > s.maxFileSize {
		return "", middleware.NewAppError(http.StatusRequestEntityTooLarge, fmt.Sprintf("文件超出最大大小 %d 字节", s.maxFileSize))
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedExt[ext] {
		return "", middleware.NewAppError(http.StatusBadRequest, "Invalid file extension")
	}

	// Content-Type 强制必填：空值之前会跳过 MIME 白名单与 image/* 前缀校验，只剩扩展名做闸。
	// 攻击者可把 HTML/SVG+JS 改名为 .png 上传，此处直接拒绝。
	mime := strings.ToLower(header.Header.Get("Content-Type"))
	if mime == "" {
		return "", middleware.NewAppError(http.StatusBadRequest, "缺少 Content-Type")
	}
	if len(s.allowedMimeTypes) > 0 && !s.allowedMimeTypes[mime] {
		return "", middleware.NewAppError(http.StatusBadRequest, fmt.Sprintf("File type %s is not allowed", mime))
	}
	if !strings.HasPrefix(mime, "image/") {
		return "", middleware.NewAppError(http.StatusBadRequest, "Only image files are allowed")
	}

	if err := os.MkdirAll(s.uploadsDir, 0o755); err != nil {
		return "", middleware.WrapAppError(http.StatusInternalServerError, "创建 uploads 目录失败", err)
	}

	id := uuid.NewString()
	filename := id + ext
	dst := filepath.Join(s.uploadsDir, filename)

	src, err := header.Open()
	if err != nil {
		return "", middleware.WrapAppError(http.StatusInternalServerError, "打开上传文件失败", err)
	}
	defer func() { _ = src.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", middleware.WrapAppError(http.StatusInternalServerError, "写入上传文件失败", err)
	}
	defer func() { _ = out.Close() }()

	// 限速拷贝：再次校验 body 不超过 maxFileSize（防 header 被伪造）
	if _, err := io.Copy(out, io.LimitReader(src, s.maxFileSize+1)); err != nil {
		_ = os.Remove(dst)
		return "", middleware.WrapAppError(http.StatusInternalServerError, "写入上传文件失败", err)
	}
	st, err := out.Stat()
	if err == nil && st.Size() > s.maxFileSize {
		_ = os.Remove(dst)
		return "", middleware.NewAppError(http.StatusRequestEntityTooLarge, "上传文件超出大小限制")
	}

	return "/uploads/" + filename, nil
}
