// Package service
// asr.go 提供基于 whisper.cpp 二进制的语音识别（CPU 友好）。
//
// 调用方式：通过 exec.Command 拉起 whisper-cli 进程，输出 JSON 格式的 segments。
// 与项目其它模块（ffmpeg、ffprobe）的调用风格一致，不依赖 Python / CGO。
//
// whisper.cpp CLI 输出格式（-oj 模式）：
//
//	{
//	  "transcription": [
//	    {"timestamps": {"from": "00:00:00,000", "to": "00:00:02,400"},
//	     "offsets": {"from": 0, "to": 2400},
//	     "text": "..."},
//	    ...
//	  ]
//	}
//
// 如果 whisper.cpp 版本变化导致字段名变化，本文件需要适配。
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ASRSegment 语音识别输出的一条字幕（毫秒级时间）。
type ASRSegment struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

// ASRResult ASR 结果。
type ASRResult struct {
	Language string       `json:"language"`
	Segments []ASRSegment `json:"segments"`
}

// ASRClient ASR 客户端接口（便于测试 mock）。
type ASRClient interface {
	Transcribe(ctx context.Context, audioPath string) (*ASRResult, error)
	ModelName() string
}

// WhisperCppASR 基于 whisper.cpp CLI 的 ASR 实现。
type WhisperCppASR struct {
	bin      string // whisper-cli 二进制路径
	model    string // GGML 模型文件路径
	language string // 源语言 ISO-639-1
	threads  int    // CPU 线程数（0=自动）
}

// NewWhisperCppASR 构造。bin / model 必须已存在；language 默认 "ja"。
func NewWhisperCppASR(bin, modelPath, language string, threads int) *WhisperCppASR {
	if bin == "" {
		bin = "whisper-cli"
	}
	if language == "" {
		language = "ja"
	}
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	return &WhisperCppASR{
		bin:      bin,
		model:    modelPath,
		language: language,
		threads:  threads,
	}
}

// ModelName 返回模型文件名（去除路径前缀），便于审计。
func (w *WhisperCppASR) ModelName() string {
	if w.model == "" {
		return ""
	}
	return filepath.Base(w.model)
}

// PreflightCheck 启动时校验：bin 可执行、model 文件存在。
func (w *WhisperCppASR) PreflightCheck() error {
	if w.model == "" {
		return fmt.Errorf("whisper model path empty (set SUBTITLE_WHISPER_MODEL)")
	}
	if _, err := os.Stat(w.model); err != nil {
		return fmt.Errorf("whisper model not found at %s: %w", w.model, err)
	}
	if _, err := exec.LookPath(w.bin); err != nil {
		return fmt.Errorf("whisper binary %q not found in PATH: %w", w.bin, err)
	}
	return nil
}

// whisperJSONOutput 解析 whisper.cpp -oj 输出 JSON 结构。
type whisperJSONOutput struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Timestamps struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"timestamps"`
		Offsets struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// Transcribe 识别 audioPath 指向的 WAV 文件。
//
// 命令行示例：
//
//	whisper-cli -m model.bin -l ja -t 8 -oj -of <stem> <audio.wav>
//
// -of 指定输出文件名前缀（whisper.cpp 会自动追加 .json）；为避免和别的任务冲突，
// 我们用 audioPath 同目录同 stem，识别完成后读 JSON 再删除。
func (w *WhisperCppASR) Transcribe(ctx context.Context, audioPath string) (*ASRResult, error) {
	if _, err := os.Stat(audioPath); err != nil {
		return nil, fmt.Errorf("audio file not found: %w", err)
	}

	dir := filepath.Dir(audioPath)
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	outStem := filepath.Join(dir, base+".asr")
	outJSON := outStem + ".json"

	defer func() {
		_ = os.Remove(outJSON)
	}()

	args := []string{
		"-m", w.model,
		"-l", w.language,
		"-t", strconv.Itoa(w.threads),
		"-oj",                  // 输出 JSON
		"-of", outStem,         // 输出前缀
		"-nt",                  // no-timestamps in stdout（减少噪声）
		"--print-progress",     // 进度信息走 stderr
		audioPath,
	}

	// 大模型在 CPU 上 1 小时音频可能跑几十分钟；限制较宽
	cctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()

	cmd := exec.CommandContext(cctx, w.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("whisper transcribe timeout: %s", stderr.String())
		}
		return nil, fmt.Errorf("whisper run failed: %w, stderr: %s", err, truncateString(stderr.String(), 4000))
	}

	raw, err := os.ReadFile(outJSON)
	if err != nil {
		return nil, fmt.Errorf("read whisper json output: %w", err)
	}

	var parsed whisperJSONOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse whisper json: %w", err)
	}

	segs := make([]ASRSegment, 0, len(parsed.Transcription))
	for _, t := range parsed.Transcription {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		segs = append(segs, ASRSegment{
			StartMs: t.Offsets.From,
			EndMs:   t.Offsets.To,
			Text:    text,
		})
	}

	lang := parsed.Result.Language
	if lang == "" {
		lang = w.language
	}
	return &ASRResult{Language: lang, Segments: segs}, nil
}

// truncateString 截断字符串到 max（含 "..."）。
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
