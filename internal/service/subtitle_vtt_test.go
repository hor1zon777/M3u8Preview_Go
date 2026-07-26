package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

const sampleVTT = "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\n你好\nこんにちは\n\n"

// markJobDone 把 seedPendingJob 建出来的任务推到 DONE + 有 vtt_path 的状态。
func markJobDone(t *testing.T, s *SubtitleService, mediaID string) *model.SubtitleJob {
	t.Helper()
	if err := s.db.Model(&model.SubtitleJob{}).Where("media_id = ?", mediaID).Updates(map[string]any{
		"status":   model.SubtitleStatusDone,
		"stage":    model.SubtitleStageDone,
		"vtt_path": mediaID + ".vtt",
	}).Error; err != nil {
		t.Fatalf("mark done: %v", err)
	}
	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	return &job
}

func TestStoreAndLoadVTT(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	seedPendingJob(t, svc.db, "m1", "https://example.com/a.m3u8")
	job := markJobDone(t, svc, "m1")

	if err := svc.storeVTT("m1", []byte(sampleVTT)); err != nil {
		t.Fatalf("storeVTT: %v", err)
	}
	got, err := svc.loadVTT(job)
	if err != nil {
		t.Fatalf("loadVTT: %v", err)
	}
	if string(got) != sampleVTT {
		t.Fatalf("正文往返不一致:\n%q", got)
	}

	// 重复写入应当是更新而非插入第二行（media_id 是主键，插入会直接报错）。
	if err := svc.storeVTT("m1", []byte("WEBVTT\n\n改过了\n")); err != nil {
		t.Fatalf("storeVTT 覆盖: %v", err)
	}
	var count int64
	svc.db.Model(&model.SubtitleVTT{}).Where("media_id = ?", "m1").Count(&count)
	if count != 1 {
		t.Fatalf("期望 1 行正文，实际 %d 行", count)
	}
}

// TestLoadVTTFallsBackToDisk 覆盖迁移期的关键路径：
// 历史任务的正文只在磁盘上，尚未回填时读取必须仍然能成功，否则升级即字幕全挂。
func TestLoadVTTFallsBackToDisk(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	seedPendingJob(t, svc.db, "m2", "https://example.com/b.m3u8")
	job := markJobDone(t, svc, "m2")

	abs := filepath.Join(svc.snap().SubtitlesDir, job.VttPath)
	if err := os.WriteFile(abs, []byte(sampleVTT), 0o644); err != nil {
		t.Fatalf("写磁盘 VTT: %v", err)
	}

	got, err := svc.loadVTT(job)
	if err != nil {
		t.Fatalf("loadVTT 未能回退到磁盘: %v", err)
	}
	if string(got) != sampleVTT {
		t.Fatalf("磁盘回退内容不一致:\n%q", got)
	}
}

func TestLoadVTTMissingEverywhere(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	seedPendingJob(t, svc.db, "m3", "https://example.com/c.m3u8")
	job := markJobDone(t, svc, "m3")

	if _, err := svc.loadVTT(job); err == nil {
		t.Fatal("库与磁盘都没有正文时应当报错，而不是返回空内容")
	}
}

func TestBackfillVTT(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	dir := svc.snap().SubtitlesDir

	// m4 有磁盘文件待回填；m5 的文件已丢失，回填时应跳过而不是中断整轮。
	for _, id := range []string{"m4", "m5"} {
		seedPendingJob(t, svc.db, id, "https://example.com/"+id+".m3u8")
		markJobDone(t, svc, id)
	}
	if err := os.WriteFile(filepath.Join(dir, "m4.vtt"), []byte(sampleVTT), 0o644); err != nil {
		t.Fatalf("写磁盘 VTT: %v", err)
	}

	svc.backfillVTT()

	var row model.SubtitleVTT
	if err := svc.db.Where("media_id = ?", "m4").Take(&row).Error; err != nil {
		t.Fatalf("m4 未被回填: %v", err)
	}
	if row.Content != sampleVTT {
		t.Fatalf("回填内容不一致:\n%q", row.Content)
	}

	var missing int64
	svc.db.Model(&model.SubtitleVTT{}).Where("media_id = ?", "m5").Count(&missing)
	if missing != 0 {
		t.Fatal("磁盘文件缺失的任务不应产生空正文行")
	}

	// 幂等：再跑一次不应改变任何东西。
	svc.backfillVTT()
	var total int64
	svc.db.Model(&model.SubtitleVTT{}).Count(&total)
	if total != 1 {
		t.Fatalf("回填不幂等，期望 1 行，实际 %d 行", total)
	}
}

// TestBackfillVTTSkippedOnReplica 守住"只读副本不得执行写操作"这条线：
// replica 的库是只读的，硬跑回填只会刷满错误日志。
func TestBackfillVTTSkippedOnReplica(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	seedPendingJob(t, svc.db, "m6", "https://example.com/d.m3u8")
	markJobDone(t, svc, "m6")
	if err := os.WriteFile(filepath.Join(svc.snap().SubtitlesDir, "m6.vtt"), []byte(sampleVTT), 0o644); err != nil {
		t.Fatalf("写磁盘 VTT: %v", err)
	}

	svc.SetWritableGate(func() bool { return false })
	svc.backfillVTT()

	var count int64
	svc.db.Model(&model.SubtitleVTT{}).Count(&count)
	if count != 0 {
		t.Fatalf("只读副本上不应写入任何正文行，实际写了 %d 行", count)
	}
}

func TestDropVTTRemovesBothCopies(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	seedPendingJob(t, svc.db, "m7", "https://example.com/e.m3u8")
	job := markJobDone(t, svc, "m7")

	abs := filepath.Join(svc.snap().SubtitlesDir, job.VttPath)
	if err := os.WriteFile(abs, []byte(sampleVTT), 0o644); err != nil {
		t.Fatalf("写磁盘 VTT: %v", err)
	}
	if err := svc.storeVTT("m7", []byte(sampleVTT)); err != nil {
		t.Fatalf("storeVTT: %v", err)
	}

	svc.dropVTT(job)

	var count int64
	svc.db.Model(&model.SubtitleVTT{}).Where("media_id = ?", "m7").Count(&count)
	if count != 0 {
		t.Fatal("数据库正文未被删除")
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatal("磁盘 VTT 文件未被删除")
	}
}
