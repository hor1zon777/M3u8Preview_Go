package util

import (
	"testing"
	"time"
)

func TestChallenge_IssueConsume(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()

	id, salt := s.Issue()
	if id == "" {
		t.Fatal("empty id")
	}
	if len(salt) != 32 {
		t.Fatalf("salt length=%d, want 32", len(salt))
	}

	got, ok := s.Consume(id)
	if !ok {
		t.Fatal("first consume should succeed")
	}
	if string(got) != string(salt) {
		t.Fatal("consumed salt != issued salt")
	}
	// 一次性：第二次消费应该失败
	if _, ok := s.Consume(id); ok {
		t.Fatal("second consume should fail (one-time use)")
	}
}

func TestChallenge_Expired(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()

	s.mu.Lock()
	s.entries["expired"] = challengeEntry{
		salt:      []byte("x"),
		expiresAt: time.Now().Add(-time.Second).UnixNano(),
	}
	s.mu.Unlock()

	if _, ok := s.Consume("expired"); ok {
		t.Fatal("expired challenge should not be consumable")
	}
}

func TestChallenge_Unknown(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	if _, ok := s.Consume("does-not-exist"); ok {
		t.Fatal("unknown challenge should fail")
	}
}

func TestChallenge_MaxItemsEviction(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	s.maxItems = 4 // 压到小值便于测试

	ids := make([]string, 0, 10)
	for range 10 {
		id, _ := s.Issue()
		ids = append(ids, id)
	}
	s.mu.Lock()
	size := len(s.entries)
	s.mu.Unlock()
	if size > 4 {
		t.Fatalf("entries=%d should be clamped to <=4", size)
	}
}

func TestChallenge_UniqueIDs(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	seen := make(map[string]bool)
	for i := range 1000 {
		id, _ := s.Issue()
		if seen[id] {
			t.Fatalf("duplicate id at iter %d: %s", i, id)
		}
		seen[id] = true
	}
}
