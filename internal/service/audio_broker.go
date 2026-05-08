// Package service
// audio_broker.go 实现 v3 / v3.2 分布式音频"零落盘"中转。
//
// v2（已废弃）：audio worker 上传 FLAC 到服务端 <UploadsDir>/intermediate/<jobId>.flac，
// subtitle worker GET 服务端的文件。服务端要承担磁盘容量。
//
// v3：FLAC 留在 audio worker 本地，服务端只在 subtitle worker 拉取时做实时 broker：
//
//   1. audio worker 完成 FLAC 后调 POST /worker/jobs/:jobId/audio-ready，仅注册元数据
//      （size / sha256 / format / durationMs），任务进 stage=audio_uploaded。
//
//   2. audio worker 维护一个 long-poll：POST /worker/audio-fetch-poll
//      服务端有 fetch / cleanup 任务时立即返回，否则 pollTimeout 秒后 204。
//
//   3. subtitle worker 调 GET /worker/jobs/:jobId/audio：
//      - 服务端找到任务的 owner audio_worker_id
//      - 把 fetch 请求 push 到该 worker 的 long-poll 通道
//      - 用 io.Pipe 准备好 reader（subtitle worker GET response writer）+
//        writer（等 audio worker 调 audio-stream 写）
//      - hold 这个 GET 连接最多 fetchTimeout 秒
//
//   4. audio worker 收到 long-poll 通知后调 POST /worker/jobs/:jobId/audio-stream，
//      流式发送本地 FLAC 文件 body：
//      - 服务端找到对应的 io.Pipe writer
//      - 把 request body io.Copy 到 pipe writer
//      - subtitle worker 那边 GET response 实时收到流
//      - 完成后两端都 close
//
//   5. 任务进 DONE 后，服务端通过同一个 long-poll 通道下发 cleanup 指令；
//      audio worker 删本地 FLAC + 索引项。
//
// v3.2 chunked 上传（绕开 Cloudflare 等 CDN 100 MiB body 上限）：
//   - audio worker 把第 4 步那一次 POST /audio-stream 改成 N 次顺序
//     POST /audio-stream-chunk，每块 ≤ 90 MiB。
//   - 每个 chunk 带 X-Chunk-Index（0-based 严格递增）+ X-Chunk-Last（仅末块）。
//   - broker 在同一条 io.Pipe 上拼接所有 chunk 的字节，subtitle worker GET 端
//     仍然看到一条连续的 chunked 流 + EOF，无感知。
//   - 任意 chunk 乱序 / io 错误 → broker.abortCoupling 立刻 close pipe writer
//     with error，subtitle worker GET 端的 io.Copy 收到错误，HTTP 链路上抛 5xx。
//   - 单流 ReceiveStream 与 chunked ReceiveChunk 互斥：同一个 fetch 不能混用，
//     混用会因 takeCoupling 抢占而失败。客户端按部署环境二选一。
//
// 并发安全：每个 audio worker 一个独立的 fetch 通道（buffered chan）；每个等待中的
// fetch 一个 io.Pipe + chan struct{}（done 信号）。所有共享状态都用 mutex 保护。
// chunked 路径上的 nextChunkIdx / streamStarted 由 fetchCoupling.chunkMu 保护。

package service

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

