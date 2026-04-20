package util

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	ffmpegTimeout  = 60 * time.Second
	ffprobeTimeout = 30 * time.Second
	thumbMaxBytes  = 30 * 1024
)

// FFProbeDuration 通过 ffprobe 获取 m3u8 流的时长（秒）。
func FFProbeDuration(ctx context.Context, m3u8URL string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		m3u8URL,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w, stderr: %s", err, stderr.String())
	}

	s := strings.TrimSpace(stdout.String())
	if s == "" || s == "N/A" {
		return 0, fmt.Errorf("ffprobe returned empty duration")
	}
	dur, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration parse error: %w", err)
	}
	return dur, nil
}

// FFmpegThumbnail 从 m3u8 流截取一帧保存为 webp。
// seekSec 为截取位置（秒），outPath 为输出文件路径（应以 .webp 结尾）。
// 如果生成的文件超过 30KB，会以较低质量重编码一次。
func FFmpegThumbnail(ctx context.Context, m3u8URL string, seekSec float64, outPath string) error {
	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	args := []string{
		"-ss", fmt.Sprintf("%.2f", seekSec),
		"-i", m3u8URL,
		"-frames:v", "1",
		"-vf", "scale=480:-1",
		"-c:v", "libwebp",
		"-q:v", "75",
		"-y",
		outPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg thumbnail failed: %w, stderr: %s", err, stderr.String())
	}

	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("stat thumbnail: %w", err)
	}
	if info.Size() <= thumbMaxBytes {
		return nil
	}

	// 文件过大，降质重编码
	return reencodeWebp(ctx, outPath, 50)
}

// RandomSeekSec 返回 duration 的 10%~40% 之间的随机位置。
func RandomSeekSec(duration float64) float64 {
	lo := duration * 0.10
	hi := duration * 0.40
	return lo + rand.Float64()*(hi-lo)
}

func reencodeWebp(ctx context.Context, path string, quality int) error {
	tmp := path + ".tmp.webp"
	defer os.Remove(tmp)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", path,
		"-c:v", "libwebp",
		"-q:v", strconv.Itoa(quality),
		"-y",
		tmp,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg re-encode failed: %w, stderr: %s", err, stderr.String())
	}
	return os.Rename(tmp, path)
}
