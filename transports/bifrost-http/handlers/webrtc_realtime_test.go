package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/kvstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"
	"github.com/valyala/fasthttp"
)

type testHandlerStore struct {
	kv *kvstore.Store
}

type realtimeKeySelectionTestAccount struct {
	keys []schemas.Key
}

func (a *realtimeKeySelectionTestAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return nil, nil
}

func (a *realtimeKeySelectionTestAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	return a.keys, nil
}

func (a *realtimeKeySelectionTestAccount) GetConfigForProvider(schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return nil, nil
}

func (s testHandlerStore) GetHeaderMatcher() *lib.HeaderMatcher { return nil }
func (s testHandlerStore) GetStreamChunkInterceptor() lib.StreamChunkInterceptor {
	return nil
}
func (s testHandlerStore) GetAsyncJobExecutor() *logstore.AsyncJobExecutor  { return nil }
func (s testHandlerStore) GetAsyncJobResultTTL() int                        { return 0 }
func (s testHandlerStore) GetKVStore() *kvstore.Store                       { return s.kv }
func (s testHandlerStore) GetMCPHeaderCombinedAllowlist() schemas.WhiteList { return nil }
func (s testHandlerStore) ShouldAllowPerRequestStorageOverride() bool       { return false }
func (s testHandlerStore) ShouldAllowPerRequestRawOverride() bool           { return false }
func (s testHandlerStore) ShouldAllowDirectKeys() bool                      { return false }
func (s testHandlerStore) GetMCPExternalServerURL() string                  { return "" }
func (s testHandlerStore) GetMCPExternalClientURL() string                  { return "" }

