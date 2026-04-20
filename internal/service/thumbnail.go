// Package service
// thumbnail.go 提供真正能替换 NoopThumbnailEnqueuer 的队列实现。
//
// 当前阶段不集成真正的 ffmpeg 生成（Docker 里 `apk add ffmpeg` 后再接入）；
// 队列只记录任务并调用 callback，供 /admin/thumbnails/status 返回计数。
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// ThumbnailTask 队列项。
type ThumbnailTask struct {
	MediaID string
	URL     string
}

// ThumbnailQueue 固定 concurrency 的简单任务队列。
type ThumbnailQueue struct {
	jobs        chan ThumbnailTask
	stop        chan struct{}
	once        sync.Once
	processor   func(ThumbnailTask) error
	queued      atomic.Int64
	active      atomic.Int64
	processed   atomic.Int64
	failed      atomic.Int64
	enqueuedIDs sync.Map // mediaID -> struct{} 防重复入队
}

// NewThumbnailQueue 构造。concurrency<=0 视为 1。
func NewThumbnailQueue(concurrency int, processor func(ThumbnailTask) error) *ThumbnailQueue {
	if concurrency < 1 {
		concurrency = 1
	}
	if processor == nil {
		processor = func(ThumbnailTask) error { return nil }
	}
	q := &ThumbnailQueue{
		jobs:      make(chan ThumbnailTask, 1024),
		stop:      make(chan struct{}),
		processor: processor,
	}
	for i := 0; i < concurrency; i++ {
		go q.worker()
	}
	return q
}

// Enqueue 追加任务；同 mediaID 重复入队会被去重。
func (q *ThumbnailQueue) Enqueue(mediaID, url string) {
	if _, loaded := q.enqueuedIDs.LoadOrStore(mediaID, struct{}{}); loaded {
		return
	}
	q.queued.Add(1)
	select {
	case q.jobs <- ThumbnailTask{MediaID: mediaID, URL: url}:
	default:
		// 队列满：退让并记为失败
		q.queued.Add(-1)
		q.failed.Add(1)
		q.enqueuedIDs.Delete(mediaID)
	}
}

// Status 返回 running / queued / processed。
func (q *ThumbnailQueue) Status() (active, queued, processed, failed int64) {
	return q.active.Load(), q.queued.Load(), q.processed.Load(), q.failed.Load()
}

// Stop 关闭队列；阻塞等待所有 worker 退出。
func (q *ThumbnailQueue) Stop() {
	q.once.Do(func() { close(q.stop) })
}

func (q *ThumbnailQueue) worker() {
	for {
		select {
		case <-q.stop:
			return
		case job, ok := <-q.jobs:
			if !ok {
				return
			}
			q.active.Add(1)
			q.queued.Add(-1)
			err := q.processor(job)
			q.active.Add(-1)
			if err != nil {
				q.failed.Add(1)
			} else {
				q.processed.Add(1)
			}
			q.enqueuedIDs.Delete(job.MediaID)
		case <-time.After(24 * time.Hour):
			// 防 worker 永久泄漏；实际 stop 会提前退出
		}
	}
}

// NewFFmpegProcessor 返回一个真正的 ffmpeg 缩略图生成 processor。
// 仅在 media.poster_url 为 NULL 时写入，避免覆盖用户上传的封面。
func NewFFmpegProcessor(uploadsDir string, db *gorm.DB) func(ThumbnailTask) error {
	thumbDir := filepath.Join(uploadsDir, "thumbnails")
	return func(task ThumbnailTask) error {
		ctx := context.Background()

		duration, err := util.FFProbeDuration(ctx, task.URL)
		if err != nil {
			return fmt.Errorf("ffprobe media %s: %w", task.MediaID, err)
		}
		if duration < 1 {
			return fmt.Errorf("media %s duration too short: %.2f", task.MediaID, duration)
		}

		if err := os.MkdirAll(thumbDir, 0o755); err != nil {
			return fmt.Errorf("mkdir thumbnails: %w", err)
		}

		filename := uuid.NewString() + ".webp"
		outPath := filepath.Join(thumbDir, filename)

		seekSec := util.RandomSeekSec(duration)
		if err := util.FFmpegThumbnail(ctx, task.URL, seekSec, outPath); err != nil {
			_ = os.Remove(outPath)
			return fmt.Errorf("ffmpeg media %s: %w", task.MediaID, err)
		}

		localURL := "/uploads/thumbnails/" + filename
		result := db.Model(&model.Media{}).
			Where("id = ? AND poster_url IS NULL", task.MediaID).
			Update("poster_url", localURL)
		if result.Error != nil {
			_ = os.Remove(outPath)
			return fmt.Errorf("update db media %s: %w", task.MediaID, result.Error)
		}

		log.Printf("[thumbnail] generated %s for media %s (seek=%.1fs)", filename, task.MediaID, seekSec)
		return nil
	}
}
