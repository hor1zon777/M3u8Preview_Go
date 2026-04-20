package util

import (
	"testing"
	"time"
)

func TestSSETicket_IssueConsume(t *testing.T) {
	s := NewSSETicketStore()
	defer s.Stop()

	ticket := s.Issue("user-1", "ADMIN")
	if ticket == "" {
		t.Fatal("empty ticket")
	}

	claim, ok := s.Consume(ticket)
	if !ok {
		t.Fatal("first consume should succeed")
	}
	if claim.UserID != "user-1" || claim.Role != "ADMIN" {
		t.Fatalf("wrong claim: %+v", claim)
	}
	// 一次性：第二次消费应该失败
	if _, ok := s.Consume(ticket); ok {
		t.Fatal("second consume should fail (one-time use)")
	}
}

func TestSSETicket_Expired(t *testing.T) {
	s := NewSSETicketStore()
	defer s.Stop()

	// 手动写入一个过期 entry
	s.mu.Lock()
	s.entries["expired"] = ticketEntry{
		claim:     SSETicketClaim{UserID: "u", Role: "USER"},
		expiresAt: time.Now().Add(-time.Second).UnixNano(),
	}
	s.mu.Unlock()

	if _, ok := s.Consume("expired"); ok {
		t.Fatal("expired ticket should not be consumable")
	}
}

func TestSSETicket_Unknown(t *testing.T) {
	s := NewSSETicketStore()
	defer s.Stop()
	if _, ok := s.Consume("does-not-exist"); ok {
		t.Fatal("unknown ticket should fail")
	}
}
