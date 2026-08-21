package handlers

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/kvstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

type testWSHandlerStore struct {
	matcher *lib.HeaderMatcher
}

func (s testWSHandlerStore) GetHeaderMatcher() *lib.HeaderMatcher {
	return s.matcher
}

func (s testWSHandlerStore) GetStreamChunkInterceptor() lib.StreamChunkInterceptor {
	return nil
}

func (s testWSHandlerStore) GetAsyncJobExecutor() *logstore.AsyncJobExecutor {
	return nil
}

func (s testWSHandlerStore) GetAsyncJobResultTTL() int {
	return 0
}

func (s testWSHandlerStore) GetKVStore() *kvstore.Store {
	return nil
}

func (s testWSHandlerStore) GetMCPHeaderCombinedAllowlist() schemas.WhiteList {
	return nil
}

func (s testWSHandlerStore) ShouldAllowPerRequestStorageOverride() bool { return false }
func (s testWSHandlerStore) ShouldAllowPerRequestRawOverride() bool     { return false }
func (s testWSHandlerStore) ShouldAllowDirectKeys() bool                { return false }
func (s testWSHandlerStore) GetMCPExternalServerURL() string            { return "" }
func (s testWSHandlerStore) GetMCPExternalClientURL() string            { return "" }

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return false }

func TestResolveWSStreamIdleTimeoutUsesProviderOverride(t *testing.T) {
	cfg := &lib.Config{
		Providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			schemas.OpenAI: {
				NetworkConfig: &schemas.NetworkConfig{StreamIdleTimeoutInSeconds: 7},
			},
		},
	}

	timeout := resolveWSStreamIdleTimeout(cfg, schemas.OpenAI)
	assert.Equal(t, 7*time.Second, timeout)
}

func TestResolveWSStreamIdleTimeoutFallsBackToDefault(t *testing.T) {
	timeout := resolveWSStreamIdleTimeout(&lib.Config{}, schemas.OpenAI)
	assert.Equal(t, time.Duration(schemas.DefaultStreamIdleTimeoutInSeconds)*time.Second, timeout)
}

func TestIsWSReadTimeout(t *testing.T) {
	assert.True(t, isWSReadTimeout(timeoutNetError{}))
	assert.False(t, isWSReadTimeout(net.UnknownNetworkError("unknown")))
	assert.False(t, isWSReadTimeout(errors.New("boom")))
	assert.False(t, isWSReadTimeout(nil))
}

func TestNewBifrostError(t *testing.T) {
	bifrostErr := newBifrostError(504, "upstream_timeout", "upstream websocket stream timed out")
	if bifrostErr == nil {
		t.Fatal("expected bifrost error, got nil")
	}
	if bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != 504 {
		t.Fatalf("status code = %#v, want 504", bifrostErr.StatusCode)
	}
	if bifrostErr.Error == nil {
		t.Fatal("expected error field, got nil")
	}
	if bifrostErr.Error.Type == nil || *bifrostErr.Error.Type != "upstream_timeout" {
		t.Fatalf("error type = %#v, want upstream_timeout", bifrostErr.Error.Type)
	}
	if bifrostErr.Error.Message != "upstream websocket stream timed out" {
		t.Fatalf("error message = %q, want upstream websocket stream timed out", bifrostErr.Error.Message)
	}
}

func TestCreateBifrostContextFromAuth_BaggageSessionIDSetsGrouping(t *testing.T) {
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{}, &authHeaders{
		headers: []headerPair{
			{key: "baggage", value: "foo=bar, session-id=rt-ws-123, baz=qux"},
		},
	})
	defer cancel()

	if got, _ := ctx.Value(schemas.BifrostContextKeyParentRequestID).(string); got != "rt-ws-123" {
		t.Fatalf("parent request id = %q, want %q", got, "rt-ws-123")
	}
}

func TestCreateBifrostContextFromAuth_EmptyBaggageSessionIDIgnored(t *testing.T) {
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{}, &authHeaders{
		headers: []headerPair{
			{key: "baggage", value: "session-id=   "},
		},
	})
	defer cancel()

	if got := ctx.Value(schemas.BifrostContextKeyParentRequestID); got != nil {
		t.Fatalf("parent request id should be unset, got %#v", got)
	}
}

