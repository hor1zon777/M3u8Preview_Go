// github.go 最小 GitHub Release 客户端。
//
// 供应链安全边界：
//   - owner/repo 编译期常量，不提供任何环境变量覆盖——防"改个 env 就把更新源
//     重定向到攻击者仓库"的投毒路径；
//   - 资产 URL 必须以本仓库 releases/download/ 前缀开头；
//   - 重定向逐跳校验：仅 https，host 限 github.com / *.githubusercontent.com；
//   - 下载后强制 sha256 对照同一 Release 附带的 checksums.txt（manager.go）。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 更新源仓库，编译期 pin 死。
const (
	updateOwner = "hor1zon777"
	updateRepo  = "M3u8Preview_Go"
)

// assetURLPrefix 合法资产下载地址的前缀。
var assetURLPrefix = fmt.Sprintf("https://github.com/%s/%s/releases/download/", updateOwner, updateRepo)

// ErrRateLimited GitHub 匿名 API 限流（60 req/h/IP）。
var ErrRateLimited = errors.New("GitHub API 限流，请稍后再试")

// ReleaseInfo 一次 releases/latest 查询的解析结果。
type ReleaseInfo struct {
	// Version 不带 v 前缀（如 "0.4.0"）。
	Version     string
	TagName     string
	Notes       string
	PublishedAt time.Time
	AssetName   string
	AssetSize   int64
	AssetURL    string
	ChecksumURL string
}

// ghClient GitHub API 与资产下载客户端。
type ghClient struct {
	http    *http.Client
	apiBase string // 测试注入；生产恒为 https://api.github.com
}

func newGHClient() *ghClient {
	return &ghClient{
		http: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: checkGitHubRedirect,
		},
		apiBase: "https://api.github.com",
	}
}

// checkGitHubRedirect 重定向逐跳白名单：GitHub 资产下载会 302 到
// objects.githubusercontent.com 等 GitHub 自有域，其余一律拒绝。
func checkGitHubRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("重定向次数过多")
	}
	if req.URL.Scheme != "https" {
		return errors.New("拒绝重定向到非 https 地址")
	}
	host := req.URL.Hostname()
	if host != "github.com" && host != "api.github.com" && !strings.HasSuffix(host, ".githubusercontent.com") {
		return fmt.Errorf("拒绝重定向到不受信任的主机 %s", host)
	}
	return nil
}

// latestRelease 查询最新正式 Release（GitHub 的 releases/latest 天然排除
// prerelease 与 draft，预发 tag 可安全用于演练）。
func (c *ghClient) latestRelease(ctx context.Context) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.apiBase, updateOwner, updateRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
			return nil, ErrRateLimited
		}
		return nil, fmt.Errorf("GitHub API 返回 HTTP %d", resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, errors.New("仓库还没有发布任何正式版本")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub API 返回 HTTP %d", resp.StatusCode)
	}

	var body struct {
		TagName     string    `json:"tag_name"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			Size               int64  `json:"size"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("解析 GitHub API 响应: %w", err)
	}

	info := &ReleaseInfo{
		Version:     strings.TrimPrefix(body.TagName, "v"),
		TagName:     body.TagName,
		Notes:       truncateRunes(body.Body, 2000),
		PublishedAt: body.PublishedAt,
	}
	for _, a := range body.Assets {
		switch {
		case strings.HasPrefix(a.Name, "m3u8preview_") && strings.HasSuffix(a.Name, "_linux_amd64.tar.gz"):
			info.AssetName = a.Name
			info.AssetSize = a.Size
			info.AssetURL = a.BrowserDownloadURL
		case a.Name == "checksums.txt":
			info.ChecksumURL = a.BrowserDownloadURL
		}
	}
	if info.AssetName == "" || info.ChecksumURL == "" {
		return nil, errors.New("最新 Release 缺少更新产物（tar.gz / checksums.txt），可能是旧版发布流程")
	}
	// 资产 URL 必须落在本仓库的 releases/download/ 之下——即便 API 响应被篡改，
	// 下载目标也出不了这个仓库。
	if !strings.HasPrefix(info.AssetURL, assetURLPrefix) || !strings.HasPrefix(info.ChecksumURL, assetURLPrefix) {
		return nil, errors.New("Release 资产地址不在受信任的下载前缀内，拒绝")
	}
	return info, nil
}

// download 流式下载 url 到 w：超过 maxBytes 报错；progress 每写入一块回调一次。
func (c *ghClient) download(ctx context.Context, url string, w io.Writer, maxBytes int64, progress func(delta int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// 下载用无整体超时的 client（大文件可能超过 15s），但保留重定向白名单；
	// 慢速攻击由 ctx（调用方设总超时）兜底。
	client := &http.Client{CheckRedirect: checkGitHubRedirect}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxBytes+1)
	buf := make([]byte, 128*1024)
	var total int64
	for {
		n, rerr := limited.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > maxBytes {
				return fmt.Errorf("下载内容超过 %d 字节上限", maxBytes)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if progress != nil {
				progress(int64(n))
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// parseChecksums 解析 sha256sum 输出格式（"<hex>  <filename>"），返回目标文件的期望哈希。
func parseChecksums(content, filename string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		// sha256sum 的第二列可能带 "*" 前缀（binary 模式）
		if strings.TrimPrefix(fields[1], "*") == filename {
			sum := strings.ToLower(fields[0])
			if len(sum) != 64 {
				return "", fmt.Errorf("checksums.txt 中 %s 的哈希长度非法", filename)
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("checksums.txt 中找不到 %s 的条目", filename)
}

// truncateRunes 按 rune 截断（发布说明可能是中文）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
