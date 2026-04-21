package util

import (
	"testing"
	"time"
)

const testFP = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

func TestChallenge_IssueConsume(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()

	id, salt := s.Issue(testFP)
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

func TestChallenge_MaxItemsEviction(t *testing.T) {
	s := NewChallengeStore()
	defer s.Stop()
	s.maxItems = 4

	for range 10 {
		s.Issue(testFP)
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
		id, _ := s.Issue(testFP)
		if seen[id] {
			t.Fatalf("duplicate id at iter %d: %s", i, id)
		}
		seen[id] = true
	}
}
