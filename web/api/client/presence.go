package client

import (
	"sync"
	"time"

	"github.com/aomtest/komari-slim-server/utils/notifier"
	agent_runtime "github.com/aomtest/komari-slim-server/web/agent"
)

const (
	// 如果超过这个时间没有收到任何消息，则认为连接已死
	readWait        = 11 * time.Second
	postPresenceTTL = 35 * time.Second
)

type postPresenceEntry struct {
	connID     int64
	timer      *time.Timer
	generation uint64
}

var (
	postPresenceMu     sync.Mutex
	postPresenceStates = make(map[string]*postPresenceEntry)
)

// refreshPostPresence 管理 HTTP POST 上报者的在线/离线状态。
func refreshPostPresence(uuid string) {
	postPresenceMu.Lock()
	defer postPresenceMu.Unlock()

	if entry, exists := postPresenceStates[uuid]; exists {
		entry.generation++
		entry.timer.Stop()
		gen := entry.generation
		entry.timer = time.AfterFunc(postPresenceTTL, func() {
			postPresenceExpired(uuid, entry.connID, gen)
		})
		agent_runtime.KeepAlivePresence(uuid, entry.connID, postPresenceTTL)
		return
	}

	connID := time.Now().UnixNano()
	agent_runtime.KeepAlivePresence(uuid, connID, postPresenceTTL)
	agent_runtime.MarkV2Client(uuid)
	go notifier.OnlineNotification(uuid, connID)

	defaultGeneration := uint64(0)
	entry := &postPresenceEntry{connID: connID, generation: defaultGeneration}
	entry.timer = time.AfterFunc(postPresenceTTL, func() {
		postPresenceExpired(uuid, connID, defaultGeneration)
	})
	postPresenceStates[uuid] = entry
}

func postPresenceExpired(uuid string, connID int64, gen uint64) {
	postPresenceMu.Lock()
	e, ok := postPresenceStates[uuid]
	if !ok || e.connID != connID || e.generation != gen {
		postPresenceMu.Unlock()
		return
	}
	delete(postPresenceStates, uuid)
	postPresenceMu.Unlock()

	agent_runtime.SetPresence(uuid, connID, false)
	// 上面已通过 generation 校验确认这是"当前这一代"的过期(agent 重连会让
	// generation 自增,旧定时器到这里会直接 return),因此可以安全地一并清掉
	// v2 标记与事件队列。不清 v2 标记的话 IsAgentOnline 会永久返回 true。
	agent_runtime.ClearV2Client(uuid)
	agent_runtime.DropV2EventQueue(uuid)
	notifier.OfflineNotification(uuid, connID)
}
