// Package service
// poster.go 实现外部封面下载到本地。
// 对齐 posterDownloadService.ts：SSRF 防护 + 5MB 上限 + 扩展名白名单 + 特殊 Referer。
package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

const (
	posterMaxBytes   int64 = 5 * 1024 * 1024
	posterTimeoutMS        = 15 * time.Second
	posterUA               = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var posterAllowedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

// PosterDownloader 负责 URL → 本地文件；作为 MediaService.PosterResolver 使用。
type PosterDownloader struct {
	postersDir   string
	bucket       *util.TokenBucket
	onDownloaded func(mediaID, localPath string) // 异步下载完成后的回调（更新 DB）

	// 队列 + 统计
	jobs      chan posterJob
	stop      chan struct{}
	once      sync.Once
	active    atomic.Int64
	queued    atomic.Int64
	processed atomic.Int64
	failed    atomic.Int64
}

type posterJob struct {
	MediaID string
	URL     string
}

// NewPosterDownloader 构造；postersDir 不存在时会按需创建。
// onDownloaded 在异步 worker 成功下载后被调用，用于更新 DB。
// 速率：100 req/min ≈ 1.67 req/s。
func NewPosterDownloader(uploadsDir string, concurrency int, onDownloaded func(mediaID, localPath string)) *PosterDownloader {
	if concurrency < 1 {
		concurrency = 1
	}
	d := &PosterDownloader{
		postersDir:   filepath.Join(uploadsDir, "posters"),
		bucket:       util.NewTokenBucket(100, 100.0/60.0),
		onDownloaded: onDownloaded,
		jobs:         make(chan posterJob, 1024),
		stop:         make(chan struct{}),
	}
	for i := 0; i < concurrency; i++ {
		go d.worker()
	}
	return d
}

// Stop 关闭队列。
func (d *PosterDownloader) Stop() {
	d.once.Do(func() { close(d.stop) })
}

// Resolve 作为 MediaService.PosterResolver：输入 nil/本地路径直接返回；外部 URL 则下载到本地。
// 返回值是新的本地 URL（/uploads/posters/xxx.ext）或原值（下载失败）。
func (d *PosterDownloader) Resolve(raw *string) (*string, error) {
	if raw == nil || *raw == "" {
		return raw, nil
	}
	if !strings.HasPrefix(*raw, "http://") && !strings.HasPrefix(*raw, "https://") {
		return raw, nil
	}
	local, err := d.downloadOnce(*raw)
	if err != nil {
		return raw, err
	}
	return &local, nil
}

// EnqueueMigrate 异步迁移一张外部封面。
func (d *PosterDownloader) EnqueueMigrate(mediaID, rawURL string) {
	d.queued.Add(1)
	select {
	case d.jobs <- posterJob{MediaID: mediaID, URL: rawURL}:
	default:
		d.queued.Add(-1)
		d.failed.Add(1)
	}
}

// Status 返回当前队列状态。
func (d *PosterDownloader) Status() (active, queued, processed, failed int64) {
	return d.active.Load(), d.queued.Load(), d.processed.Load(), d.failed.Load()
}

// downloadOnce 执行一次下载，返回本地路径（如 /uploads/posters/xxx.jpg）。
func (d *PosterDownloader) downloadOnce(raw string) (string, error) {
	if err := d.bucket.Wait(context.Background()); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), posterTimeoutMS)
	defer cancel()

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	headers := http.Header{}
	headers.Set("User-Agent", posterUA)
	headers.Set("Accept", "image/*,*/*;q=0.8")
	if ref := refererForHost(u.Hostname()); ref != "" {
		headers.Set("Referer", ref)
	}

	resp, err := util.SafeFetch(ctx, raw, util.SafeFetchOptions{
		MaxRedirects: 3,
		Headers:      headers,
		Method:       http.MethodGet,
		Timeout:      posterTimeoutMS,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &httpStatusError{Code: resp.StatusCode}
	}

	ext := guessExt(u, resp.Header.Get("Content-Type"))
	if ext == "" || !posterAllowedExt[ext] {
		return "", &unsupportedFormatError{Ext: ext}
	}

	if err := os.MkdirAll(d.postersDir, 0o755); err != nil {
		return "", err
	}
	filename := uuid.NewString() + ext
	dst := filepath.Join(d.postersDir, filename)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Close() }()

	// 限流到 5MB
	if _, err := io.Copy(out, io.LimitReader(resp.Body, posterMaxBytes+1)); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	st, err := out.Stat()
	if err == nil && st.Size() > posterMaxBytes {
		_ = os.Remove(dst)
		return "", &fileTooLargeError{Size: st.Size(), Max: posterMaxBytes}
	}
	return "/uploads/posters/" + filename, nil
}

func (d *PosterDownloader) worker() {
	for {
		select {
		case <-d.stop:
			return
		case job, ok := <-d.jobs:
			if !ok {
				return
			}
			d.active.Add(1)
			d.queued.Add(-1)
			localPath, err := d.downloadOnce(job.URL)
			d.active.Add(-1)
			if err != nil {
				d.failed.Add(1)
			} else {
				if d.onDownloaded != nil {
					d.onDownloaded(job.MediaID, localPath)
				}
				d.processed.Add(1)
			}
		}
	}
}

// ---- helpers ----

func refererForHost(host string) string {
	h := strings.ToLower(host)
	if h == "fourhoi.com" || strings.HasSuffix(h, ".fourhoi.com") ||
		h == "surrit.com" || strings.HasSuffix(h, ".surrit.com") {
		return "https://missav.ws"
	}
	return ""
}

func guessExt(u *url.URL, contentType string) string {
	// 先从路径取
	if u != nil {
		last := u.Path
		if idx := strings.LastIndex(last, "/"); idx >= 0 {
			last = last[idx+1:]
		}
		if qs := strings.Index(last, "?"); qs >= 0 {
			last = last[:qs]
		}
		if dot := strings.LastIndex(last, "."); dot >= 0 {
			ext := strings.ToLower(last[dot:])
			if posterAllowedExt[ext] {
				return ext
			}
		}
	}
	// 回退用 Content-Type
	switch strings.ToLower(strings.SplitN(contentType, ";", 2)[0]) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ""
}

// 特定错误类型：供上层把状态码映射到 AppError。
type httpStatusError struct{ Code int }

func (e *httpStatusError) Error() string { return "上游状态码异常" }

type unsupportedFormatError struct{ Ext string }

func (e *unsupportedFormatError) Error() string { return "不支持的封面格式" }

type fileTooLargeError struct {
	Size int64
	Max  int64
}

func (e *fileTooLargeError) Error() string { return "封面文件过大" }
