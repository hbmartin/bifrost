package bifrost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminalErrorObserver records the error metadata post-hooks observe on the
// worker-error path and optionally swallows the error, returning (nil, nil).
type terminalErrorObserver struct {
	suppress bool
	seen     []schemas.BifrostErrorExtraFields
}

func (p *terminalErrorObserver) GetName() string { return "terminal-error-observer" }
func (p *terminalErrorObserver) Cleanup() error  { return nil }
func (p *terminalErrorObserver) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *terminalErrorObserver) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (p *terminalErrorObserver) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if bifrostErr != nil {
		p.seen = append(p.seen, bifrostErr.ExtraFields)
	}
	if p.suppress {
		return nil, nil, nil
	}
	return resp, bifrostErr, nil
}

// newRejectingUpstreamClient wires a real Bifrost against an OpenAI-compatible
// upstream that rejects every request with HTTP 400, so the provider worker
// produces a terminal error (400 is neither retried nor rotated) that flows
// back through msg.Err. The single key aliases "fast" to a concrete model so
// the worker's alias resolution is observable on the error.
func newRejectingUpstreamClient(t *testing.T, plugins ...schemas.LLMPlugin) *Bifrost {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream rejected the request","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(upstream.Close)

	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 1, upstream.URL)
	account.SetKeysForProvider(schemas.OpenAI, []schemas.Key{{
		ID:      "openai-alias-key",
		Value:   *schemas.NewSecretVar("sk-test"),
		Models:  schemas.WhiteList{"*"},
		Aliases: schemas.KeyAliases{"fast": {ModelID: "gpt-4o-mini-2024-07-18"}},
	}})

	client, initErr := Init(context.Background(), schemas.BifrostConfig{
		Account:    account,
		Logger:     NewNoOpLogger(),
		LLMPlugins: plugins,
	})
	require.NoError(t, initErr)
	t.Cleanup(client.Shutdown)
	return client
}

func newAliasedChatRequest() *schemas.BifrostChatRequest {
	return &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "fast",
		Input: []schemas.ChatMessage{{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("hello")},
		}},
	}
}

// Post-hooks must observe the worker's alias-resolved metadata on the terminal
// error for both request shapes; re-stamping the error with the alias name
// before hooks run would make logging/telemetry attribute the failure to the
// alias instead of the model actually called.
func TestWorkerErrorPostHooksObserveResolvedModel(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "unary"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			observer := &terminalErrorObserver{}
			client := newRejectingUpstreamClient(t, observer)
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

			var bifrostErr *schemas.BifrostError
			if streaming {
				var stream chan *schemas.BifrostStreamChunk
				stream, bifrostErr = client.ChatCompletionStreamRequest(ctx, newAliasedChatRequest())
				assert.Nil(t, stream)
			} else {
				var resp *schemas.BifrostChatResponse
				resp, bifrostErr = client.ChatCompletionRequest(ctx, newAliasedChatRequest())
				assert.Nil(t, resp)
			}
			require.NotNil(t, bifrostErr)
			require.NotNil(t, bifrostErr.StatusCode)
			assert.Equal(t, http.StatusBadRequest, *bifrostErr.StatusCode)

			// The caller-visible error must keep the worker's stamps too: the
			// post-hook restore may not clobber ResolvedModelUsed back to the
			// alias while RoutingInfo still names the target.
			assert.Equal(t, "fast", bifrostErr.ExtraFields.OriginalModelRequested)
			assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.ResolvedModelUsed,
				"caller-visible error must keep the worker's alias-resolved model after post-hooks")
			require.NotNil(t, bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias)
			assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias.ModelID,
				"RoutingInfo and the deprecated triplet must agree on the caller-visible error")

			require.Len(t, observer.seen, 1, "post-hook must run exactly once for the terminal error")
			seen := observer.seen[0]
			assert.Equal(t, "fast", seen.OriginalModelRequested)
			assert.Equal(t, "gpt-4o-mini-2024-07-18", seen.ResolvedModelUsed, "post-hooks must see the worker's alias-resolved model")
			assert.Equal(t, schemas.OpenAI, seen.Provider)
			require.NotNil(t, seen.RoutingInfo.ResolvedKeyAlias, "worker routing info must survive to post-hooks")
			assert.Equal(t, "gpt-4o-mini-2024-07-18", seen.RoutingInfo.ResolvedKeyAlias.ModelID)
		})
	}
}

