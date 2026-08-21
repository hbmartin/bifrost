package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestWebSocketAuthContextRetainsResolvedSessionID(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "harness priority",
			headers: map[string]string{"thread-id": "codex-thread", "session-id": "codex-session"},
			want:    "codex-session",
		},
		{
			name:    "explicit wins",
			headers: map[string]string{"session-id": "codex-session", "x-bf-session-id": "explicit"},
			want:    "explicit",
		},
		{
			name: "oversized explicit fails closed",
			headers: map[string]string{
				"x-bf-session-id": strings.Repeat("x", schemas.MaxSessionIDLength+1),
				"session-id":      "codex-session",
			},
			want: "",
		},
		{
			name: "oversized harness falls through",
			headers: map[string]string{
				"x-claude-code-session-id": strings.Repeat("x", schemas.MaxSessionIDLength+1),
				"session-id":               "codex-session",
			},
			want: "codex-session",
		},
		{
			name:    "multibyte boundary counts runes",
			headers: map[string]string{"x-bf-session-id": strings.Repeat("界", schemas.MaxSessionIDLength)},
			want:    strings.Repeat("界", schemas.MaxSessionIDLength),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestCtx := &fasthttp.RequestCtx{}
			for name, value := range tt.headers {
				requestCtx.Request.Header.Set(name, value)
			}

			auth := captureAuthHeaders(requestCtx)
			if auth.sessionID != tt.want {
				t.Fatalf("captured session ID = %q, want %q", auth.sessionID, tt.want)
			}

			ctx, cancel := createBifrostContextFromAuth(nil, auth)
			defer cancel()
			got, _ := ctx.Value(schemas.BifrostContextKeySessionID).(string)
			if got != tt.want {
				t.Fatalf("websocket context session ID = %q, want %q", got, tt.want)
			}
		})
	}
}
