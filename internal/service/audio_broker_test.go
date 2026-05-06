// Package service
// audio_broker_test.go 覆盖 v3 / v3.2 broker 的核心路径。
//
// 重点测试：
//  1. 单流 ReceiveStream：subtitle worker GET 端实时收到字节流 + EOF
//  2. 分块 ReceiveChunk 顺序：多个 chunk 在 pipe 上拼接成完整字节流
//  3. ReceiveChunk 乱序：整段中止，subtitle worker GET 收到错误
//  4. ReceiveChunk 在没有等待方时返回 ErrAudioNoFetcher
//  5. RequestFetch 在 audio worker 不上传时按 streamTimeout 返回 ErrAudioOwnerOffline
package service

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// runFetchInBg 启动一个后台 goroutine 模拟 subtitle worker GET，把 broker pipe
// 转出的字节写到 buf，并把 RequestFetch 的返回值送到 errCh。
func runFetchInBg(b *AudioBroker, jobID, ownerWorkerID string) (*bytes.Buffer, chan error) {
	buf := &bytes.Buffer{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.RequestFetch(jobID, ownerWorkerID, buf)
	}()
	// 给 RequestFetch 一点时间登记 coupling
	time.Sleep(20 * time.Millisecond)
	return buf, errCh
}

// drainPoll 消费 audio worker long-poll 通道里的 fetch 通知，避免 buffered chan 满。
func drainPoll(b *AudioBroker, workerID string) {
	go func() {
		for {
			task, err := b.Poll(workerID, 200*time.Millisecond)
			if err != nil || task == nil {
				return
			}
		}
	}()
}