// 默认 broker 超时（与 distributed-worker.md v3 一致）。
//
// v4：服务端在 NewAudioBrokerWithTimeouts / overrideTimeouts 时可以从 config 注入更大值；
// 这些常量是兜底默认。
const (
	// audioFetchPollDefaultTimeout audio worker long-poll 单次请求最长 hold 时长；
	// 超时返回 204 让 audio worker 立即重新 poll，避免连接被中间设备 idle 断开。
	audioFetchPollDefaultTimeout = 25 * time.Second

	// audioFetchHoldTimeout subtitle worker GET 最长等待时长；超过则返回 503。
	// 这个值 ≥ audioFetchPollDefaultTimeout，确保 audio worker 至少能收到一次 long-poll。
	//
	// v4 默认从 30s 拉到 5min：覆盖大 FLAC 文件慢上传 + 网络抖动恢复。
	// audio worker 收到 fetch 通知后已经在主动推流，5min 足够 ~500MB FLAC 在
	// 1.4MB/s（11Mbps）的窄带下完成。
	audioFetchHoldTimeout = 5 * time.Minute

	// audioStreamFirstByteTimeout audio worker 收到 fetch 通知后必须在此时间内开始上传；
	// 超过视为该 worker 异常，subtitle worker GET 返回 503。
	//
	// v4 默认从 15s 拉到 30s：audio worker 在执行 cleanup / 切换 storage_dir 之类
	// 短任务期间，long-poll → handle_task 的延迟可能 >15s。30s 仍在用户可接受范围内。
	audioStreamFirstByteTimeout = 30 * time.Second

	// audioFetchMaxRetries fetch 首字节超时后的最大重试次数（总尝试 = 1 + maxRetries）。
	//
	// audio worker 可能因临时原因未及时响应（GC、磁盘 IO、网络抖动），直接放弃会
	// 导致任务失败但本地 FLAC 残留。重试给 audio worker 恢复窗口。
	audioFetchMaxRetries = 2
)

// AudioBroker 是 v3 协议的核心中转层。
//
// 生命周期与 SubtitleService 一致；NewSubtitleService 内部初始化一份 broker 单例。
type AudioBroker struct {
	mu sync.Mutex

	// pollChannels：audio worker workerId → 待发给该 worker 的 fetch / cleanup 任务队列
	// 用 buffered chan 防止生产者（subtitle worker GET）阻塞太久。
	pollChannels map[string]chan AudioFetchTask

	// pendingFetches：jobId → 当前等待中的 fetch 协调对象（subtitle worker 在等的那条 GET）
	pendingFetches map[string]*fetchCoupling

	// 配置（可被外部覆盖，便于测试）
	pollTimeout   time.Duration
	holdTimeout   time.Duration
	streamTimeout time.Duration
}

// NewAudioBroker 构造 broker。
func NewAudioBroker() *AudioBroker {
	return &AudioBroker{
		pollChannels:   make(map[string]chan AudioFetchTask),
		pendingFetches: make(map[string]*fetchCoupling),
		pollTimeout:    audioFetchPollDefaultTimeout,
		holdTimeout:    audioFetchHoldTimeout,
		streamTimeout:  audioStreamFirstByteTimeout,
	}
}

// SetTimeouts 在运行期覆写 broker 的超时配置（仅在 NewSubtitleService 启动时调用）。
//
// 用于 v4 把 cfg.AudioFetchHoldSec / cfg.AudioStreamFirstByteSec 注入。
// 0 / 负值表示"保留默认值"，避免 cfg 默认 0 把生产配置打回兜底。
func (b *AudioBroker) SetTimeouts(holdSec, firstByteSec int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if holdSec > 0 {
		b.holdTimeout = time.Duration(holdSec) * time.Second
	}
	if firstByteSec > 0 {
		b.streamTimeout = time.Duration(firstByteSec) * time.Second
	}
}

// AudioFetchTask 是服务端通过 long-poll 下发给 audio worker 的指令。
//
// Action 取值：
//   - "fetch"  ：subtitle worker 在等 jobId 的 FLAC，请上传
//   - "cleanup"：任务已完成，请删除本地 jobId.flac + 索引项
type AudioFetchTask struct {
	Action string `json:"action"`
	JobID  string `json:"jobId"`
}

