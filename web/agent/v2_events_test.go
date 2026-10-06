package agent

import (
	"testing"
	"time"

	v2 "github.com/aomtest/komari-slim-server/protocol/v2"
)

func resetV2EventQueues(t *testing.T) {
	t.Helper()
	v2EventMu.Lock()
	previous := v2EventQueues
	v2EventQueues = make(map[string]*v2EventQueue)
	v2EventMu.Unlock()
	t.Cleanup(func() {
		v2EventMu.Lock()
		v2EventQueues = previous
		v2EventMu.Unlock()
	})
}

func TestAckV2EventsReclaimsEmptyQueue(t *testing.T) {
	resetV2EventQueues(t)

	event := EnqueueV2Event("node-a", v2.MethodAgentTerminal, map[string]any{"request_id": "req-1"})
	AckV2Events("node-a", []string{event.ID})

	v2EventMu.Lock()
	_, exists := v2EventQueues["node-a"]
	v2EventMu.Unlock()
	if exists {
		t.Fatal("acknowledging the last event must reclaim the empty queue")
	}
}

func TestWaitV2EventsNonBlockingReclaimsEmptyQueue(t *testing.T) {
	resetV2EventQueues(t)

	if events := WaitV2Events("node-a", nil, 0); len(events) != 0 {
		t.Fatalf("expected no events, got %#v", events)
	}

	v2EventMu.Lock()
	_, exists := v2EventQueues["node-a"]
	v2EventMu.Unlock()
	if exists {
		t.Fatal("non-blocking wait on an empty queue must reclaim it")
	}

	EnqueueV2Event("node-a", v2.MethodAgentPing, v2.PingParams{TaskID: 1})
	if events := WaitV2Events("node-a", nil, time.Nanosecond); len(events) != 1 {
		t.Fatalf("expected queued event after recreating queue, got %#v", events)
	}
}
