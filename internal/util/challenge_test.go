package util

import (
	"errors"
	"testing"
	"time"
)

const testFP = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
const testIP = "10.0.0.1"

func TestChallenge_IssueConsume(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()

	id, salt, err := s.Issue(testFP, testIP)
	if err != nil {
		t.Fatalf("issue err=%v", err)
	}
	if id == "" {
		t.Fatal("empty id")
	}
	if len(salt) != 32 {
		t.Fatalf("salt length=%d, want 32", len(salt))
	}

	got, fp, ok := s.Consume(id)
	if !ok {
		t.Fatal("first consume should succeed")
	}
	if string(got) != string(salt) {
		t.Fatal("consumed salt != issued salt")
	}
	if fp != testFP {
		t.Fatalf("fingerprint mismatch: %s", fp)
	}
	if _, _, ok := s.Consume(id); ok {
		t.Fatal("second consume should fail (one-time use)")
	}
}

func TestChallenge_Expired(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()

	s.mu.Lock()
	s.entries["expired"] = challengeEntry{
		salt:        []byte("x"),
		fingerprint: testFP,
		ip:          testIP,
		issuedAt:    time.Now().Add(-2 * time.Second).UnixNano(),
		expiresAt:   time.Now().Add(-time.Second).UnixNano(),
	}
	s.mu.Unlock()

	if _, _, ok := s.Consume("expired"); ok {
		t.Fatal("expired challenge should not be consumable")
	}
}

func TestChallenge_Unknown(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	if _, _, ok := s.Consume("does-not-exist"); ok {
		t.Fatal("unknown challenge should fail")
	}
}

// 全局 cap 超限时应按 issuedAt 最旧优先淘汰。
func TestChallenge_MaxItemsEvictsOldest(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	s.maxItems = 4
	s.perIPLimit = 0 // 关闭 per-IP 限制，专测全局 LRU

	// 多样化 IP 避免触达 perIPLimit；此测关闭了 perIPLimit 但保持习惯
	ids := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		id, _, err := s.Issue(testFP, "")
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		ids = append(ids, id)
		// 错开时间戳确保 LRU 有序
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	size := len(s.entries)
	_, firstExists := s.entries[ids[0]]
	_, lastExists := s.entries[ids[5]]
	s.mu.Unlock()
	if size > 4 {
		t.Fatalf("entries=%d, want <=4", size)
	}
	if firstExists {
		t.Fatal("oldest entry should have been evicted")
	}
	if !lastExists {
		t.Fatal("newest entry should remain")
	}
}

// 单 IP 并发配额用尽应返回 ErrChallengeStoreBusy。
func TestChallenge_PerIPLimitBlocks(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	s.perIPLimit = 3

	for i := 0; i < 3; i++ {
		if _, _, err := s.Issue(testFP, "1.2.3.4"); err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
	}
	if _, _, err := s.Issue(testFP, "1.2.3.4"); !errors.Is(err, ErrChallengeStoreBusy) {
		t.Fatalf("want ErrChallengeStoreBusy, got %v", err)
	}
	// 其它 IP 不受影响
	if _, _, err := s.Issue(testFP, "5.6.7.8"); err != nil {
		t.Fatalf("other IP should succeed: %v", err)
	}
}

func TestChallenge_UniqueIDs(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	s.perIPLimit = 0
	s.maxItems = 10000
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id, _, err := s.Issue(testFP, "")
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate id at iter %d: %s", i, id)
		}
		seen[id] = true
	}
}
