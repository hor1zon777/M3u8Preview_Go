package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.4.0", "0.3.0", true},
		{"0.3.0", "0.3.0", false},
		{"0.3.0", "0.4.0", false},
		{"1.0.0", "0.99.99", true},
		// prerelease 语义：正式版新于同号预发版
		{"0.4.0", "0.4.0-rc.1", true},
		{"0.4.0-rc.1", "0.4.0", false},
		// 非法版本一律 false——比较不了就不更新
		{"dev", "0.3.0", false},
		{"0.4.0", "dev", false},
		{"", "0.3.0", false},
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v, 期望 %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	content := "abc\n" +
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  m3u8preview_0.4.0_linux_amd64.tar.gz\n" +
		"deadbeef  other.txt\n"
	sum, err := parseChecksums(content, "m3u8preview_0.4.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}
	if sum != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("哈希解析错误: %s", sum)
	}
	if _, err := parseChecksums(content, "missing.tar.gz"); err == nil {
		t.Fatal("找不到条目应报错")
	}
	if _, err := parseChecksums("deadbeef  other.txt\n", "other.txt"); err == nil {
		t.Fatal("哈希长度非法应报错")
	}
}

// makeTarGz 构造测试用 tar.gz。entries: 路径 → 内容；typ 允许覆盖条目类型。
func makeTarGz(t *testing.T, entries map[string]string, mutate func(h *tar.Header)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if mutate != nil {
			mutate(h)
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatalf("write body: %v", err)
			}
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestExtractTarGzHappyPath(t *testing.T) {
	dest := t.TempDir()
	data := makeTarGz(t, map[string]string{"server": "binary", "web-dist/index.html": "<html>"}, nil)
	files, err := extractTarGz(bytes.NewReader(data), dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(files) != 2 || files["server"] == "" || files["web-dist/index.html"] == "" {
		t.Fatalf("files 记录不完整: %v", files)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "server")); err != nil || string(b) != "binary" {
		t.Fatalf("解包内容错误: %s %v", b, err)
	}
}

func TestExtractTarGzRejectsTraversalAndSymlink(t *testing.T) {
	dest := t.TempDir()
	// 路径穿越
	data := makeTarGz(t, map[string]string{"../evil": "x"}, nil)
	if _, err := extractTarGz(bytes.NewReader(data), dest); err == nil {
		t.Fatal("路径穿越应被拒绝")
	}
	// 绝对路径
	data = makeTarGz(t, map[string]string{"/etc/passwd": "x"}, nil)
	if _, err := extractTarGz(bytes.NewReader(data), dest); err == nil {
		t.Fatal("绝对路径应被拒绝")
	}
	// symlink
	data = makeTarGz(t, map[string]string{"link": ""}, func(h *tar.Header) {
		h.Typeflag = tar.TypeSymlink
		h.Linkname = "/etc/passwd"
		h.Size = 0
	})
	if _, err := extractTarGz(bytes.NewReader(data), dest); err == nil {
		t.Fatal("symlink 应被拒绝")
	}
}

// writeStagedFixture 在临时 dataDir 下摆出一个可用的 staged 目录。
func writeStagedFixture(t *testing.T, dataDir, version string) {
	t.Helper()
	staged := stagedDir(dataDir)
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatalf("mkdir staged: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "server"), []byte("fake-binary"), 0o755); err != nil {
		t.Fatalf("write server: %v", err)
	}
	sum, err := fileSHA256(filepath.Join(staged, "server"))
	if err != nil {
		t.Fatalf("sha256: %v", err)
	}
	info := &StagedInfo{
		Version:  version,
		Files:    map[string]string{"server": sum},
		StagedAt: time.Now(),
	}
	if err := WriteStagedInfoForTest(staged, info); err != nil {
		t.Fatalf("write staged.json: %v", err)
	}
}

func TestPreflightBranches(t *testing.T) {
	t.Run("无staged用镜像版", func(t *testing.T) {
		if got := RunPreflight(t.TempDir(), "0.3.0"); got != 1 {
			t.Fatalf("期望 1, 得到 %d", got)
		}
	})

	t.Run("staged更新通过预检", func(t *testing.T) {
		dir := t.TempDir()
		writeStagedFixture(t, dir, "0.4.0")
		if got := RunPreflight(dir, "0.3.0"); got != 0 {
			t.Fatalf("期望 0, 得到 %d", got)
		}
	})

	t.Run("镜像追平后清理staged", func(t *testing.T) {
		dir := t.TempDir()
		writeStagedFixture(t, dir, "0.4.0")
		if got := RunPreflight(dir, "0.4.0"); got != 1 {
			t.Fatalf("期望 1, 得到 %d", got)
		}
		if _, err := os.Stat(stagedDir(dir)); !os.IsNotExist(err) {
			t.Fatal("镜像追平后 staged 目录应被清理")
		}
	})

	t.Run("连续启动失败弃用staged", func(t *testing.T) {
		dir := t.TempDir()
		writeStagedFixture(t, dir, "0.4.0")
		if err := os.WriteFile(attemptsFile(dir), []byte("3"), 0o644); err != nil {
			t.Fatalf("write attempts: %v", err)
		}
		if got := RunPreflight(dir, "0.3.0"); got != 1 {
			t.Fatalf("期望 1, 得到 %d", got)
		}
		if _, err := os.Stat(stagedDir(dir)); !os.IsNotExist(err) {
			t.Fatal("失败的 staged 应被移走")
		}
		// 现场应保留为 failed-* 目录
		entries, _ := os.ReadDir(updatesDir(dir))
		found := false
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "failed-0.4.0-") {
				found = true
			}
		}
		if !found {
			t.Fatal("应保留 failed-<ver>-<ts> 现场目录")
		}
	})

	t.Run("哈希自检失败弃用staged", func(t *testing.T) {
		dir := t.TempDir()
		writeStagedFixture(t, dir, "0.4.0")
		// 篡改二进制
		if err := os.WriteFile(filepath.Join(stagedDir(dir), "server"), []byte("tampered"), 0o755); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		if got := RunPreflight(dir, "0.3.0"); got != 1 {
			t.Fatalf("期望 1, 得到 %d", got)
		}
		if _, err := os.Stat(stagedDir(dir)); !os.IsNotExist(err) {
			t.Fatal("哈希不符的 staged 应被删除")
		}
	})

	t.Run("dev镜像不比较不删除", func(t *testing.T) {
		dir := t.TempDir()
		writeStagedFixture(t, dir, "0.4.0")
		if got := RunPreflight(dir, "dev"); got != 1 {
			t.Fatalf("期望 1, 得到 %d", got)
		}
		if _, err := os.Stat(stagedDir(dir)); err != nil {
			t.Fatal("dev 镜像下不应删除 staged（保守回退即可）")
		}
	})
}

