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

func BenchmarkReviewResolveSessionIDNoSession(b *testing.B) {
	h := reviewHeaderSet()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reviewSessionIDSink = ResolveSessionIDFromRequest(h)
	}
}

func BenchmarkReviewDirectPeekNoSession(b *testing.B) {
	h := reviewHeaderSet()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reviewSessionIDSink = string(h.Peek("x-bf-session-id"))
	}
}

func reviewResolveSessionIDWithPeek(h *fasthttp.RequestHeader) string {
	if value := strings.TrimSpace(string(h.Peek("x-bf-session-id"))); value != "" && len(value) <= schemas.MaxSessionIDLength {
		return value
	}
	for _, name := range schemas.HarnessSessionHeaders {
		if value := strings.TrimSpace(string(h.Peek(name))); value != "" && len(value) <= schemas.MaxSessionIDLength {
			return value
		}
	}
	return ""
}

func BenchmarkReviewPriorityPeekNoSession(b *testing.B) {
	h := reviewHeaderSet()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reviewSessionIDSink = reviewResolveSessionIDWithPeek(h)
	}
}

func BenchmarkReviewPriorityPeekUnderscoreSession(b *testing.B) {
	h := reviewHeaderSet()
	h.Set("session_id", "review-session")
	if got := reviewResolveSessionIDWithPeek(h); got != "review-session" {
		b.Fatalf("underscore header via Peek = %q", got)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reviewSessionIDSink = reviewResolveSessionIDWithPeek(h)
	}
}

func TestReviewPriorityPeekParsesUnderscoreHeader(t *testing.T) {
	var h fasthttp.RequestHeader
	raw := "POST /v1/chat/completions HTTP/1.1\r\nHost: localhost\r\nsession_id: raw-session\r\nContent-Length: 0\r\n\r\n"
	if err := h.Read(bufio.NewReader(strings.NewReader(raw))); err != nil {
		t.Fatal(err)
	}
	if got := reviewResolveSessionIDWithPeek(&h); got != "raw-session" {
		t.Fatalf("parsed underscore header via Peek = %q", got)
	}
}

// Recaptured from the PR #6333 review worktree. The original probe asserted
// that an oversized x-bf-session-id was accepted verbatim; the merged form of
// #6333 (2a64316c4) deliberately reversed that, capping every ingestion path at
// schemas.MaxSessionIDLength because session IDs become KV keys and exported
// trace attributes that nothing downstream bounds. This keeps the review's
// question as a live guard for the behavior that actually shipped: the
// oversized value is dropped, and it does not silently fall back to a harness
// header either.
func TestReviewOversizedExplicitSessionIsRejected(t *testing.T) {
	oversized := strings.Repeat("x", schemas.MaxSessionIDLength+1)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("x-bf-session-id", oversized)
	ctx.Request.Header.Set("session-id", "valid-harness-fallback")
	if got := ResolveSessionIDFromRequest(&ctx.Request.Header); got != "" {
		t.Fatalf("request resolver returned %q, want an oversized explicit session ID to be dropped", got)
	}
	bifrostCtx, cancel := ConvertToBifrostContext(ctx, testHandlerStore{})
	defer cancel()
	if got, _ := bifrostCtx.Value(schemas.BifrostContextKeySessionID).(string); got != "" {
		t.Fatalf("context resolver returned %q, want an oversized explicit session ID to be dropped", got)
	}
}