// fetchCoupling 一对生产者-消费者协调对象。
//
//   - subtitle worker GET handler 创建（通过 RequestFetch），持有 reader 端
//   - audio worker POST audio-stream / audio-stream-chunk handler 拿到 writer 端写入
//   - subtitle worker 端 io.Copy(writer, reader) 把数据流给客户端
//   - done 信号让"开始接收第一字节"超时检测能停在 audio worker 真的开始上传时
//
// v3.2 增加 chunked 上传：单次 fetch 期间多个 ReceiveChunk 调用顺序拼接同一条 pipe。
// streamStarted / nextChunkIdx 由 chunkMu 保护，避免并发 chunk 互踩。
type fetchCoupling struct {
	reader      *io.PipeReader
	writer      *io.PipeWriter
	streamStart chan struct{} // 第一次 Receive(Stream|Chunk) 时关闭，让 RequestFetch 取消 firstByteTimeout
	done        chan struct{} // 整个 fetch 结束（成功或失败）时关闭

	// chunked 上传状态（仅 ReceiveChunk 路径使用，单流 ReceiveStream 路径不读不写）
	chunkMu       sync.Mutex
	nextChunkIdx  int  // 期望的下一个 chunk 序号（0-based）
	streamStarted bool // 第一个 chunk / 单流是否已到（避免重复 close streamStart）
}

// 错误集合。
var (
	ErrAudioOwnerOffline    = errors.New("audio worker offline (no long-poll within timeout)")
	ErrAudioStreamTaken     = errors.New("another fetch is already in progress for this job")
	ErrAudioNoFetcher       = errors.New("no subtitle worker waiting for this job's audio")
	ErrAudioStreamStuck     = errors.New("audio worker accepted fetch task but never started uploading")
	ErrAudioChunkOutOfOrder = errors.New("audio chunk out-of-order")
)

// Poll 是 audio worker long-poll 的实现。
//
// 阻塞最多 timeout（默认 b.pollTimeout）；返回：
//   - (task, nil)：拿到一条 fetch / cleanup 指令
//   - (nil, nil)  ：超时，让 audio worker 立即重新 poll
//
// timeout = 0 用默认值。
func (b *AudioBroker) Poll(workerID string, timeout time.Duration) (*AudioFetchTask, error) {
	if workerID == "" {
		return nil, fmt.Errorf("workerId required")
	}
	if timeout <= 0 {
		timeout = b.pollTimeout
	}
	ch := b.getOrCreateChannel(workerID)
	select {
	case task := <-ch:
		return &task, nil
	case <-time.After(timeout):
		return nil, nil
	}
}

// EnqueueFetch 把一条 fetch / cleanup 指令推到指定 worker 的 long-poll 队列。
//
// 立即返回。如果队列满则丢弃最旧的（broker 模式下宁可丢失一次 cleanup 通知，
// 也不阻塞 subtitle worker GET 链路）。
func (b *AudioBroker) EnqueueFetch(workerID string, task AudioFetchTask) {
	if workerID == "" || task.JobID == "" {
		return
	}
	ch := b.getOrCreateChannel(workerID)
	select {
	case ch <- task:
		// ok
	default:
		// 队列满（buffer 32）：丢弃最旧的，再 push
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- task:
		default:
		}
	}
}

