// Package util
// challenge.go 登录加密协议的一次性 challenge 存储。
//
// 与 sseticket.go 的相似之处：内存 map + TTL + 周期清理 + 单次消费。
// 不同之处：值直接是 32B 随机字节（复用作 HKDF salt），无 userID 概念（登录前用户未认证）。
//
// TTL 60s，maxItems 4096（登录并发远低于此）。过载时随机淘汰一条保证上限。
package util

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// challengeEntry 存储 salt 字节与过期时间。
type challengeEntry struct {
	salt      []byte
	expiresAt int64 // unix nano
}

// ChallengeStore 是进程内 challenge 存储。单实例复用。
type ChallengeStore struct {
	mu       sync.Mutex
	entries  map[string]challengeEntry
	ttl      time.Duration
	maxItems int
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewChallengeStore 构造存储并启动周期清理 goroutine。
func NewChallengeStore() *ChallengeStore {
	s := &ChallengeStore{
		entries:  make(map[string]challengeEntry),
		ttl:      60 * time.Second,
		maxItems: 4096,
		stopCh:   make(chan struct{}),
	}
	go s.loopCleanup()
	return s
}

// Stop 停止后台清理 goroutine（测试用）。
func (s *ChallengeStore) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// TTLSeconds 返回 TTL 的整秒值，用于响应 body。
func (s *ChallengeStore) TTLSeconds() int {
	return int(s.ttl.Seconds())
}

// Issue 生成 32B 随机 challenge，存入并返回（id, base64url 编码）。
// id 同时也是 map key 与前端提交时回传的标识；id = base64url(salt)（无需额外随机 id）。
func (s *ChallengeStore) Issue() (id string, salt []byte) {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	id = base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= s.maxItems {
		for k := range s.entries { // map 无序遍历，取一条删除近似 FIFO
			delete(s.entries, k)
			break
		}
	}
	s.entries[id] = challengeEntry{
		salt:      buf,
		expiresAt: time.Now().Add(s.ttl).UnixNano(),
	}
	return id, buf
}

// Consume 一次性消费 challenge。成功返回 salt 字节和 true；不存在 / 过期返回 false。
// id 是 Issue 返回的 base64url 字符串。
func (s *ChallengeStore) Consume(id string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, false
	}
	delete(s.entries, id)
	if time.Now().UnixNano() > e.expiresAt {
		return nil, false
	}
	return e.salt, true
}

func (s *ChallengeStore) loopCleanup() {
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
