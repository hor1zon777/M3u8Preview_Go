package util

import (
	"testing"
	"time"
)

// TestProxySign_RoundTrip：签 + 验正向用例必须为 true。
func TestProxySign_RoundTrip(t *testing.T) {
	signer := NewProxySigner("test-secret", time.Hour)
	url := "https://example.com/video.m3u8"
	uid := "user-123"

	frag := signer.Sign(url, uid)
	if len(frag) == 0 {
		t.Fatal("empty signature fragment")
	}
	// 解析 expires 与 sig
	expires, sig := parseFragment(t, frag)
	if !signer.Verify(url, expires, sig, uid) {
		t.Fatal("verify should succeed")
	}
}

// TestProxySign_DifferentUser：签名绑定 userId，换个用户应失败。
func TestProxySign_DifferentUser(t *testing.T) {
	signer := NewProxySigner("test-secret", time.Hour)
	url := "https://example.com/video.m3u8"
	frag := signer.Sign(url, "user-a")
	expires, sig := parseFragment(t, frag)
	if signer.Verify(url, expires, sig, "user-b") {
		t.Fatal("signature should not verify for another user")
	}
}

// TestProxySign_Expired：过期 expires 必须失败。
func TestProxySign_Expired(t *testing.T) {
	signer := NewProxySigner("test-secret", time.Hour)
	url := "https://example.com/video.m3u8"
	uid := "u"
	// 伪造一个过期的 expires，signature 用当前 secret 重算
	past := time.Now().Add(-time.Hour).Unix()
	pastStr := toDigits(past)
	sig := signer.compute(url, pastStr, uid)
	if signer.Verify(url, pastStr, sig, uid) {
		t.Fatal("expired signature should not verify")
	}
}

// TestProxySign_TamperedURL：URL 不同签名失败。
func TestProxySign_TamperedURL(t *testing.T) {
	signer := NewProxySigner("test-secret", time.Hour)
	frag := signer.Sign("https://example.com/a.m3u8", "u")
	expires, sig := parseFragment(t, frag)
	if signer.Verify("https://example.com/b.m3u8", expires, sig, "u") {
		t.Fatal("tampered url should fail verify")
	}
}

// TestProxySign_KnownVector：把当前算法的确定输出锚定成快照。
// 任何人改了分隔符 / 换算法 / 加盐，这个测试都会挂。
// TS 侧等价验证：
//   node -e 'const c=require("crypto");console.log(c.createHmac("sha256","secret").update("https://x/a.m3u8\n1700000000\nuid123").digest("hex"))'
// 输出应与下方 want 一致。
func TestProxySign_KnownVector(t *testing.T) {
	signer := NewProxySigner("secret", time.Hour)
	got := signer.compute("https://x/a.m3u8", "1700000000", "uid123")
	want := "570dd52a20e978bb0480d0b8fa09eef4da0bdacd28b0f7138d73c5e7f3b9dd74"
	if got != want {
		t.Fatalf("hmac mismatch\nwant %s\ngot  %s", want, got)
	}
}

// --- helpers ---

func parseFragment(t *testing.T, frag string) (expires, sig string) {
	t.Helper()
	// frag 形如 "&expires=123&sig=abc"
	var e, s string
	if n, _ := sprintfScan(frag, &e, &s); n != 2 {
		t.Fatalf("unable to parse fragment: %q", frag)
	}
	return e, s
}

// sprintfScan 是 fmt.Sscanf 的替代，用简单的字符串切分避免额外依赖。
func sprintfScan(frag string, expires, sig *string) (int, error) {
	// 形如 "&expires=<num>&sig=<hex>"
	const ePrefix = "&expires="
	const sPrefix = "&sig="
	ei := indexOf(frag, ePrefix)
	si := indexOf(frag, sPrefix)
	if ei < 0 || si < 0 || si < ei {
		return 0, nil
	}
	*expires = frag[ei+len(ePrefix) : si]
	*sig = frag[si+len(sPrefix):]
	return 2, nil
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func toDigits(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