func TestResolveRealtimeSDPTarget_BaseRouteRequiresProviderPrefix(t *testing.T) {
	var ctx fasthttp.RequestCtx
	cfg := &lib.Config{}
	_, _, _, err := resolveRealtimeSDPTarget(&ctx, cfg, "/v1/realtime", []byte(`{"model":"gpt-4o-realtime-preview"}`))
	if err == nil {
		t.Fatal("expected provider/model validation error")
	}
	if err.Error == nil || err.Error.Message != "session.model must use provider/model on /v1 realtime routes" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestResolveRealtimeSDPTarget_BaseRouteNormalizesModel(t *testing.T) {
	var ctx fasthttp.RequestCtx
	cfg := &lib.Config{}
	provider, model, normalized, err := resolveRealtimeSDPTarget(&ctx, cfg, "/v1/realtime", []byte(`{"model":"openai/gpt-4o-realtime-preview","voice":"alloy"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != schemas.OpenAI {
		t.Fatalf("expected provider %s, got %s", schemas.OpenAI, provider)
	}
	if model != "gpt-4o-realtime-preview" {
		t.Fatalf("unexpected normalized model: %s", model)
	}

	var root map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(normalized, &root); unmarshalErr != nil {
		t.Fatalf("failed to unmarshal normalized session: %v", unmarshalErr)
	}
	var sessionModel string
	if unmarshalErr := json.Unmarshal(root["model"], &sessionModel); unmarshalErr != nil {
		t.Fatalf("failed to unmarshal model: %v", unmarshalErr)
	}
	if sessionModel != "gpt-4o-realtime-preview" {
		t.Fatalf("unexpected marshaled model: %s", sessionModel)
	}
}

func TestResolveRealtimeSDPTarget_OpenAIRouteDefaultsProvider(t *testing.T) {
	var ctx fasthttp.RequestCtx
	cfg := &lib.Config{}
	provider, model, _, err := resolveRealtimeSDPTarget(&ctx, cfg, "/openai/v1/realtime", []byte(`{"model":"gpt-4o-realtime-preview"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != schemas.OpenAI {
		t.Fatalf("expected provider %s, got %s", schemas.OpenAI, provider)
	}
	if model != "gpt-4o-realtime-preview" {
		t.Fatalf("unexpected model: %s", model)
	}
}

func TestParseCallsWebRTCRequest_RawSDPKeepsGARoute(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetRequestURI("/openai/v1/realtime/calls?model=gpt-realtime")
	ctx.Request.Header.SetContentType("application/sdp")
	ctx.Request.SetBodyString("v=0\r\n")

	sdpOffer, provider, model, session, err := parseCallsWebRTCRequest(&ctx, &lib.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sdpOffer != "v=0\r\n" {
		t.Fatalf("unexpected sdp offer: %q", sdpOffer)
	}
	if provider != schemas.OpenAI {
		t.Fatalf("expected provider %s, got %s", schemas.OpenAI, provider)
	}
	if model != "gpt-realtime" {
		t.Fatalf("unexpected model: %s", model)
	}
	if session != nil {
		t.Fatalf("expected nil session for raw SDP /calls request, got %s", string(session))
	}
}

func TestNewRealtimeRelayContextCopiesValuesWithoutRequestCancellation(t *testing.T) {
	requestCtx, requestCancel := schemas.NewBifrostContextWithCancel(context.Background())
	requestCtx.SetValue(schemas.BifrostContextKeyHTTPRequestType, schemas.RealtimeRequest)
	requestCtx.SetValue(schemas.BifrostContextKeyIntegrationType, "openai")
	requestCtx.SetValue(schemas.BifrostContextKeyGovernanceVirtualKeyID, "vk_test")

	relayCtx, relayCancel := newRealtimeRelayContext(requestCtx)
	defer relayCancel()

	requestCancel()

	select {
	case <-requestCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected request context to be cancelled")
	}

	select {
	case <-relayCtx.Done():
		t.Fatal("relay context should outlive cancelled request context")
	default:
	}

	if got := relayCtx.Value(schemas.BifrostContextKeyHTTPRequestType); got != schemas.RealtimeRequest {
		t.Fatalf("request type = %v, want %v", got, schemas.RealtimeRequest)
	}
	if got := relayCtx.Value(schemas.BifrostContextKeyIntegrationType); got != "openai" {
		t.Fatalf("integration type = %v, want %q", got, "openai")
	}
	if got := relayCtx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID); got != "vk_test" {
		t.Fatalf("virtual key id = %v, want %q", got, "vk_test")
	}
}

func TestParseRealtimeEventPreservesExtraParams(t *testing.T) {
	event, err := schemas.ParseRealtimeEvent([]byte(`{"type":"conversation.item.truncate","item_id":"item_123","content_index":0,"audio_end_ms":640}`))
	if err != nil {
		t.Fatalf("ParseRealtimeEvent() error = %v", err)
	}

	var itemID string
	if err := json.Unmarshal(event.ExtraParams["item_id"], &itemID); err != nil {
		t.Fatalf("json.Unmarshal(item_id) error = %v", err)
	}
	if itemID != "item_123" {
		t.Fatalf("item_id = %q, want %q", itemID, "item_123")
	}

	var contentIndex int
	if err := json.Unmarshal(event.ExtraParams["content_index"], &contentIndex); err != nil {
		t.Fatalf("json.Unmarshal(content_index) error = %v", err)
	}
	if contentIndex != 0 {
		t.Fatalf("content_index = %d, want 0", contentIndex)
	}
}

func TestExtractRealtimeBearerToken(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("Authorization", "Bearer ek_test_123")

	if got := extractRealtimeBearerToken(&ctx); got != "ek_test_123" {
		t.Fatalf("extractRealtimeBearerToken() = %q, want %q", got, "ek_test_123")
	}
}

func TestClassifyRealtimeKeySelectionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		mapped     bool
		wantStatus int
		wantType   string
		wantMsg    string
	}{
		{
			name:       "stale mapped pin",
			err:        fmt.Errorf("%w: removed", bifrost.ErrPinnedAPIKeyUnavailable),
			mapped:     true,
			wantStatus: fasthttp.StatusUnauthorized,
			wantType:   "invalid_request_error",
			wantMsg:    errRealtimeEphemeralKeyUnknown.Error(),
		},
		{
			name:       "caller supplied stale pin",
			err:        fmt.Errorf("%w: removed", bifrost.ErrPinnedAPIKeyUnavailable),
			wantStatus: fasthttp.StatusBadRequest,
			wantType:   "invalid_request_error",
			wantMsg:    "pinned API key unavailable: removed",
		},
		{
			name:       "key store failure",
			err:        fmt.Errorf("%w: database offline", bifrost.ErrKeySelectionUnavailable),
			mapped:     true,
			wantStatus: fasthttp.StatusInternalServerError,
			wantType:   "server_error",
			wantMsg:    realtimeKeySelectionUnavailableMessage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, errorType, message := classifyRealtimeKeySelectionError(tt.err, tt.mapped)
			if status != tt.wantStatus || errorType != tt.wantType || message != tt.wantMsg {
				t.Fatalf("classification = (%d, %q, %q), want (%d, %q, %q)", status, errorType, message, tt.wantStatus, tt.wantType, tt.wantMsg)
			}
		})
	}
}

func TestLookupRealtimeEphemeralKeyMappingKeepsEntryUntilTTLExpiry(t *testing.T) {
	t.Parallel()

	store, err := kvstore.New(kvstore.Config{})
	if err != nil {
		t.Fatalf("kvstore.New() error = %v", err)
	}
	defer store.Close()

	payload, err := json.Marshal(realtimeEphemeralKeyMapping{Version: realtimeEphemeralKeyMappingVersion, KeyID: "key_123", VirtualKey: "sk-bf-test"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := store.SetWithTTL(buildRealtimeEphemeralKeyMappingKey("ek_test_123"), payload, time.Minute); err != nil {
		t.Fatalf("store.SetWithTTL() error = %v", err)
	}

	mapping, ok := lookupRealtimeEphemeralKeyMappingWithCodec(store, "ek_test_123", nil)
	if !ok {
		t.Fatal("expected mapping to be consumed")
	}
	if mapping.KeyID != "key_123" {
		t.Fatalf("mapping.KeyID = %q, want %q", mapping.KeyID, "key_123")
	}
	if mapping.VirtualKey != "sk-bf-test" {
		t.Fatalf("mapping.VirtualKey = %q, want %q", mapping.VirtualKey, "sk-bf-test")
	}

	raw, err := store.Get(buildRealtimeEphemeralKeyMappingKey("ek_test_123"))
	if err != nil {
		t.Fatalf("expected mapping to remain until TTL expiry: %v", err)
	}
	if raw == nil {
		t.Fatal("expected mapping to remain in KV store")
	}
}

func TestLookupRealtimeEphemeralKeyMappingRejectsUnversionedStringValue(t *testing.T) {
	t.Parallel()

	store, err := kvstore.New(kvstore.Config{})
	if err != nil {
		t.Fatalf("kvstore.New() error = %v", err)
	}
	defer store.Close()

	if err := store.SetWithTTL(buildRealtimeEphemeralKeyMappingKey("ek_test_legacy"), "key_legacy", time.Minute); err != nil {
		t.Fatalf("store.SetWithTTL() error = %v", err)
	}

	if mapping, ok := lookupRealtimeEphemeralKeyMappingWithCodec(store, "ek_test_legacy", nil); ok {
		t.Fatalf("lookupRealtimeEphemeralKeyMapping() = %#v, true; want legacy mapping rejected", mapping)
	}
}

func TestLookupRealtimeEphemeralKeyMappingAcceptsLegacyStructuredValue(t *testing.T) {
	t.Parallel()

	store, err := kvstore.New(kvstore.Config{})
	if err != nil {
		t.Fatalf("kvstore.New() error = %v", err)
	}
	defer store.Close()

	payload := []byte(`{"key_id":"key_legacy","virtual_key":"sk-bf-legacy"}`)
	if err := store.SetWithTTL(buildRealtimeEphemeralKeyMappingKey("ek_test_legacy_structured"), payload, time.Minute); err != nil {
		t.Fatalf("store.SetWithTTL() error = %v", err)
	}

	mapping, ok := lookupRealtimeEphemeralKeyMappingWithCodec(store, "ek_test_legacy_structured", nil)
	if !ok {
		t.Fatal("expected the previous release's structured mapping to remain readable")
	}
	if mapping.Version != realtimeEphemeralKeyMappingVersion || mapping.KeyID != "key_legacy" || mapping.VirtualKey != "sk-bf-legacy" {
		t.Fatalf("unexpected normalized legacy mapping: %#v", mapping)
	}
}

func TestWebRTCRealtimeRelayCloseFinalizesActiveTurnHooks(t *testing.T) {
	t.Parallel()

	session := bfws.NewSession(nil)
	session.SetProviderSessionID("sess_provider_123")
	session.AddRealtimeInput("hello from user", `{"type":"conversation.item.added"}`)

	var (
		capturedErr *schemas.BifrostError
		cleanedUp   bool
	)
	session.SetRealtimeTurnHooks(&bfws.RealtimeTurnPluginState{
		RequestID: "req_realtime_123",
		StartedAt: time.Now().Add(-time.Second),
		PostHookRunner: func(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
			capturedErr = err
			return result, nil
		},
		Cleanup: func() {
			cleanedUp = true
		},
	})

	relay := &webrtcRealtimeRelay{
		session:     session,
		providerKey: schemas.OpenAI,
		model:       "gpt-realtime",
	}

	relay.close()

	if capturedErr == nil {
		t.Fatal("expected active turn to be finalized with an error on close")
	}
	if capturedErr.ExtraFields.RequestType != schemas.RealtimeRequest {
		t.Fatalf("request type = %q, want %q", capturedErr.ExtraFields.RequestType, schemas.RealtimeRequest)
	}
	if capturedErr.Error == nil || capturedErr.Error.Message != "realtime WebRTC session closed before turn completed" {
		t.Fatalf("error message = %#v, want realtime close message", capturedErr.Error)
	}
	if session.PeekRealtimeTurnHooks() != nil {
		t.Fatal("expected active realtime turn hooks to be cleared")
	}
	if !cleanedUp {
		t.Fatal("expected realtime hook cleanup to run")
	}
}

func TestResolveRealtimeWebRTCKeys_UnmappedEphemeralTokenIsRejected(t *testing.T) {
	t.Parallel()

	store, err := kvstore.New(kvstore.Config{})
	if err != nil {
		t.Fatalf("kvstore.New() error = %v", err)
	}
	defer store.Close()

	handler := &WebRTCRealtimeHandler{
		handlerStore: testHandlerStore{kv: store},
	}

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("Authorization", "Bearer ek_test_unmapped")

	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostCtx.SetValue(schemas.BifrostContextKeySelectedKeyID, "selected")
	bifrostCtx.SetValue(schemas.BifrostContextKeySelectedKeyName, "selected-name")
	bifrostCtx.SetValue(schemas.BifrostContextKeyAPIKeyID, "mapped-id")
	bifrostCtx.SetValue(schemas.BifrostContextKeyAPIKeyName, "mapped-name")

	authKey, selectedKey, mapped, err := handler.resolveRealtimeWebRTCKeys(&ctx, bifrostCtx, schemas.OpenAI, "gpt-realtime")
	if err != errRealtimeEphemeralKeyUnknown {
		t.Fatalf("resolveRealtimeWebRTCKeys() error = %v, want %v", err, errRealtimeEphemeralKeyUnknown)
	}
	if got := authKey.Value.GetValue(); got != "" {
		t.Fatalf("auth key value = %q, want empty", got)
	}
	if selectedKey != nil {
		t.Fatalf("selectedKey = %#v, want nil", selectedKey)
	}
	if mapped {
		t.Fatal("unmapped ephemeral token reported as mapped")
	}
}

func TestResolveRealtimeWebRTCKeysClassifiesStaleMappedKey(t *testing.T) {
	t.Parallel()

	store, err := kvstore.New(kvstore.Config{})
	if err != nil {
		t.Fatalf("kvstore.New() error = %v", err)
	}
	defer store.Close()
	payload, err := json.Marshal(realtimeEphemeralKeyMapping{
		Version:    realtimeEphemeralKeyMappingVersion,
		KeyID:      "missing-key",
		VirtualKey: "sk-bf-mapped",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := store.SetWithTTL(buildRealtimeEphemeralKeyMappingKey("ek_mapped"), payload, time.Minute); err != nil {
		t.Fatalf("store.SetWithTTL() error = %v", err)
	}

	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: &realtimeKeySelectionTestAccount{keys: []schemas.Key{
			{ID: "other-key", Value: *schemas.NewSecretVar("sk-provider"), Models: schemas.WhiteList{"*"}, Weight: 1},
		}},
	})
	if err != nil {
		t.Fatalf("bifrost.Init() error = %v", err)
	}
	defer client.Shutdown()

	handler := &WebRTCRealtimeHandler{
		client:       client,
		handlerStore: testHandlerStore{kv: store},
	}
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("Authorization", "Bearer ek_mapped")
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	_, selectedKey, mapped, err := handler.resolveRealtimeWebRTCKeys(&ctx, bifrostCtx, schemas.OpenAI, "gpt-realtime")
	if !errors.Is(err, bifrost.ErrPinnedAPIKeyUnavailable) {
		t.Fatalf("resolveRealtimeWebRTCKeys() error = %v, want %v", err, bifrost.ErrPinnedAPIKeyUnavailable)
	}
	if !mapped {
		t.Fatal("mapped ephemeral credential was not reported")
	}
	if selectedKey != nil {
		t.Fatalf("selectedKey = %#v, want nil", selectedKey)
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAPIKeyID); got != "missing-key" {
		t.Fatalf("api key pin = %#v, want mapped pin preserved", got)
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyVirtualKey); got != "sk-bf-mapped" {
		t.Fatalf("virtual key = %#v, want mapped governance identity preserved", got)
	}

	var relayCtx fasthttp.RequestCtx
	relayCtx.Request.Header.Set("Authorization", "Bearer ek_mapped")
	handler.runWebRTCRelay(&relayCtx, nil, schemas.OpenAI, "gpt-realtime", "v=0\r\n", nil)
	if got := relayCtx.Response.StatusCode(); got != fasthttp.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", got, fasthttp.StatusUnauthorized)
	}
	if body := string(relayCtx.Response.Body()); strings.Contains(body, "missing-key") {
		t.Fatalf("response leaked the stale mapped key ID: %s", body)
	}
}

func TestRunWebRTCRelayRejectsUnmappedEphemeralTokenAsUnauthorized(t *testing.T) {
	t.Parallel()

	store, err := kvstore.New(kvstore.Config{})
	if err != nil {
		t.Fatalf("kvstore.New() error = %v", err)
	}
	defer store.Close()

	handler := &WebRTCRealtimeHandler{handlerStore: testHandlerStore{kv: store}}
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("Authorization", "Bearer ek_test_unmapped")

	handler.runWebRTCRelay(&ctx, nil, schemas.OpenAI, "gpt-realtime", "v=0\r\n", nil)

	if got := ctx.Response.StatusCode(); got != fasthttp.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", got, fasthttp.StatusUnauthorized)
	}
}

func TestLookupRealtimeEphemeralKeyMappingHandlesTypedNilStore(t *testing.T) {
	t.Parallel()

	var store *kvstore.Store
	if mapping, ok := lookupRealtimeEphemeralKeyMappingWithCodec(store, "ek_test", nil); ok {
		t.Fatalf("lookupRealtimeEphemeralKeyMapping() = %#v, true; want no mapping", mapping)
	}
}

func TestApplyRealtimeEphemeralKeyMapping_ReplacesCompetingCredentialAliases(t *testing.T) {
	t.Parallel()

	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostCtx.SetValue(schemas.BifrostContextKeyVirtualKey, "sk-bf-competing")
	bifrostCtx.SetValue(schemas.BifrostContextKeyAPIKeyID, "competing-id")
	bifrostCtx.SetValue(schemas.BifrostContextKeyAPIKeyName, "competing-name")
	bifrostCtx.SetValue(schemas.BifrostContextKeySelectedKeyID, "routing-selected-id")
	applyRealtimeEphemeralKeyMapping(bifrostCtx, realtimeEphemeralKeyMapping{
		KeyID:      "key_123",
		VirtualKey: "sk-bf-test",
	})

	if got := bifrostCtx.Value(schemas.BifrostContextKeyVirtualKey); got != "sk-bf-test" {
		t.Fatalf("virtual key context = %#v, want %q", got, "sk-bf-test")
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAPIKeyID); got != "key_123" {
		t.Fatalf("api key id context = %#v, want %q", got, "key_123")
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAPIKeyName); got != nil {
		t.Fatalf("api key name context = %#v, want nil", got)
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeySelectedKeyID); got != "routing-selected-id" {
		t.Fatalf("selected key id context = %#v, want preserved routing metadata", got)
	}
}

func TestApplyRealtimeEphemeralKeyMapping_ClearsAliasesMissingFromMapping(t *testing.T) {
	t.Parallel()

	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostCtx.SetValue(schemas.BifrostContextKeyVirtualKey, "sk-bf-competing")
	bifrostCtx.SetValue(schemas.BifrostContextKeyAPIKeyID, "competing-id")
	bifrostCtx.SetValue(schemas.BifrostContextKeyAPIKeyName, "competing-name")
	applyRealtimeEphemeralKeyMapping(bifrostCtx, realtimeEphemeralKeyMapping{VirtualKey: "sk-bf-mapped"})

	if got := bifrostCtx.Value(schemas.BifrostContextKeyVirtualKey); got != "sk-bf-mapped" {
		t.Fatalf("virtual key context = %#v, want %q", got, "sk-bf-mapped")
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAPIKeyID); got != nil {
		t.Fatalf("api key id context = %#v, want nil", got)
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAPIKeyName); got != nil {
		t.Fatalf("api key name context = %#v, want nil", got)
	}
}

func TestApplyRealtimeEphemeralKeyMappingVersionedMappingWithoutVirtualKeyClearsCallerVirtualKey(t *testing.T) {
	t.Parallel()

	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostCtx.SetValue(schemas.BifrostContextKeyVirtualKey, "sk-bf-caller-controlled")
	applyRealtimeEphemeralKeyMapping(bifrostCtx, realtimeEphemeralKeyMapping{
		Version: realtimeEphemeralKeyMappingVersion,
		KeyID:   "key_123",
	})

	if got := bifrostCtx.Value(schemas.BifrostContextKeyVirtualKey); got != nil {
		t.Fatalf("virtual key context = %#v, want caller identity cleared", got)
	}
	if got := bifrostCtx.Value(schemas.BifrostContextKeyAPIKeyID); got != "key_123" {
		t.Fatalf("api key id context = %#v, want explicit mapped key", got)
	}
}