// RequestFetch 是 subtitle worker GET /audio 的处理实现。
//
// 流程：
//  1. 注册一个 fetchCoupling 到 pendingFetches[jobId]
//  2. 通过 EnqueueFetch 通知 audio worker
//  3. 等 audio worker 调 ReceiveStream，开始往 pipe writer 写
//  4. io.Copy 到调用方 w
//  5. 全程超时控制：streamTimeout（第一字节）+ holdTimeout（整体）
//  6. 首字节超时时自动重试（最多 audioFetchMaxRetries 次），给 audio worker 恢复窗口
//
// 调用方负责设置 HTTP Headers（Content-Type 等）后再传 w。
//
// 返回：
//   - nil           ：成功传输完整 body
//   - ErrAudioStreamTaken：已经有另一个 fetch 在进行（同一 jobId）
//   - ErrAudioOwnerOffline：超过 streamTimeout 仍未收到 audio worker 的 stream（重试用尽后）
//   - ctx 错误      ：客户端断开 / 上层超时
func (b *AudioBroker) RequestFetch(jobID, ownerWorkerID string, w io.Writer) error {
	if jobID == "" || ownerWorkerID == "" {
		return fmt.Errorf("jobId and ownerWorkerId required")
	}

	var lastErr error
	for attempt := 0; attempt <= audioFetchMaxRetries; attempt++ {
		err := b.doOneFetchAttempt(jobID, ownerWorkerID, w)
		if err == nil {
			return nil
		}
		lastErr = err

		// 仅首字节超时可重试；其它错误（StreamTaken / ctx 取消 / io 错误）直接返回
		if !errors.Is(err, ErrAudioOwnerOffline) {
			return err
		}

		if attempt < audioFetchMaxRetries {
			log.Printf("[audio-broker] fetch retry %d/%d for job=%s worker=%s (first-byte timeout)",
				attempt+1, audioFetchMaxRetries, jobID, ownerWorkerID)
			b.EnqueueFetch(ownerWorkerID, AudioFetchTask{Action: "fetch", JobID: jobID})
		}
	}
	return lastErr
}

// doOneFetchAttempt 是单次 fetch 尝试（含 coupling 注册、通知、firstByte + hold 超时）。
func (b *AudioBroker) doOneFetchAttempt(jobID, ownerWorkerID string, w io.Writer) error {
	pr, pw := io.Pipe()
	coupling := &fetchCoupling{
		reader:      pr,
		writer:      pw,
		streamStart: make(chan struct{}),
		done:        make(chan struct{}),
	}

	// 1. 占位（同 jobId 不允许并发 fetch）
	if err := b.registerCoupling(jobID, coupling); err != nil {
		return err
	}
	defer b.unregisterCoupling(jobID, coupling)
	defer close(coupling.done)
	defer pr.Close()

	// 2. 通知 audio worker
	b.EnqueueFetch(ownerWorkerID, AudioFetchTask{Action: "fetch", JobID: jobID})

	// 3. 启动 firstByte 看门狗
	startTimer := time.NewTimer(b.streamTimeout)
	defer startTimer.Stop()

	// 等到 audio worker 真的开始上传，再切换到 holdTimeout 整体超时
	select {
	case <-coupling.streamStart:
		// audio worker 已开始写，关掉 firstByte 看门狗
		startTimer.Stop()
	case <-startTimer.C:
		// audio worker 没在 streamTimeout 内开始上传 → 视为离线
		_ = pw.CloseWithError(ErrAudioOwnerOffline)
		return ErrAudioOwnerOffline
	}

	// 4. 真正传输：流式 copy；上限 holdTimeout 兜底（覆盖大文件慢上传）
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(w, pr)
		copyDone <- err
	}()
	select {
	case err := <-copyDone:
		return err
	case <-time.After(b.holdTimeout):
		_ = pw.CloseWithError(fmt.Errorf("hold timeout"))
		return fmt.Errorf("hold timeout after %s", b.holdTimeout)
	}
}

// ReceiveStream 是 audio worker POST /audio-stream 的处理实现（v3 单流模式）。
//
// 把 body 流式 io.Copy 到对应的 fetch coupling 的 pipe writer。subtitle worker 那边
// 会实时拿到。
//
// expectedSize 用于让调用方在 handler 层做 LimitReader（防止巨型 body 占资源），
// broker 内部不强制；返回 (bytesWritten, error)。
//
// 注意：单流模式与 chunk 模式互斥；同一个 fetch 不能混用。
func (b *AudioBroker) ReceiveStream(jobID string, body io.Reader) (int64, error) {
	if jobID == "" {
		return 0, fmt.Errorf("jobId required")
	}
	coupling, ok := b.takeCoupling(jobID)
	if !ok || coupling == nil {
		return 0, ErrAudioNoFetcher
	}

	// 通知 RequestFetch 端"上传开始了"，关掉 firstByte 超时
	// （streamStarted 标志保护双 close panic：chunked 路径同样会 close streamStart）
	coupling.chunkMu.Lock()
	if !coupling.streamStarted {
		close(coupling.streamStart)
		coupling.streamStarted = true
	}
	coupling.chunkMu.Unlock()

	// 流式 copy 到 pipe writer
	written, err := io.Copy(coupling.writer, body)
	if err != nil {
		_ = coupling.writer.CloseWithError(err)
		return written, err
	}
	// 正常结束：close pipe writer 让 subtitle worker 那侧的 io.Copy 看到 EOF
	_ = coupling.writer.Close()
	return written, nil
}

