// Package util
// sseticket.go 对齐 packages/server/src/utils/sseTicket.ts。
//
// 用途：EventSource 无法自定义 Authorization 头；前端先 POST /auth/sse-ticket 拿一次性 ticket，
// 再以 ?ticket=xxx 打开 SSE，后端一次性消费避免 token 泄漏到日志。
//
// TTL 30s、max 1000、过期条目在消费时惰性清理 + 每分钟扫描清理。
package util

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SSETicketClaim 是 ticket 消费后返回的 token 等价声明（userId + role）。
type SSETicketClaim struct {
	UserID string
	Role   string
}

type ticketEntry struct {
	claim     SSETicketClaim
	expiresAt int64 // unix nano
}

// SSETicketStore 是进程内 ticket 存储。单实例复用，零依赖。
type SSETicketStore struct {
	mu       sync.Mutex
	entries  map[string]ticketEntry
	ttl      time.Duration
	maxItems int
	stopCh   chan struct{}
}

// NewSSETicketStore 构造存储并启动周期清理 goroutine。
// 调用方一般在 main 组装时创建一个实例，传给 auth handler 与 sse handler。
func NewSSETicketStore() *SSETicketStore {
	s := &SSETicketStore{
		entries:  make(map[string]ticketEntry),
		ttl:      30 * time.Second,
		maxItems: 1000,
		stopCh:   make(chan struct{}),
	}
	go s.loopCleanup()
	return s
}

// Stop 停止后台清理 goroutine（测试用）。
func (s *SSETicketStore) Stop() {
	close(s.stopCh)
}

// Issue 生成一次性 ticket 并存入；返回 ticket 字符串。
// 超过 maxItems 时淘汰最早插入的一条（Go map 无顺序，故遍历一次取任意一条删除即可近似 FIFO）。
func (s *SSETicketStore) Issue(userID, role string) string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	ticket := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= s.maxItems {
		for k := range s.entries { // 随机挑一条踢掉，保证上限
			delete(s.entries, k)
			break
		}
	}
	s.entries[ticket] = ticketEntry{
		claim:     SSETicketClaim{UserID: userID, Role: role},
		expiresAt: time.Now().Add(s.ttl).UnixNano(),
	}
	return ticket
}

// Consume 一次性消费 ticket。成功返回对应 claim 和 true；不存在 / 过期返回 false。
func (s *SSETicketStore) Consume(ticket string) (SSETicketClaim, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[ticket]
	if !ok {
		return SSETicketClaim{}, false
	}
	delete(s.entries, ticket)
	if time.Now().UnixNano() > e.expiresAt {
		return SSETicketClaim{}, false
	}
	return e.claim, true
}

func (s *SSETicketStore) loopCleanup() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.mu.Lock()
			now := time.Now().UnixNano()
			for k, e := range s.entries {
				if now > e.expiresAt {
					delete(s.entries, k)
				}
			}
			s.mu.Unlock()
		}
	}
}
