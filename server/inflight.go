package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type inFlightTask struct {
	done  chan struct{}
	lines []string
	err   error
}

// InFlightManager 管理正在进行的后台翻译任务，实现并发请求去重，
// 并在客户端中断连接（如切歌取消请求）时，确保大模型翻译在后台继续执行并落盘至 SQLite 缓存。
type InFlightManager struct {
	mu    sync.Mutex
	tasks map[string]*inFlightTask
	wg    sync.WaitGroup
}

// NewInFlightManager 初始化并发管理器
func NewInFlightManager() *InFlightManager {
	return &InFlightManager{
		tasks: make(map[string]*inFlightTask),
	}
}

// ExecuteOrAttach 获取正在执行的任务或创建新的后台任务。
// - 若已有相同 key 的任务在进行，当前请求直接附着等待结果，避免向大模型重复发送相同歌词消耗 Token；
// - 若无相同任务，启动新的后台 Goroutine 并在脱离当前客户端生命周期的独立 Context 下执行 fn，
//   确保客户端切歌中断连接后，后台翻译依然会完整跑完并写入本地缓存；
// - 调用方在此等待任务完成或客户端断开连接。
func (m *InFlightManager) ExecuteOrAttach(
	ctx context.Context,
	key string,
	timeout time.Duration,
	fn func(bgCtx context.Context) ([]string, error),
) ([]string, error) {
	shortKey := key
	if len(shortKey) > 12 {
		shortKey = shortKey[:12]
	}

	m.mu.Lock()
	task, exists := m.tasks[key]
	if exists {
		m.mu.Unlock()
		log.Printf("[IN-FLIGHT] 命中正在执行中的任务，附着等待: Key=%s...", shortKey)

		select {
		case <-task.done:
			return task.lines, task.err
		case <-ctx.Done():
			log.Printf("[CLIENT CANCELLED] 客户端断开连接/切歌 (附着等待者退出): Key=%s...", shortKey)
			return nil, ctx.Err()
		}
	}

	// 创建新任务
	task = &inFlightTask{
		done: make(chan struct{}),
	}
	m.tasks[key] = task
	m.wg.Add(1)
	m.mu.Unlock()

	// 启动后台独立 Goroutine 执行大模型翻译并落盘
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				task.err = fmt.Errorf("in-flight translation panicked: %v", r)
				log.Printf("[PANIC RECOVER] 后台翻译任务崩溃捕获: %v", r)
			}
			m.mu.Lock()
			delete(m.tasks, key)
			m.mu.Unlock()
			close(task.done)
		}()

		bgCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		lines, err := fn(bgCtx)
		task.lines = lines
		task.err = err
	}()

	// 当前请求等待任务结果或客户端取消
	select {
	case <-task.done:
		return task.lines, task.err
	case <-ctx.Done():
		log.Printf("[CLIENT CANCELLED] 客户端断开连接/切歌 (任务发起者)，大模型翻译已转入后台继续执行并落盘: Key=%s...", shortKey)
		return nil, ctx.Err()
	}
}

// WaitAll 优雅停机时等待所有正在执行的后台翻译任务落盘或超时
func (m *InFlightManager) WaitAll(timeout time.Duration) {
	doneChan := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(doneChan)
	}()

	select {
	case <-doneChan:
	case <-time.After(timeout):
		log.Printf("[WARN] 等待后台翻译任务落盘超时")
	}
}