// When a post-hook swallows a worker-produced error without supplying a
// response, the original error stays authoritative on both request shapes.
// The unary path previously returned (nil, nil) here, which the public
// request methods dereference.
func TestWorkerErrorKeepsOriginalErrorWhenPostHookReturnsNilNil(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "unary"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			observer := &terminalErrorObserver{suppress: true}
			client := newRejectingUpstreamClient(t, observer)
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

			var bifrostErr *schemas.BifrostError
			if streaming {
				var stream chan *schemas.BifrostStreamChunk
				stream, bifrostErr = client.ChatCompletionStreamRequest(ctx, newAliasedChatRequest())
				assert.Nil(t, stream)
			} else {
				var resp *schemas.BifrostChatResponse
				resp, bifrostErr = client.ChatCompletionRequest(ctx, newAliasedChatRequest())
				assert.Nil(t, resp)
			}
			require.NotNil(t, bifrostErr, "suppressed terminal error must remain authoritative")
			require.NotNil(t, bifrostErr.StatusCode)
			assert.Equal(t, http.StatusBadRequest, *bifrostErr.StatusCode)
			assert.Equal(t, schemas.OpenAI, bifrostErr.ExtraFields.Provider)
			assert.Equal(t, "fast", bifrostErr.ExtraFields.OriginalModelRequested)
			assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.ResolvedModelUsed,
				"swallowed worker error must escape with the resolved model intact")
			require.Len(t, observer.seen, 1)
		})
	}
}

// terminalErrorTamperer is the adversarial counterpart to
// terminalErrorObserver: it rewrites every request-identity field on the error
// a post-hook receives. Core snapshots those fields before hooks run and
// restores them afterwards, so none of these values may reach the caller.
type terminalErrorTamperer struct {
	suppress bool
}

func (p *terminalErrorTamperer) GetName() string { return "terminal-error-tamperer" }
func (p *terminalErrorTamperer) Cleanup() error  { return nil }
func (p *terminalErrorTamperer) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *terminalErrorTamperer) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (p *terminalErrorTamperer) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if bifrostErr != nil {
		bifrostErr.ExtraFields.Provider = schemas.Anthropic
		bifrostErr.ExtraFields.OriginalModelRequested = "tampered-original"
		bifrostErr.ExtraFields.ResolvedModelUsed = "tampered-resolved"
		bifrostErr.ExtraFields.RoutingInfo = schemas.RoutingInfo{
			Provider: schemas.Anthropic,
			Model:    "tampered-routing",
		}
	}
	if p.suppress {
		return nil, nil, nil
	}
	return resp, bifrostErr, nil
}

// A post-hook may not rewrite the routed identity of a terminal error, on
// either the pass-through or the swallowed path: the caller must still see the
// provider, requested model, alias-resolved model, and RoutingInfo the worker
// stamped.
func TestWorkerErrorRestoresRoutedIdentityAfterPostHookTampering(t *testing.T) {
	for _, suppress := range []bool{false, true} {
		name := "pass-through"
		if suppress {
			name = "swallow"
		}
		t.Run(name, func(t *testing.T) {
			client := newRejectingUpstreamClient(t, &terminalErrorTamperer{suppress: suppress})
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

			resp, bifrostErr := client.ChatCompletionRequest(ctx, newAliasedChatRequest())
			assert.Nil(t, resp)
			require.NotNil(t, bifrostErr, "a tampering post-hook must not make the error disappear")

			assert.Equal(t, schemas.OpenAI, bifrostErr.ExtraFields.Provider,
				"post-hook provider tampering must not survive")
			assert.Equal(t, "fast", bifrostErr.ExtraFields.OriginalModelRequested,
				"post-hook requested-model tampering must not survive")
			assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.ResolvedModelUsed,
				"post-hook resolved-model tampering must not survive")
			assert.Equal(t, schemas.OpenAI, bifrostErr.ExtraFields.RoutingInfo.Provider)
			assert.Equal(t, "fast", bifrostErr.ExtraFields.RoutingInfo.Model,
				"post-hook RoutingInfo tampering must not survive")
			require.NotNil(t, bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias,
				"the worker's alias resolution must be restored with RoutingInfo")
			assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias.ModelID)
		})
	}
}

