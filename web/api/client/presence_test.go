package client

import (
	"testing"

	agent_runtime "github.com/aomtest/komari-slim-server/web/agent"
)

func TestPostPresenceExpiredIgnoresStaleGeneration(t *testing.T) {
	const uuid = "post-presence-generation-test"

	postPresenceMu.Lock()
	previous := postPresenceStates
	postPresenceStates = make(map[string]*postPresenceEntry)
	postPresenceMu.Unlock()
	t.Cleanup(func() {
		postPresenceMu.Lock()
		for _, entry := range postPresenceStates {
			entry.timer.Stop()
		}
		postPresenceStates = previous
		postPresenceMu.Unlock()
		agent_runtime.ClearV2Client(uuid)
		agent_runtime.DropV2EventQueue(uuid)
	})

	refreshPostPresence(uuid)
	postPresenceMu.Lock()
	entry := postPresenceStates[uuid]
	connID, generation := entry.connID, entry.generation
	postPresenceMu.Unlock()

	refreshPostPresence(uuid)
	postPresenceExpired(uuid, connID, generation)

	if !agent_runtime.IsV2Client(uuid) {
		t.Fatal("stale expiry must not clear the current v2 client marker")
	}
	postPresenceMu.Lock()
	_, exists := postPresenceStates[uuid]
	postPresenceMu.Unlock()
	if !exists {
		t.Fatal("stale expiry must not remove the current presence state")
	}
}
