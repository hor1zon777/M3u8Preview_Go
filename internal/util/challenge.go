// Package util
// challenge.go 登录加密协议的一次性 challenge 存储。
//
// 与 sseticket.go 的相似之处：内存 map + TTL + 周期清理 + 单次消费。
// 不同之处：值直接是 32B 随机字节（复用作 HKDF salt），无 userID 概念（登录前用户未认证）。
//
// TTL 60s，maxItems 4096。过载时先淘汰过期项，再按 issuedAt 最旧优先淘汰，
// 避免随机淘汰让攻击者以约 1/N 概率驱逐合法用户刚签发的 challenge 造成登录 DoS。
// 额外：每个 IP 并发 challenge 数不超过 perIPLimit，防单源打爆全表。
package util

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// ErrChallengeStoreBusy 指示当前无法签发 challenge（全局或单 IP 配额满）。
// 调用方应映射为 HTTP 503 / 429，提示客户端稍后重试。
var ErrChallengeStoreBusy = errors.New("challenge store busy")

// challengeEntry 存储 salt 字节、指纹、所属 IP、签发与过期时间。
type challengeEntry struct {
	salt        []byte
	fingerprint string // 设备指纹 SHA-256 hex
	ip          string // 签发时的客户端 IP，用于配额统计
	issuedAt    int64  // unix nano，用于 LRU 淘汰
	expiresAt   int64  // unix nano
}

// ChallengeStore 是进程内 challenge 存储。单实例复用。
type ChallengeStore struct {
	mu          sync.Mutex
	entries     map[string]challengeEntry
	ttl         time.Duration
	maxItems    int
	perIPLimit  int // 单 IP 最大并发 challenge 数
	stopCh      chan struct{}
	stopOnce    sync.Once
}

// NewChallengeStore 构造存储并启动周期清理 goroutine。
func NewChallengeStore() *ChallengeStore {
	s := &ChallengeStore{
		entries:    make(map[string]challengeEntry),
		ttl:        60 * time.Second,
		maxItems:   4096,
		perIPLimit: 16, // 足够覆盖正常用户多标签并发，也挡住单源恶意刷量
		stopCh:     make(chan struct{}),
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

// Issue 生成 32B 随机 challenge，绑定设备指纹 + IP，存入并返回 (id, salt)。
// 任一失败路径返回 ErrChallengeStoreBusy / rand 错误，上层映射为 HTTP 503。
// fingerprint 与 ip 只参与配额与审计，不在这层校验格式。
func (s *ChallengeStore) Issue(fingerprint, ip string) (string, []byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极少但不能吞：否则 salt 全零 = 可预测。
		return "", nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	now := time.Now().UnixNano()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 先做一次懒清理：过期项统一删除
	s.sweepExpiredLocked(now)

	// 单 IP 配额检查
	if ip != "" && s.perIPLimit > 0 {
		count := 0
		for _, e := range s.entries {
			if e.ip == ip {
				count++
				if count >= s.perIPLimit {
					return "", nil, ErrChallengeStoreBusy
				}
			}
		}
	}

	// 全局配额：满时按 issuedAt 最旧优先淘汰一项
	if len(s.entries) >= s.maxItems {
		s.evictOldestLocked()
		// evict 完仍满（极端竞态）：拒绝
		if len(s.entries) >= s.maxItems {
			return "", nil, ErrChallengeStoreBusy
		}
	}

	s.entries[id] = challengeEntry{
		salt:        buf,
		fingerprint: fingerprint,
		ip:          ip,
		issuedAt:    now,
		expiresAt:   time.Now().Add(s.ttl).UnixNano(),
	}
	return id, buf, nil
}

// Consume 一次性消费 challenge。成功返回 (salt, fingerprint, true)；不存在 / 过期返回 false。
func (s *ChallengeStore) Consume(id string) (salt []byte, fingerprint string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, exists := s.entries[id]
	if !exists {
		return nil, "", false
	}
	delete(s.entries, id)
	if time.Now().UnixNano() > e.expiresAt {
		return nil, "", false
	}
	return e.salt, e.fingerprint, true
}

// sweepExpiredLocked 调用方必须持锁。删除所有已过期项。
func (s *ChallengeStore) sweepExpiredLocked(now int64) {
	for k, e := range s.entries {
		if now > e.expiresAt {
			delete(s.entries, k)
		}
	}
}

// evictOldestLocked 调用方必须持锁。删除 issuedAt 最小的一项。
// 相比随机淘汰，能让攻击者"先来"的请求先被驱逐，减少对合法用户的影响。
func (s *ChallengeStore) evictOldestLocked() {
	var oldestKey string
	var oldestAt int64
	first := true
	for k, e := range s.entries {
		if first || e.issuedAt < oldestAt {
			oldestKey = k
			oldestAt = e.issuedAt
			first = false
		}
	}
	if oldestKey != "" {
		delete(s.entries, oldestKey)
	}
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
			s.sweepExpiredLocked(time.Now().UnixNano())
			s.mu.Unlock()
		}
	}
}
