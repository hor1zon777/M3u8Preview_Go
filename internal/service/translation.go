// Package service
// translation.go 提供 OpenAI 兼容的翻译客户端。
//
// 调用契约：
//   - POST <baseURL>/v1/chat/completions
//   - Authorization: Bearer <apiKey>
//   - 请求体形如 OpenAI Chat Completions：{model, messages, temperature, response_format?}
//
// 兼容服务：DeepSeek、Qwen、智谱、OpenAI、Together、自建 OpenAI 网关、ollama 等。
//
// 翻译策略：
//   - 批量打包 N 条字幕一起请求（N=BatchSize，默认 8）
//   - 强制 LLM 输出 JSON 数组，长度与输入一致
//   - 单批失败重试，仍失败则把日文原文回退到目标位置（保证字幕轨完整）
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Translator 翻译接口。
type Translator interface {
	Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error)
	ModelName() string
}

// OpenAICompatibleTranslator 通过 OpenAI Chat Completions 兼容接口做翻译。
type OpenAICompatibleTranslator struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
	maxRetries int
}

// NewOpenAICompatibleTranslator 构造。
func NewOpenAICompatibleTranslator(baseURL, apiKey, model string, maxRetries int) *OpenAICompatibleTranslator {
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &OpenAICompatibleTranslator{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
		maxRetries: maxRetries,
	}
}

// ModelName 返回模型名。
func (t *OpenAICompatibleTranslator) ModelName() string { return t.model }

// PreflightCheck 启动时校验：base url 与 api key 已配置。
func (t *OpenAICompatibleTranslator) PreflightCheck() error {
	if t.baseURL == "" {
		return fmt.Errorf("translate base url empty (set SUBTITLE_TRANSLATE_BASE_URL)")
	}
	if t.apiKey == "" {
		return fmt.Errorf("translate api key empty (set SUBTITLE_TRANSLATE_API_KEY)")
	}
	if t.model == "" {
		return fmt.Errorf("translate model empty (set SUBTITLE_TRANSLATE_MODEL)")
	}
	return nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	// 注：不强制 response_format=json_object，因为部分兼容服务不支持；
	// 我们通过 prompt 约束 + 解析容忍来兜底。
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Translate 批量翻译 texts 为目标语言；返回数组长度与输入一致。
// 失败时返回 error，调用方决定是否回退到原文。
func (t *OpenAICompatibleTranslator) Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	srcLabel := languageLabel(sourceLang)
	tgtLabel := languageLabel(targetLang)

	systemPrompt := fmt.Sprintf(
		"You are a professional subtitle translator. Translate the input %s subtitles to %s.\n"+
			"Rules:\n"+
			"1. Output ONLY a JSON array of strings, no commentary, no markdown fences.\n"+
			"2. Preserve the order and the array length EXACTLY (input has %d items, output must have %d items).\n"+
			"3. Keep proper nouns and technical terms accurate.\n"+
			"4. Use natural, conversational %s suitable for video subtitles.\n"+
			"5. Do NOT merge or split lines.",
		srcLabel, tgtLabel, len(texts), len(texts), tgtLabel,
	)

	// 把 texts 编为 JSON 数组当用户消息内容
	inputJSON, err := json.Marshal(texts)
	if err != nil {
		return nil, fmt.Errorf("marshal input texts: %w", err)
	}
	userPrompt := fmt.Sprintf("Input subtitles (JSON array of %d strings):\n%s", len(texts), string(inputJSON))

	body := chatCompletionRequest{
		Model: t.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.2,
	}

	var lastErr error
	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		out, err := t.doRequest(ctx, body, len(texts))
		if err == nil {
			return out, nil
		}
		lastErr = err
		// 退避
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	return nil, fmt.Errorf("translate failed after %d retries: %w", t.maxRetries+1, lastErr)
}

// doRequest 发起一次 HTTP 调用并解析 JSON 数组结果。
func (t *OpenAICompatibleTranslator) doRequest(ctx context.Context, body chatCompletionRequest, expectedLen int) ([]string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncateString(string(respBody), 500))
	}

	var parsed chatCompletionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse chat response: %w (body=%s)", err, truncateString(string(respBody), 500))
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("upstream error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}
	content := parsed.Choices[0].Message.Content
	arr, err := extractJSONArray(content, expectedLen)
	if err != nil {
		return nil, fmt.Errorf("extract json array: %w (content=%s)", err, truncateString(content, 500))
	}
	return arr, nil
}

// extractJSONArray 从 LLM 文本输出中提取 JSON 字符串数组。
// 容忍以下情形：
//   - 整体就是合法 JSON 数组
//   - 包裹在 ```json ... ``` 代码块内
//   - 前后有解释性文字
//
// 数组长度与 expected 不一致时返回 error。
func extractJSONArray(content string, expected int) ([]string, error) {
	s := strings.TrimSpace(content)

	// 去除 markdown 代码块标记
	if strings.HasPrefix(s, "```") {
		// 去掉首行 ``` 或 ```json
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = s[idx+1:]
		}
		// 去尾部 ```
		s = strings.TrimSuffix(strings.TrimRight(s, "` \n\r\t"), "```")
		s = strings.TrimSpace(s)
	}

	// 找第一个 [ 和最后一个 ]
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < 0 || end < start {
		return nil, fmt.Errorf("no json array found")
	}
	candidate := s[start : end+1]

	var arr []string
	if err := json.Unmarshal([]byte(candidate), &arr); err != nil {
		return nil, fmt.Errorf("unmarshal array: %w", err)
	}
	if len(arr) != expected {
		return nil, fmt.Errorf("array length mismatch: got %d, want %d", len(arr), expected)
	}
	return arr, nil
}

// languageLabel 把 ISO-639-1 代码映射成 LLM 更熟悉的英文标签。
func languageLabel(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "ja", "jp", "japanese":
		return "Japanese"
	case "zh", "zh-cn", "zh-hans", "chinese":
		return "Simplified Chinese"
	case "zh-tw", "zh-hk", "zh-hant":
		return "Traditional Chinese"
	case "en", "english":
		return "English"
	case "ko", "korean":
		return "Korean"
	case "fr", "french":
		return "French"
	case "de", "german":
		return "German"
	case "es", "spanish":
		return "Spanish"
	case "ru", "russian":
		return "Russian"
	default:
		if code == "" {
			return "the source language"
		}
		return code
	}
}