// terminalErrorInPlaceTamperer rewrites the routed identity *through* the
// pointers RoutingInfo carries instead of replacing the struct. A value-copy
// snapshot shares those pointees, so this is precisely the tampering a shallow
// snapshot cannot undo: the restore reads ResolvedKeyAlias.ModelID back out of
// the snapshot to re-derive ResolvedModelUsed, and would republish the
// tampered value as though the worker had stamped it.
type terminalErrorInPlaceTamperer struct{}

func (p *terminalErrorInPlaceTamperer) GetName() string { return "terminal-error-in-place-tamperer" }
func (p *terminalErrorInPlaceTamperer) Cleanup() error  { return nil }
func (p *terminalErrorInPlaceTamperer) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *terminalErrorInPlaceTamperer) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (p *terminalErrorInPlaceTamperer) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if bifrostErr != nil {
		if alias := bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias; alias != nil {
			alias.ModelID = "tampered-deployment"
			alias.ModelName = schemas.Ptr("tampered-name")
		}
	}
	return resp, bifrostErr, nil
}

// The anti-tamper restore must survive mutation through RoutingInfo's pointers,
// not just wholesale replacement of the struct.
func TestWorkerErrorRestoresRoutedIdentityAfterInPlacePostHookTampering(t *testing.T) {
	client := newRejectingUpstreamClient(t, &terminalErrorInPlaceTamperer{})
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	resp, bifrostErr := client.ChatCompletionRequest(ctx, newAliasedChatRequest())
	assert.Nil(t, resp)
	require.NotNil(t, bifrostErr)

	require.NotNil(t, bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias)
	assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias.ModelID,
		"in-place alias tampering must not survive")
	assert.Nil(t, bifrostErr.ExtraFields.RoutingInfo.ResolvedKeyAlias.ModelName,
		"the worker resolved no canonical name; a hook may not invent one")
	assert.Equal(t, "gpt-4o-mini-2024-07-18", bifrostErr.ExtraFields.ResolvedModelUsed,
		"the restore must not re-derive the resolved model from a tampered alias")
}

// Queue-transfer and shutdown-drain producers send errors whose ExtraFields
// carry no ResolvedModelUsed and no RoutingInfo. recoverFromTerminalError must
// complete that metadata before hooks run and keep it on the returned error,
// whether hooks pass the error through or swallow it.
func TestRecoverFromTerminalErrorFillsPartialProducerMetadata(t *testing.T) {
	for _, suppress := range []bool{false, true} {
		name := "pass-through"
		if suppress {
			name = "swallow"
		}
		t.Run(name, func(t *testing.T) {
			observer := &terminalErrorObserver{suppress: suppress}
			pipeline := &PluginPipeline{
				logger:     NewNoOpLogger(),
				tracer:     &schemas.NoOpTracer{},
				llmPlugins: []schemas.LLMPlugin{observer},
			}
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			// Mirrors the drainQueueWithErrors / shutdown-drain error shape:
			// RequestType, Provider, OriginalModelRequested only.
			partial := &schemas.BifrostError{
				Error: &schemas.ErrorField{Message: "provider is shutting down"},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RequestType:            schemas.ChatCompletionRequest,
					Provider:               schemas.OpenAI,
					OriginalModelRequested: "gpt-4o-mini",
				},
			}

			resp, errOut := recoverFromTerminalError(ctx, pipeline, partial, 1,
				schemas.ChatCompletionRequest, schemas.OpenAI, "gpt-4o-mini")
			assert.Nil(t, resp)
			require.NotNil(t, errOut)
			assert.Equal(t, "gpt-4o-mini", errOut.ExtraFields.ResolvedModelUsed,
				"partial producer metadata must be completed on the returned error")
			require.Len(t, observer.seen, 1)
			assert.Equal(t, "gpt-4o-mini", observer.seen[0].ResolvedModelUsed,
				"post-hooks must observe completed metadata")
		})
	}
}