// ReceiveChunk 是 audio worker POST /audio-stream-chunk 的处理实现（v3.2 分块模式）。
//
// 用于 audio worker 通过 Cloudflare 等 CDN 推送时绕开 100MB body 上限：把 FLAC 切成
// 多个 ≤90MiB 的 chunk 顺序上传，broker 在同一条 io.Pipe 上拼接。subtitle worker
// 端 GET 仍然看到一条连续的 chunked 流，无感知。
//
// 协议：
//   - chunkIndex 必须从 0 开始严格递增；乱序则整段中止（subtitle worker GET 收到错误）
//   - 第一个 chunk 时关闭 coupling.streamStart，让 RequestFetch 取消 firstByteTimeout
//   - 写入完成（io.Copy）每块都成功；失败立即 abort
//   - isLast=true 时 close pipe writer (EOF) + 从 pendingFetches 移除
//
// 与 ReceiveStream 互斥：同一个 fetch 不能混用两种协议（混用会因 takeCoupling 抢
// 占而出错）。
//
// 返回 (写入字节数, error)。
//
//   - ErrAudioNoFetcher：没人在等这个 jobId（subtitle worker 已离开 / 任务已完）
//   - ErrAudioChunkOutOfOrder：chunk 序号不匹配，整段已中止
//   - 其它 error：io 错误，整段已中止
func (b *AudioBroker) ReceiveChunk(jobID string, chunkIndex int, isLast bool, body io.Reader) (int64, error) {
	if jobID == "" {
		return 0, fmt.Errorf("jobId required")
	}
	if chunkIndex < 0 {
		return 0, fmt.Errorf("chunkIndex must be >= 0")
	}

	coupling, ok := b.peekCoupling(jobID)
	if !ok || coupling == nil {
		return 0, ErrAudioNoFetcher
	}

	// 序号校验 + 第一块通知 + 状态推进，全在 chunkMu 内原子完成
	coupling.chunkMu.Lock()
	if chunkIndex != coupling.nextChunkIdx {
		expected := coupling.nextChunkIdx
		coupling.chunkMu.Unlock()
		// 整段中止：subtitle worker GET 端会收到这个错误
		b.abortCoupling(jobID, fmt.Errorf("expected chunk %d, got %d: %w",
			expected, chunkIndex, ErrAudioChunkOutOfOrder))
		return 0, fmt.Errorf("expected chunk %d, got %d: %w",
			expected, chunkIndex, ErrAudioChunkOutOfOrder)
	}
	if !coupling.streamStarted {
		close(coupling.streamStart)
		coupling.streamStarted = true
	}
	coupling.nextChunkIdx++
	coupling.chunkMu.Unlock()

	written, err := io.Copy(coupling.writer, body)
	if err != nil {
		b.abortCoupling(jobID, err)
		return written, err
	}

	if isLast {
		// 末块：close pipe writer (EOF) + 从 map 移除（保证只能消费一次）
		// 用 takeCoupling 而不是直接 delete，确保仍是同一个 coupling（防 race）
		if c, ok := b.takeCoupling(jobID); ok && c == coupling {
			_ = c.writer.Close()
		}
	}
	return written, nil
}

