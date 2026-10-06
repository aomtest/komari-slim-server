package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	v2 "github.com/aomtest/komari-slim-server/protocol/v2"
)

const (
	v2EventQueueLimit = 128
	v2EventTTL        = 5 * time.Minute
	v2PingEventTTL    = 3 * time.Second
	// File operations may include a remote read/search with a 90 second
	// deadline, so queued file commands need a little headroom while an agent
	// reconnects.
	v2FileEventTTL = 2 * time.Minute
)

type v2EventQueue struct {
	events []v2.Event
	signal chan struct{}
}

var (
	v2EventMu     sync.Mutex
	v2EventQueues = make(map[string]*v2EventQueue)
)

func getV2EventQueueLocked(uuid string) *v2EventQueue {
	q := v2EventQueues[uuid]
	if q == nil {
		q = &v2EventQueue{signal: make(chan struct{})}
		v2EventQueues[uuid] = q
	}
	return q
}

// DropV2EventQueue 回收 uuid 的事件队列。
//
// 队列在首次取用时就会由 getV2EventQueueLocked 创建,而此前全文件没有任何
// 删除路径 —— 每个调用过 TakeV2Events / WaitV2Events / EnqueueV2Event 的 uuid
// 都会在 map 里永久留下一个条目。单个条目不大,但"创建后删除客户端"这种
// 反复操作会让它只增不减。
func DropV2EventQueue(uuid string) {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	delete(v2EventQueues, uuid)
}

func DispatchV2Event(uuid, method string, params any) bool {
	if conn := GetConnectedClients()[uuid]; conn != nil {
		payload := v2.Request{JSONRPC: v2.Version, Method: method, Params: params}
		if conn.WriteJSON(payload) == nil {
			return true
		}
	}
	if !IsV2Client(uuid) {
		return false
	}
	EnqueueV2Event(uuid, method, params)
	return true
}

func DispatchPing(uuid string, params v2.PingParams) bool {
	if conn := GetConnectedClients()[uuid]; conn != nil {
		payload := v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPing, Params: params}
		if conn.WriteJSON(payload) == nil {
			return true
		}
	}
	if !IsV2Client(uuid) {
		return false
	}
	EnqueueV2Event(uuid, v2.MethodAgentPing, params)
	return true
}

func IsAgentOnline(uuid string) bool {
	if GetConnectedClients()[uuid] != nil {
		return true
	}
	return IsV2Client(uuid)
}

func EnqueueV2Event(uuid, method string, params any) v2.Event {
	now := time.Now().UTC()
	ttl := v2EventTTL
	if method == v2.MethodAgentPing {
		ttl = v2PingEventTTL
	} else if method == v2.MethodAgentFile {
		ttl = v2FileEventTTL
	}
	event := v2.Event{
		ID:        newV2EventID(),
		Method:    method,
		Params:    params,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}

	v2EventMu.Lock()
	q := getV2EventQueueLocked(uuid)
	pruneExpiredV2EventsLocked(q)
	coalesceV2EventLocked(q, event)
	q.events = append(q.events, event)
	if len(q.events) > v2EventQueueLimit {
		q.events = q.events[len(q.events)-v2EventQueueLimit:]
	}
	close(q.signal)
	q.signal = make(chan struct{})
	v2EventMu.Unlock()

	return event
}

func newV2EventID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func coalesceV2EventLocked(q *v2EventQueue, event v2.Event) {
	key := v2EventCoalesceKey(event)
	if key == "" {
		return
	}
	filtered := q.events[:0]
	for _, existing := range q.events {
		if v2EventCoalesceKey(existing) != key {
			filtered = append(filtered, existing)
		}
	}
	q.events = filtered
}

func v2EventCoalesceKey(event v2.Event) string {
	if event.Method == v2.MethodAgentTerminal {
		var params v2.TerminalRequestParams
		if err := bindV2EventParams(event.Params, &params); err == nil && params.RequestID != "" {
			return event.Method + ":" + params.RequestID
		}
		return ""
	}
	if event.Method != v2.MethodAgentPing {
		return ""
	}
	var params v2.PingParams
	if err := bindV2EventParams(event.Params, &params); err != nil || params.TaskID == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", event.Method, params.TaskID)
}

func bindV2EventParams(raw any, target any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func ackV2EventsLocked(q *v2EventQueue, ackIDs []string) {
	if len(ackIDs) == 0 || len(q.events) == 0 {
		return
	}
	acked := make(map[string]struct{}, len(ackIDs))
	for _, id := range ackIDs {
		acked[id] = struct{}{}
	}
	filtered := q.events[:0]
	for _, event := range q.events {
		if _, ok := acked[event.ID]; !ok {
			filtered = append(filtered, event)
		}
	}
	q.events = filtered
}

func pruneExpiredV2EventsLocked(q *v2EventQueue) {
	if len(q.events) == 0 {
		return
	}
	now := time.Now().UTC()
	filtered := q.events[:0]
	for _, event := range q.events {
		if event.ExpiresAt.IsZero() {
			filtered = append(filtered, event)
			continue
		}
		if event.ExpiresAt.After(now) {
			filtered = append(filtered, event)
		}
	}
	q.events = filtered
}

func TakeV2Events(uuid string, ackIDs []string, limit int) []v2.Event {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()

	q := getV2EventQueueLocked(uuid)
	ackV2EventsLocked(q, ackIDs)
	pruneExpiredV2EventsLocked(q)
	events := takeV2EventsLocked(q, limit)
	// 取空即回收,避免离线客户端的空队列长期占位。
	// WaitV2Events 超时后也会走到这里,所以反复长轮询是"创建-回收"循环,不会累积。
	if len(q.events) == 0 {
		delete(v2EventQueues, uuid)
	}
	return events
}

func AckV2Events(uuid string, ackIDs []string) {
	if len(ackIDs) == 0 {
		return
	}
	v2EventMu.Lock()
	defer v2EventMu.Unlock()

	q := v2EventQueues[uuid]
	if q == nil {
		return
	}
	ackV2EventsLocked(q, ackIDs)
}

func takeV2EventsLocked(q *v2EventQueue, limit int) []v2.Event {
	if limit <= 0 || limit > len(q.events) {
		limit = len(q.events)
	}
	events := make([]v2.Event, limit)
	copy(events, q.events[:limit])
	return events
}

func WaitV2Events(uuid string, ackIDs []string, timeout time.Duration) []v2.Event {
	v2EventMu.Lock()
	q := getV2EventQueueLocked(uuid)
	ackV2EventsLocked(q, ackIDs)
	pruneExpiredV2EventsLocked(q)
	events := takeV2EventsLocked(q, v2EventQueueLimit)
	if len(events) > 0 || timeout <= 0 {
		v2EventMu.Unlock()
		return events
	}
	signal := q.signal
	v2EventMu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
	}
	return TakeV2Events(uuid, nil, v2EventQueueLimit)
}
