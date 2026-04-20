// Package util
// ratelimit_bucket.go 提供一个简单的令牌桶，用于外部封面下载的速率控制。
// 对齐 posterDownloadService 的 TokenBucketRateLimiter：默认 100 token / 60s。
package util

import (
	"context"

	"golang.org/x/time/rate"
)

// TokenBucket 用 golang.org/x/time/rate 实现；每秒补 ratePerSec 个 token，容量 burst。
type TokenBucket struct {
	limiter *rate.Limiter
}

// NewTokenBucket 构造令牌桶。ratePerSec = capacity / windowSec。
func NewTokenBucket(capacity int, ratePerSec float64) *TokenBucket {
	return &TokenBucket{limiter: rate.NewLimiter(rate.Limit(ratePerSec), capacity)}
}

// Wait 阻塞直到拿到 1 个 token 或 ctx 被取消。
func (b *TokenBucket) Wait(ctx context.Context) error {
	return b.limiter.Wait(ctx)
}