// CancelFetch 让 fetch 等待方立即放弃（admin 主动撤销 / 任务被 stale 回收时调用）。
func (b *AudioBroker) CancelFetch(jobID string) {
	if jobID == "" {
		return
	}
	coupling, ok := b.takeCoupling(jobID)
	if !ok || coupling == nil {
		return
	}
	_ = coupling.writer.CloseWithError(fmt.Errorf("fetch cancelled"))
}

// PendingFetchCount 给 admin 监控用：当前等待中的 fetch 数。
func (b *AudioBroker) PendingFetchCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pendingFetches)
}

// OnlineAudioWorkers 给 admin 监控用：当前正在 long-poll 的 audio worker 数。
//
// 注意：这是一个近似值。worker 在 poll 间隙（200ms）会暂时不在 channel 上，
// 但这种 case 下 channel 仍然在 pollChannels map 里（不会被清），所以仍计入。
func (b *AudioBroker) OnlineAudioWorkers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pollChannels)
}

// IsWorkerPolling 检查指定 audio worker 是否曾经发起过 long-poll 且 channel 仍存活。
//
// 用法：admin Alerts 检查"audio_uploaded 任务的 owner audio worker 是否在线"。
// 注意：channel 一旦创建不会主动销毁，所以 worker 可能"曾经在线但现在断开"，
// 这种情况下仍返回 true。配合 last_seen_at 做更精确判断时调用方需要二次校验。
func (b *AudioBroker) IsWorkerPolling(workerID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.pollChannels[workerID]
	return ok
}

// === 内部工具 ===

// pollChannelBuffer 给每个 audio worker 的 fetch 队列分配的 buffer 大小。
// 设为 32 应对短时间内多个 subtitle worker 同时拉同一台 audio worker 上的不同 job。
const pollChannelBuffer = 32

func (b *AudioBroker) getOrCreateChannel(workerID string) chan AudioFetchTask {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.pollChannels[workerID]; ok {
		return ch
	}
	ch := make(chan AudioFetchTask, pollChannelBuffer)
	b.pollChannels[workerID] = ch
	return ch
}

// PurgeWorker 把指定 worker 的 long-poll 通道关闭并从 map 移除。
// 用于 admin 吊销 worker / worker 主动注销时。当前未启用，预留接口。
func (b *AudioBroker) PurgeWorker(workerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.pollChannels[workerID]; ok {
		close(ch)
		delete(b.pollChannels, workerID)
	}
}

func (b *AudioBroker) registerCoupling(jobID string, c *fetchCoupling) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.pendingFetches[jobID]; exists {
		return ErrAudioStreamTaken
	}
	b.pendingFetches[jobID] = c
	return nil
}

func (b *AudioBroker) unregisterCoupling(jobID string, c *fetchCoupling) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.pendingFetches[jobID]; ok && cur == c {
		delete(b.pendingFetches, jobID)
	}
}

// takeCoupling 取走 jobID 对应的 coupling（保证只能被消费一次）。
func (b *AudioBroker) takeCoupling(jobID string) (*fetchCoupling, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.pendingFetches[jobID]
	if !ok {
		return nil, false
	}
	delete(b.pendingFetches, jobID)
	return c, true
}

// peekCoupling 查看 jobID 对应的 coupling 但不取走（chunked 上传期间多次调用）。
//
// 用于 ReceiveChunk：每个 chunk 都需要找到同一个 coupling，只有最后一块或 abort
// 才通过 takeCoupling 移除。
func (b *AudioBroker) peekCoupling(jobID string) (*fetchCoupling, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.pendingFetches[jobID]
	return c, ok
}

// abortCoupling 异常中止整个 fetch：close pipe writer with error + 从 map 移除。
//
// 用于 ReceiveChunk 路径上的乱序 / io 错误，让 RequestFetch 那侧 io.Copy 立即收到
// 该错误并把 5xx 透传给 subtitle worker GET。
func (b *AudioBroker) abortCoupling(jobID string, abortErr error) {
	c, ok := b.takeCoupling(jobID)
	if !ok || c == nil {
		return
	}
	_ = c.writer.CloseWithError(abortErr)
}
