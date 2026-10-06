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
	defer postPresenceMu.Unlock()

	e, ok := postPresenceStates[uuid]
	if !ok || e.connID != connID || e.generation != gen {
		return
	}
	delete(postPresenceStates, uuid)

	agent_runtime.SetPresence(uuid, connID, false)
	// 保持 postPresenceMu 直到所有清理完成,避免新一代 POST presence 在这里
	// 删除状态后、清理 v2 标记和事件队列前插入。
	agent_runtime.ClearV2Client(uuid)
	agent_runtime.DropV2EventQueue(uuid)
	notifier.OfflineNotification(uuid, connID)
}
