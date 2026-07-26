// archive.go 更新包（tar.gz）的安全解包。
//
// 解包目标是 /data 下的暂存目录，输入是"已通过 sha256 校验的官方产物"，
// 但校验对照的 checksums.txt 与包同源——若发布链路整体被攻破，哈希救不了。
// 因此解包自身仍按不可信输入处理：拒绝路径穿越/符号链接/超限条目，
// 是纵深防御的最后一层，而不是多余的偏执。
package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 解包硬限制。当前产物 ≈ 20MB（二进制）+ 2MB（dist），上限留了充分余量。
const (
	maxEntryBytes   = 128 << 20 // 单文件 128MiB
	maxTotalBytes   = 500 << 20 // 总量 500MiB
	maxEntries      = 5000
	maxArchiveBytes = 300 << 20 // tar.gz 本体下载上限
)

// extractTarGz 把 r（tar.gz 流）解包到 destDir，返回每个常规文件的
// 相对路径 → sha256（供 staged.json 记录、preflight 逐文件自检）。
func extractTarGz(r io.Reader, destDir string) (map[string]string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip 解压失败: %w", err)
	}
	defer func() { _ = gz.Close() }()

	files := make(map[string]string)
	tr := tar.NewReader(gz)
	var total int64
	entries := 0

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 tar 条目失败: %w", err)
		}

		entries++
		if entries > maxEntries {
			return nil, fmt.Errorf("tar 条目数超过 %d 上限", maxEntries)
		}

		rel, err := sanitizeTarPath(hdr.Name)
		if err != nil {
			return nil, err
		}
		if rel == "" {
			continue // "./" 之类的根条目
		}
		target := filepath.Join(destDir, rel)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if hdr.Size > maxEntryBytes {
				return nil, fmt.Errorf("文件 %s 超过单文件上限", rel)
			}
			total += hdr.Size
			if total > maxTotalBytes {
				return nil, fmt.Errorf("解包总量超过上限")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			sum, err := writeFileWithHash(target, tr, hdr.Size)
			if err != nil {
				return nil, fmt.Errorf("写出 %s: %w", rel, err)
			}
			files[filepath.ToSlash(rel)] = sum
		default:
			// symlink / hardlink / 设备文件等一律拒绝：官方产物里只有普通文件与目录，
			// 出现其它类型即为异常输入。
			return nil, fmt.Errorf("tar 含不允许的条目类型 %q (%s)", hdr.Typeflag, rel)
		}
	}
	return files, nil
}

// sanitizeTarPath 拒绝绝对路径与 ".." 穿越，返回清洗后的相对路径。
func sanitizeTarPath(name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	if name == "" || name == "." {
		return "", nil
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("tar 含非法路径 %q", name)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tar 含路径穿越条目 %q", name)
	}
	return clean, nil
}

// writeFileWithHash 写出单个文件（严格按声明大小截断）并返回其 sha256。
func writeFileWithHash(path string, r io.Reader, size int64) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, cerr := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, size))
	if err := f.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return "", cerr
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