// TestReceiveStream_HappyPath 单流上传：audio worker 一次 POST，subtitle worker 端
// 收到完整字节 + EOF。
func TestReceiveStream_HappyPath(t *testing.T) {
	b := NewAudioBroker()
	const jobID = "job-1"
	const ownerID = "audio-worker-1"
	drainPoll(b, ownerID)

	payload := bytes.Repeat([]byte("FLAC"), 8192) // 32 KiB
	buf, errCh := runFetchInBg(b, jobID, ownerID)

	written, err := b.ReceiveStream(jobID, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("ReceiveStream: %v", err)
	}
	if int(written) != len(payload) {
		t.Fatalf("written %d != payload %d", written, len(payload))
	}

	select {
	case ferr := <-errCh:
		if ferr != nil {
			t.Fatalf("RequestFetch returned err: %v", ferr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestFetch did not return in time")
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatalf("subtitle worker buf %d bytes != payload %d", buf.Len(), len(payload))
	}
}

// TestReceiveChunk_HappyPath 分块上传 3 个 chunk，subtitle worker 端拼接得到完整 payload。
func TestReceiveChunk_HappyPath(t *testing.T) {
	b := NewAudioBroker()
	const jobID = "job-chunk-1"
	const ownerID = "audio-worker-1"
	drainPoll(b, ownerID)

	// 模拟 250 KiB FLAC，按 100 KiB / 100 KiB / 50 KiB 切片
	payload := make([]byte, 250*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	chunks := [][]byte{
		payload[:100*1024],
		payload[100*1024 : 200*1024],
		payload[200*1024:],
	}

	buf, errCh := runFetchInBg(b, jobID, ownerID)

	for i, c := range chunks {
		isLast := i == len(chunks)-1
		written, err := b.ReceiveChunk(jobID, i, isLast, bytes.NewReader(c))
		if err != nil {
			t.Fatalf("ReceiveChunk %d: %v", i, err)
		}
		if int(written) != len(c) {
			t.Fatalf("chunk %d written %d != len %d", i, written, len(c))
		}
	}

	select {
	case ferr := <-errCh:
		if ferr != nil {
			t.Fatalf("RequestFetch returned err: %v", ferr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestFetch did not return in time")
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatalf("reassembled buf len=%d != payload len=%d", buf.Len(), len(payload))
	}
}

// TestReceiveChunk_OutOfOrder 第 1 块来时 chunkIndex=2 → 整段中止；subtitle worker
// GET 端收到错误（io.Copy 到 buf 的过程会因为 PipeReader CloseWithError 提早返回）。
func TestReceiveChunk_OutOfOrder(t *testing.T) {
	b := NewAudioBroker()
	const jobID = "job-chunk-oop"
	const ownerID = "audio-worker-1"
	drainPoll(b, ownerID)

	_, errCh := runFetchInBg(b, jobID, ownerID)

	// chunk 0 正常
	if _, err := b.ReceiveChunk(jobID, 0, false, strings.NewReader("hello")); err != nil {
		t.Fatalf("ReceiveChunk 0: %v", err)
	}
	// chunk 2 跳号（应该是 1）
	_, err := b.ReceiveChunk(jobID, 2, true, strings.NewReader("world"))
	if !errors.Is(err, ErrAudioChunkOutOfOrder) {
		t.Fatalf("expected ErrAudioChunkOutOfOrder, got %v", err)
	}

	// RequestFetch 那侧应当收到错误
	select {
	case ferr := <-errCh:
		if ferr == nil {
			t.Fatal("expected RequestFetch to return non-nil error after abort")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestFetch did not return after abort")
	}

	// coupling 应该已经被 abort 移除，再 chunk 1 进来会报 ErrAudioNoFetcher
	_, err = b.ReceiveChunk(jobID, 1, false, strings.NewReader("anything"))
	if !errors.Is(err, ErrAudioNoFetcher) {
		t.Fatalf("expected ErrAudioNoFetcher after abort, got %v", err)
	}
}

// TestReceiveChunk_NoFetcher 没有 subtitle worker 在等的情况下，chunk 上传立即报错。
func TestReceiveChunk_NoFetcher(t *testing.T) {
	b := NewAudioBroker()
	_, err := b.ReceiveChunk("ghost-job", 0, true, strings.NewReader("data"))
	if !errors.Is(err, ErrAudioNoFetcher) {
		t.Fatalf("expected ErrAudioNoFetcher, got %v", err)
	}
}

// TestRequestFetch_OwnerOffline audio worker 不上传 → 超过 streamTimeout 返回
// ErrAudioOwnerOffline。
func TestRequestFetch_OwnerOffline(t *testing.T) {
	b := NewAudioBroker()
	// 缩短超时让测试快跑
	b.streamTimeout = 80 * time.Millisecond
	b.holdTimeout = 200 * time.Millisecond

	const jobID = "job-offline"
	const ownerID = "audio-worker-offline"
	// 不消费 poll 通道也不上传，直接等 streamTimeout 触发

	buf := &bytes.Buffer{}
	err := b.RequestFetch(jobID, ownerID, buf)
	if !errors.Is(err, ErrAudioOwnerOffline) {
		t.Fatalf("expected ErrAudioOwnerOffline, got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("buf should be empty, got %d bytes", buf.Len())
	}
}

// TestReceiveChunk_AbortOnIOError 中途 io.Copy 失败时 coupling 被 abort，下一块进来收到 ErrAudioNoFetcher。
func TestReceiveChunk_AbortOnIOError(t *testing.T) {
	b := NewAudioBroker()
	const jobID = "job-io-err"
	const ownerID = "audio-worker-1"
	drainPoll(b, ownerID)

	_, errCh := runFetchInBg(b, jobID, ownerID)

	// chunk 0 正常发送
	if _, err := b.ReceiveChunk(jobID, 0, false, strings.NewReader("first")); err != nil {
		t.Fatalf("ReceiveChunk 0: %v", err)
	}

	// chunk 1 用一个会立即返回 io.ErrUnexpectedEOF 的 reader
	_, err := b.ReceiveChunk(jobID, 1, false, &errReader{})
	if err == nil {
		t.Fatal("expected error from broken reader")
	}

	// RequestFetch 那侧应当收到错误
	select {
	case <-errCh:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("RequestFetch did not return after io abort")
	}

	// 后续 chunk 应当看到 coupling 已不存在
	_, err = b.ReceiveChunk(jobID, 2, true, strings.NewReader("late"))
	if !errors.Is(err, ErrAudioNoFetcher) {
		t.Fatalf("expected ErrAudioNoFetcher after abort, got %v", err)
	}
}

// errReader 立即返回 io.ErrUnexpectedEOF，用来模拟 chunk body 损坏。
type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