func TestCreateBifrostContextFromAuth_ForwardsPrefixedHeaders(t *testing.T) {
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{}, &authHeaders{
		headers: []headerPair{
			{key: "x-bf-eh-originator", value: "my-test-client"},
			{key: "x-bf-eh-x-trace-id", value: "abc-123"},
			{key: "x-bf-eh-cookie", value: "blocked"},
		},
	})
	defer cancel()

	extraHeaders, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string)
	if !ok {
		t.Fatal("expected websocket extra headers in context")
	}
	assert.Equal(t, []string{"my-test-client"}, extraHeaders["originator"])
	assert.Equal(t, []string{"abc-123"}, extraHeaders["x-trace-id"])
	assert.NotContains(t, extraHeaders, "cookie")
}

func TestCreateBifrostContextFromAuth_AppliesHeaderFilterAndDirectAllowlist(t *testing.T) {
	matcher := lib.NewHeaderMatcher(&configstoreTables.GlobalHeaderFilterConfig{
		Allowlist: []string{"originator", "anthropic-*"},
		Denylist:  []string{"anthropic-secret"},
	})
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{matcher: matcher}, &authHeaders{
		headers: []headerPair{
			{key: "x-bf-eh-originator", value: "allowed-prefix"},
			{key: "x-bf-eh-x-trace-id", value: "blocked-by-allowlist"},
			{key: "anthropic-beta", value: "allowed-direct"},
			{key: "anthropic-secret", value: "blocked-by-denylist"},
			{key: "x-bf-eh-anthropic-secret", value: "blocked-prefix-denylist"},
		},
	})
	defer cancel()

	extraHeaders, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string)
	if !ok {
		t.Fatal("expected websocket extra headers in context")
	}
	assert.Equal(t, []string{"allowed-prefix"}, extraHeaders["originator"])
	assert.Equal(t, []string{"allowed-direct"}, extraHeaders["anthropic-beta"])
	assert.NotContains(t, extraHeaders, "x-trace-id")
	assert.NotContains(t, extraHeaders, "anthropic-secret")
}

func TestCaptureAuthHeaders_PreservesDuplicateHeaderValues(t *testing.T) {
	var req fasthttp.Request
	req.Header.Set("Host", "example.test")
	req.Header.Add("x-bf-eh-x-trace-id", "trace-a")
	req.Header.Add("x-bf-eh-x-trace-id", "trace-b")

	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)

	auth := captureAuthHeaders(ctx)
	var traceValues []string
	for _, pair := range auth.headers {
		if pair.key == "x-bf-eh-x-trace-id" {
			traceValues = append(traceValues, pair.value)
		}
	}
	assert.Equal(t, []string{"trace-a", "trace-b"}, traceValues)
}

func TestCreateBifrostContextFromAuth_PreservesMultipleForwardedHeaderValues(t *testing.T) {
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{}, &authHeaders{
		headers: []headerPair{
			{key: "x-bf-eh-x-trace-id", value: "trace-a"},
			{key: "x-bf-eh-x-trace-id", value: "trace-b"},
		},
	})
	defer cancel()

	extraHeaders, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string)
	if !ok {
		t.Fatal("expected websocket extra headers in context")
	}
	assert.Equal(t, []string{"trace-a", "trace-b"}, extraHeaders["x-trace-id"])
}

func TestCreateBifrostContextFromAuth_BlocksWebSocketHandshakeForwardedHeaders(t *testing.T) {
	matcher := lib.NewHeaderMatcher(&configstoreTables.GlobalHeaderFilterConfig{
		Allowlist: []string{"*"},
	})
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{matcher: matcher}, &authHeaders{
		headers: []headerPair{
			{key: "x-bf-eh-upgrade", value: "websocket"},
			{key: "x-bf-eh-sec-websocket-protocol", value: "realtime"},
			{key: "sec-websocket-extensions", value: "permessage-deflate"},
			{key: "x-bf-eh-originator", value: "safe"},
		},
	})
	defer cancel()

	extraHeaders, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string)
	if !ok {
		t.Fatal("expected websocket extra headers in context")
	}
	assert.Equal(t, []string{"safe"}, extraHeaders["originator"])
	assert.NotContains(t, extraHeaders, "upgrade")
	assert.NotContains(t, extraHeaders, "sec-websocket-protocol")
	assert.NotContains(t, extraHeaders, "sec-websocket-extensions")
}