func TestManagerApplyGuards(t *testing.T) {
	m := New(t.TempDir(), "0.3.0", "abc", false)

	// 未检查过 → ErrNoUpdate
	if err := m.Apply("0.4.0"); err != ErrNoUpdate {
		t.Fatalf("期望 ErrNoUpdate, 得到 %v", err)
	}

	// 手工塞一个 latest 模拟已检查
	m.mu.Lock()
	m.latest = &ReleaseInfo{Version: "0.4.0", AssetSize: 100}
	m.state = StateUpdateAvailable
	m.mu.Unlock()

	// 版本不匹配 → TOCTOU 防护
	if err := m.Apply("0.5.0"); err != ErrVersionMismatch {
		t.Fatalf("期望 ErrVersionMismatch, 得到 %v", err)
	}

	// dev 构建拒绝
	dev := New(t.TempDir(), "dev", "", false)
	if err := dev.Apply("0.4.0"); err != ErrDevBuild {
		t.Fatalf("期望 ErrDevBuild, 得到 %v", err)
	}
	if dev.Enabled() {
		t.Fatal("dev 构建 Enabled 应为 false")
	}

	// 环境禁用拒绝
	disabled := New(t.TempDir(), "0.3.0", "", true)
	if err := disabled.Apply("0.4.0"); err != ErrDisabled {
		t.Fatalf("期望 ErrDisabled, 得到 %v", err)
	}
}

func TestManagerLoadsExistingStaged(t *testing.T) {
	dir := t.TempDir()
	writeStagedFixture(t, dir, "0.4.0")
	m := New(dir, "0.3.0", "", false)
	s := m.Status()
	if s.State != StateStaged || s.Staged == nil || s.Staged.Version != "0.4.0" {
		t.Fatalf("启动时应恢复 staged 状态: %+v", s)
	}
}
