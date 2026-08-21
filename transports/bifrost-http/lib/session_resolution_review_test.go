package lib

import (
	"bufio"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

var reviewSessionIDSink string

func reviewHeaderSet() *fasthttp.RequestHeader {
	h := &fasthttp.RequestHeader{}
	for i := 0; i < 12; i++ {
		h.Set("x-review-header-"+strings.Repeat("a", i+1), "value")
	}
	return h
}

// reviewResolveSessionIDWithPeek is a behaviorally equivalent optimization
// candidate for ResolveSessionIDFromRequest. PeekAll is intentional: production
// header iteration keeps the final value when a caller sends a duplicate field,
// while Peek alone returns the first.
func reviewResolveSessionIDWithPeek(h *fasthttp.RequestHeader) string {
	if h == nil {
		return ""
	}
	if values := h.PeekAll("x-bf-session-id"); len(values) > 0 {
		raw := string(values[len(values)-1])
		if strings.TrimSpace(raw) != "" {
			sessionID, ok := schemas.NormalizeSessionID(raw)
			if !ok {
				return ""
			}
			return sessionID
		}
	}
	for _, name := range schemas.HarnessSessionHeaders {
		values := h.PeekAll(name)
		if len(values) == 0 {
			continue
		}
		raw := string(values[len(values)-1])
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if sessionID, ok := schemas.NormalizeSessionID(raw); ok {
			return sessionID
		}
	}
	return ""
}

func TestReviewPriorityPeekMatchesProduction(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fasthttp.RequestHeader)
		want  string
	}{
		{name: "no session", setup: func(*fasthttp.RequestHeader) {}, want: ""},
		{
			name:  "explicit value is trimmed",
			setup: func(h *fasthttp.RequestHeader) { h.Set("x-bf-session-id", "  explicit  ") },
			want:  "explicit",
		},
		{
			name: "explicit beats harness",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("session-id", "harness")
				h.Set("x-bf-session-id", "explicit")
			},
			want: "explicit",
		},
		{
			name: "blank explicit falls through",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("x-bf-session-id", "  ")
				h.Set("session-id", "harness")
			},
			want: "harness",
		},
		{
			name: "oversized explicit fails closed",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("x-bf-session-id", strings.Repeat("x", schemas.MaxSessionIDLength+1))
				h.Set("session-id", "harness")
			},
			want: "",
		},
		{
			name: "first harness priority wins",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("thread-id", "thread")
				h.Set("x-claude-code-session-id", "claude")
			},
			want: "claude",
		},
		{
			name:  "last harness priority is accepted",
			setup: func(h *fasthttp.RequestHeader) { h.Set("conversation_id", "conversation") },
			want:  "conversation",
		},
		{
			name: "oversized harness falls through",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("x-claude-code-session-id", strings.Repeat("x", schemas.MaxSessionIDLength+1))
				h.Set("session-id", "harness")
			},
			want: "harness",
		},
		{
			name: "unicode boundary counts runes",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("x-bf-session-id", strings.Repeat("界", schemas.MaxSessionIDLength))
			},
			want: strings.Repeat("界", schemas.MaxSessionIDLength),
		},
		{
			name: "duplicate explicit uses final value",
			setup: func(h *fasthttp.RequestHeader) {
				h.Add("x-bf-session-id", "first")
				h.Add("x-bf-session-id", "second")
			},
			want: "second",
		},
		{
			name: "duplicate harness uses final value",
			setup: func(h *fasthttp.RequestHeader) {
				h.Add("session-id", "first")
				h.Add("session-id", "second")
			},
			want: "second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := reviewHeaderSet()
			tt.setup(h)
			if got := ResolveSessionIDFromRequest(h); got != tt.want {
				t.Fatalf("production resolver = %q, want %q", got, tt.want)
			}
			if got := reviewResolveSessionIDWithPeek(h); got != tt.want {
				t.Fatalf("priority-Peek resolver = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReviewProductionResolverParsesRawUnderscoreHeader(t *testing.T) {
	var h fasthttp.RequestHeader
	raw := "POST /v1/chat/completions HTTP/1.1\r\nHost: localhost\r\nsession_id: raw-session\r\nContent-Length: 0\r\n\r\n"
	if err := h.Read(bufio.NewReader(strings.NewReader(raw))); err != nil {
		t.Fatal(err)
	}
	if got := ResolveSessionIDFromRequest(&h); got != "raw-session" {
		t.Fatalf("production resolver = %q, want raw-session", got)
	}
	if got := reviewResolveSessionIDWithPeek(&h); got != "raw-session" {
		t.Fatalf("priority-Peek resolver = %q, want raw-session", got)
	}
}

func BenchmarkReviewSessionResolvers(b *testing.B) {
	workloads := []struct {
		name  string
		setup func(*fasthttp.RequestHeader)
	}{
		{name: "no_session", setup: func(*fasthttp.RequestHeader) {}},
		{name: "explicit_hit", setup: func(h *fasthttp.RequestHeader) { h.Set("x-bf-session-id", "explicit") }},
		{name: "first_harness_hit", setup: func(h *fasthttp.RequestHeader) { h.Set("x-claude-code-session-id", "claude") }},
		{name: "final_harness_hit", setup: func(h *fasthttp.RequestHeader) { h.Set("conversation_id", "conversation") }},
		{name: "underscore_hit", setup: func(h *fasthttp.RequestHeader) { h.Set("session_id", "codex") }},
		{
			name: "oversized_explicit",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("x-bf-session-id", strings.Repeat("x", schemas.MaxSessionIDLength+1))
				h.Set("session-id", "harness")
			},
		},
		{
			name: "unicode_boundary",
			setup: func(h *fasthttp.RequestHeader) {
				h.Set("x-bf-session-id", strings.Repeat("界", schemas.MaxSessionIDLength))
			},
		},
	}

	for _, workload := range workloads {
		h := reviewHeaderSet()
		workload.setup(h)
		want := ResolveSessionIDFromRequest(h)
		if got := reviewResolveSessionIDWithPeek(h); got != want {
			b.Fatalf("%s: priority-Peek resolver = %q, production = %q", workload.name, got, want)
		}

		b.Run(workload.name+"/production", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				reviewSessionIDSink = ResolveSessionIDFromRequest(h)
			}
		})
		b.Run(workload.name+"/priority_peek", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				reviewSessionIDSink = reviewResolveSessionIDWithPeek(h)
			}
		})
	}
}
