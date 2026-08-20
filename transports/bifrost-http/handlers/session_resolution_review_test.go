package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestReviewWebSocketAuthContextDropsHarnessSession(t *testing.T) {
	ctx, cancel := createBifrostContextFromAuth(nil, &authHeaders{
		headers: map[string][]string{
			"session-id": {"codex-session"},
			"thread-id":  {"codex-thread"},
		},
	})
	defer cancel()
	if got := ctx.Value(schemas.BifrostContextKeySessionID); got != nil {
		t.Fatalf("websocket context unexpectedly retained session ID: %#v", got)
	}
}
