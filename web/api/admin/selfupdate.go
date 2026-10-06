package admin

import (
	"fmt"
	"sync"
	"time"
)

// 更新任务的状态。前端按这个顺序展示阶段。
const (
	StageQueued       = "queued"
	StageDownloading  = "downloading"
	StageVerifying    = "verifying"
	StageBackingUp    = "backing_up"
	StageReplacing    = "replacing"
	StageRestarting   = "restarting"
	StageHealthCheck  = "health_check"
	StageSucceeded    = "succeeded"
	StageFailed       = "failed"
	StageRolledBack   = "rolled_back"
	StageUnknown      = "unknown"
)

// UpdateTask 是一次更新或回滚操作的完整状态。
//
// 任务与 HTTP 连接解耦:apply 只负责创建任务并返回 ID,实际工作在后台 goroutine
// 里跑。这样浏览器关闭或 SSE 断开都不会中断更新 —— 更新过程中服务本来就会重启,
// 把任务绑在连接生命周期上是不成立的。
type UpdateTask struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"` // "update" 或 "rollback"
	Stage       string    `json:"stage"`
	Progress    float64   `json:"progress"` // 0..1,仅下载阶段有意义
	Downloaded  int64     `json:"downloaded"`
	Total       int64     `json:"total"`
	FromVersion string    `json:"from_version"`
	ToVersion   string    `json:"to_version"`
	Error       string    `json:"error,omitempty"`
	FailedStage string    `json:"failed_stage,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Recovered 表示失败后是否执行了恢复动作(把备份还原回原位)。
	Recovered bool `json:"recovered"`
}

// IsTerminal 判断任务是否已经结束。
func (t *UpdateTask) IsTerminal() bool {
	switch t.Stage {
	case StageSucceeded, StageFailed, StageRolledBack, StageUnknown:
		return true
	}
	return false
}

// UpdateTaskManager 持有当前任务、全局互斥锁和进度订阅者。
//
// 同一时刻只允许一个任务在跑。并发请求直接拒绝而不是排队 —— 更新这种操作
// 排队没有意义,而且会让"我点了更新但没反应"变得更难诊断。
type UpdateTaskManager struct {
	mu      sync.Mutex
	current *UpdateTask
	// watchers 是当前订阅进度的通道。任务重启时这些连接会自然断开,
	// 所以这里只需要在广播时跳过已满/已关的通道,不需要保活。
	watchers map[chan UpdateTask]struct{}
}

var updateTasks = &UpdateTaskManager{
	watchers: make(map[chan UpdateTask]struct{}),
}

// GetUpdateTaskManager 返回全局任务管理器。
func GetUpdateTaskManager() *UpdateTaskManager { return updateTasks }

// Begin 尝试开始一个新任务。已有任务在跑时返回 nil 和错误。
func (m *UpdateTaskManager) Begin(kind, fromVersion, toVersion string) (*UpdateTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current != nil && !m.current.IsTerminal() {
		return nil, fmt.Errorf("another %s task is already running (stage: %s)", m.current.Kind, m.current.Stage)
	}

	task := &UpdateTask{
		ID:          newUpdateTaskID(),
		Kind:        kind,
		Stage:       StageQueued,
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		StartedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	m.current = task
	return task, nil
}

// SetStage 推进任务阶段并通知订阅者。
func (m *UpdateTaskManager) SetStage(id, stage string) {
	m.mu.Lock()
	if m.current == nil || m.current.ID != id {
		m.mu.Unlock()
		return
	}
	m.current.Stage = stage
	m.current.UpdatedAt = time.Now().UTC()
	snapshot := *m.current
	m.mu.Unlock()

	m.broadcast(snapshot)
}

// SetProgress 更新下载进度并通知订阅者。
func (m *UpdateTaskManager) SetProgress(id string, downloaded, total int64) {
	m.mu.Lock()
	if m.current == nil || m.current.ID != id {
		m.mu.Unlock()
		return
	}
	m.current.Stage = StageDownloading
	m.current.Downloaded = downloaded
	m.current.Total = total
	if total > 0 {
		m.current.Progress = float64(downloaded) / float64(total)
	}
	m.current.UpdatedAt = time.Now().UTC()
	snapshot := *m.current
	m.mu.Unlock()

	m.broadcast(snapshot)
}

// Fail 把任务标记为失败。
func (m *UpdateTaskManager) Fail(id, stage string, err error) {
	m.mu.Lock()
	if m.current == nil || m.current.ID != id {
		m.mu.Unlock()
		return
	}
	m.current.Stage = StageFailed
	m.current.FailedStage = stage
	m.current.Error = err.Error()
	m.current.UpdatedAt = time.Now().UTC()
	snapshot := *m.current
	m.mu.Unlock()

	m.broadcast(snapshot)
}

// Finish 标记任务成功结束。successStage 由调用方决定(更新成功用 succeeded,
// 回滚成功用 rolled_back)。
func (m *UpdateTaskManager) Finish(id, successStage string, recovered bool) {
	m.mu.Lock()
	if m.current == nil || m.current.ID != id {
		m.mu.Unlock()
		return
	}
	m.current.Stage = successStage
	m.current.Recovered = recovered
	m.current.UpdatedAt = time.Now().UTC()
	snapshot := *m.current
	m.mu.Unlock()

	m.broadcast(snapshot)
}

// MarkUnknown 用于重启后无法确认结果的场景。
//
// 这里刻意不标记为 failed:进程已经重启,我们确实不知道新版本是否起来了。
// 直接断言"更新失败"会误导用户去回滚一个可能其实正常的版本。
func (m *UpdateTaskManager) MarkUnknown(id, reason string) {
	m.mu.Lock()
	if m.current == nil || m.current.ID != id {
		m.mu.Unlock()
		return
	}
	m.current.Stage = StageUnknown
	m.current.Error = reason
	m.current.UpdatedAt = time.Now().UTC()
	snapshot := *m.current
	m.mu.Unlock()

	m.broadcast(snapshot)
}

// Snapshot 返回当前任务的副本。
func (m *UpdateTaskManager) Snapshot() *UpdateTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	copy := *m.current
	return &copy
}

// Subscribe 注册一个进度订阅者,返回通道和取消函数。
func (m *UpdateTaskManager) Subscribe() (chan UpdateTask, func()) {
	ch := make(chan UpdateTask, 16)

	m.mu.Lock()
	m.watchers[ch] = struct{}{}
	current := m.current
	m.mu.Unlock()

	// 立刻推送一次当前状态,避免订阅者要等到下一次阶段变化才有数据。
	if current != nil {
		select {
		case ch <- *current:
		default:
		}
	}

	cancel := func() {
		m.mu.Lock()
		if _, ok := m.watchers[ch]; ok {
			delete(m.watchers, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
	return ch, cancel
}

// broadcast 把任务状态推给所有订阅者。通道满了就跳过该订阅者 ——
// 进度事件是可丢弃的,积压只会让慢客户端拖住整个更新流程。
func (m *UpdateTaskManager) broadcast(task UpdateTask) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.watchers {
		select {
		case ch <- task:
		default:
		}
	}
}