func TestCreateBifrostContextFromAuth_MatchesHTTPHeaderMapping(t *testing.T) {
	var req fasthttp.Request
	req.SetRequestURI("/v1/responses?team=acme-team")
	req.Header.Set("x-bf-session-id", "sess-1")
	req.Header.Set("x-bf-session-ttl", "24h")
	req.Header.Set("x-bf-dim-tenant", "acme")
	req.Header.Set("x-team-id", "team-7")
	req.Header.Set("x-bf-mcp-include-clients", "github")

	fctx := &fasthttp.RequestCtx{}
	fctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)

	auth := captureAuthHeaders(fctx)
	ctx, cancel := createBifrostContextFromAuth(testWSHandlerStore{}, auth)
	defer cancel()

	sessionID, _ := ctx.Value(schemas.BifrostContextKeySessionID).(string)
	assert.Equal(t, "sess-1", sessionID)

	ttl, _ := ctx.Value(schemas.BifrostContextKeySessionTTL).(time.Duration)
	assert.Equal(t, 24*time.Hour, ttl)

	dimensions, _ := ctx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	assert.Equal(t, map[string]string{"tenant": "acme"}, dimensions)

	requestHeaders, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	if assert.NotNil(t, requestHeaders, "governance required-headers checks read this map") {
		assert.Equal(t, "team-7", requestHeaders["x-team-id"])
	}

	requestQuery, _ := ctx.Value(schemas.BifrostContextKeyRequestQuery).(map[string]string)
	assert.Equal(t, map[string]string{"team": "acme-team"}, requestQuery)

	includeClients, _ := ctx.Value(schemas.MCPContextKeyIncludeClients).([]string)
	assert.Equal(t, []string{"github"}, includeClients)
}

func TestCreateBifrostContextFromAuth_ConflictingCredentialsMatchHTTPPrecedence(t *testing.T) {
	// Several header names can set the virtual key; on the HTTP path the
	// last-processed one wins, so the WS replay must preserve wire order for
	// both paths to resolve the same credential.
	var req fasthttp.Request
	req.SetRequestURI("/v1/responses")
	req.Header.Set("x-bf-vk", "sk-bf-from-vk-header")
	req.Header.Set("Authorization", "Bearer sk-bf-from-authorization")

	fctx := &fasthttp.RequestCtx{}
	fctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)

	httpCtx, httpCancel := lib.ConvertToBifrostContext(fctx, nil)
	defer httpCancel()
	httpVK, _ := httpCtx.Value(schemas.BifrostContextKeyVirtualKey).(string)

	auth := captureAuthHeaders(fctx)
	wsCtx, wsCancel := createBifrostContextFromAuth(nil, auth)
	defer wsCancel()
	wsVK, _ := wsCtx.Value(schemas.BifrostContextKeyVirtualKey).(string)

	assert.NotEmpty(t, httpVK)
	assert.Equal(t, httpVK, wsVK)
}

func TestMergeWebSocketHeaders_ForwardedHeadersOverrideProviderHeadersAndPreserveValues(t *testing.T) {
	ctx := schemas.NewBifrostContext(nil, time.Time{})
	ctx.SetValue(schemas.BifrostContextKeyExtraHeaders, map[string][]string{
		"originator":    {"my-test-client"},
		"authorization": {"Bearer malicious"},
		"x-static":      {"client-value-1", "client-value-2"},
	})

	merged := mergeWebSocketHeaders(ctx, map[string]string{
		"Authorization": "Bearer provider-key",
		"x-static":      "provider-value",
	})

	assert.Equal(t, []string{"my-test-client"}, merged.Values("originator"))
	assert.Equal(t, []string{"Bearer provider-key"}, merged.Values("Authorization"))
	assert.Equal(t, []string{"client-value-1", "client-value-2"}, merged.Values("x-static"))
	assert.NotContains(t, merged.Values("Authorization"), "Bearer malicious")
}

func TestHasWebSocketForwardedHeaders(t *testing.T) {
	ctx := schemas.NewBifrostContext(nil, time.Time{})
	assert.False(t, hasWebSocketForwardedHeaders(ctx))

	ctx.SetValue(schemas.BifrostContextKeyExtraHeaders, map[string][]string{
		"authorization": {"Bearer malicious"},
	})
	assert.False(t, hasWebSocketForwardedHeaders(ctx))

	ctx.SetValue(schemas.BifrostContextKeyExtraHeaders, map[string][]string{
		"x-trace-id": {"abc-123"},
	})
	assert.True(t, hasWebSocketForwardedHeaders(ctx))
}
