// Package util
// ffmpeg_subtitle.go 提供字幕模块所需的音频抽取。
//
// 抽取目标：whisper.cpp 标准输入格式
//   - 16kHz 采样率
//   - 单声道（-ac 1）
//   - 16-bit signed little-endian PCM（-c:a pcm_s16le）
//   - WAV 容器
//
// 调用 ffmpeg 直接拉 m3u8 流并抽音轨；不下载完整视频文件，节省磁盘。
// 长视频可能耗时数十秒到几分钟，调用方应在独立 goroutine 里跑。
package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// 抽音频默认超时：30 分钟（覆盖 1~2 小时长视频；CPU 解码 + 网络下载综合）
const ffmpegAudioExtractTimeout = 30 * time.Minute

// ExtractAudioForASR 从 m3u8 URL 抽取 16kHz 单声道 PCM WAV，写到 outPath。
// 父目录会自动创建。已有同名文件会被覆盖（-y）。
//
// 注意：ffmpeg 进程会忽略 ctx.Done 中的 SIGKILL 之外的信号，
// 我们用 CommandContext 来确保 cancel 时进程被强制结束。
func ExtractAudioForASR(ctx context.Context, m3u8URL, outPath string) error {
	if err := assertM3U8URLSafe(m3u8URL); err != nil {
		return err
	}
	if outPath == "" {
		return fmt.Errorf("outPath empty")
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("mkdir audio outdir: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, ffmpegAudioExtractTimeout)
	defer cancel()

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-y",
		"-i", m3u8URL,
		"-vn",                  // 丢视频流
		"-ac", "1",             // 单声道
		"-ar", "16000",         // 16kHz
		"-c:a", "pcm_s16le",    // 16-bit PCM
		"-f", "wav",            // WAV 容器
		outPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// 失败时清理半成品文件，避免下次误判已抽取
		_ = os.Remove(outPath)
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg audio extract timeout after %s: %s", ffmpegAudioExtractTimeout, stderr.String())
		}
		return fmt.Errorf("ffmpeg audio extract failed: %w, stderr: %s", err, stderr.String())
	}

	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("stat audio file: %w", err)
	}
	if info.Size() < 1024 {
		_ = os.Remove(outPath)
		return fmt.Errorf("ffmpeg produced suspicious tiny audio file (%d bytes), likely empty stream", info.Size())
	}
	return nil
}
