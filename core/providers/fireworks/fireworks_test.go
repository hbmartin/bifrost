package fireworks_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/internal/llmtests"
	fireworksprovider "github.com/maximhq/bifrost/core/providers/fireworks"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestFireworks(t *testing.T) {
	t.Parallel()
	if strings.TrimSpace(os.Getenv("FIREWORKS_API_KEY")) == "" {
		t.Skip("Skipping Fireworks tests because FIREWORKS_API_KEY is not set")
	}

	client, ctx, cancel, err := llmtests.SetupTest()
	if err != nil {
		t.Fatalf("Error initializing test setup: %v", err)
	}
	defer cancel()
	defer client.Shutdown()

	testConfig := llmtests.ComprehensiveTestConfig{
		Provider:                schemas.Fireworks,
		ChatModel:               "accounts/fireworks/models/deepseek-v4-pro",
		Fallbacks:               []schemas.Fallback{},
		TextModel:               "accounts/fireworks/models/deepseek-v4-pro",
		TextCompletionFallbacks: []schemas.Fallback{},
		EmbeddingModel:          "fireworks/qwen3-embedding-8b",
		ReasoningModel:          "",
		TranscriptionModel:      "",
		SpeechSynthesisModel:    "",
		Scenarios: llmtests.TestScenarios{
			TextCompletion:        true,
			TextCompletionStream:  true,
			SimpleChat:            true,
			CompletionStream:      true,
			MultiTurnConversation: true,
			ToolCalls:             true,
			ToolCallsStreaming:    true,
			MultipleToolCalls:     false,
			End2EndToolCalling:    false,
			AutomaticFunctionCall: false,
			ImageURL:              false,
			ImageBase64:           false,
			MultipleImages:        false,
			FileBase64:            false,
			FileURL:               false,
			CompleteEnd2End:       true,
			Embedding:             true,
			ListModels:            true,
			Reasoning:             false,
			Transcription:         false,
			SpeechSynthesis:       false,
			PromptCaching:         false,
		},
	}
	t.Run("FireworksTests", func(t *testing.T) {
		llmtests.RunAllComprehensiveTests(t, client, ctx, testConfig)
	})
}

// TestFireworksProviderUsesNativeEndpoints verifies that the Fireworks provider targets native completions, responses, and embeddings endpoints.
func TestFireworksProviderUsesNativeEndpoints(t *testing.T) {
	tests := []struct {
		name         string
		expectedPath string
		run          func(t *testing.T, provider *fireworksprovider.FireworksProvider, ctx *schemas.BifrostContext, key schemas.Key)
	}{
		{
			name:         "TextCompletion",
			expectedPath: "/v1/completions",
			run: func(t *testing.T, provider *fireworksprovider.FireworksProvider, ctx *schemas.BifrostContext, key schemas.Key) {
				t.Helper()
				prompt := "A is for apple and B is for"
				resp, err := provider.TextCompletion(ctx, key, &schemas.BifrostTextCompletionRequest{
					Provider: schemas.Fireworks,
					Model:    "accounts/fireworks/models/deepseek-v3p2",
					Input: &schemas.TextCompletionInput{
						PromptStr: &prompt,
					},
				})
				if err != nil {
					t.Fatalf("TextCompletion returned error: %v", llmtests.GetErrorMessage(err))
				}
				if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Text == nil || *resp.Choices[0].Text == "" {
					t.Fatalf("unexpected text completion response: %#v", resp)
				}
			},
		},
		{
			name:         "Responses",
			expectedPath: "/v1/responses",
			run: func(t *testing.T, provider *fireworksprovider.FireworksProvider, ctx *schemas.BifrostContext, key schemas.Key) {
				t.Helper()
				resp, err := provider.Responses(ctx, key, &schemas.BifrostResponsesRequest{
					Provider: schemas.Fireworks,
					Model:    "accounts/fireworks/models/deepseek-v3p2",
					Input: []schemas.ResponsesMessage{
						llmtests.CreateBasicResponsesMessage("hello"),
					},
					Params: &schemas.ResponsesParameters{
						PreviousResponseID: schemas.Ptr("resp_previous"),
						MaxToolCalls:       schemas.Ptr(2),
						Store:              schemas.Ptr(true),
					},
				})
				if err != nil {
					t.Fatalf("Responses returned error: %v", llmtests.GetErrorMessage(err))
				}
				if resp == nil || resp.PreviousResponseID == nil || *resp.PreviousResponseID != "resp_previous" {
					t.Fatalf("unexpected responses response: %#v", resp)
				}
			},
		},
		{
			name:         "Embedding",
			expectedPath: "/v1/embeddings",
			run: func(t *testing.T, provider *fireworksprovider.FireworksProvider, ctx *schemas.BifrostContext, key schemas.Key) {
				t.Helper()
				resp, err := provider.Embedding(ctx, key, &schemas.BifrostEmbeddingRequest{
					Provider: schemas.Fireworks,
					Model:    "accounts/fireworks/models/nomic-embed-text-v1.5",
					Input: &schemas.EmbeddingInput{
						Text: schemas.Ptr("embedding test"),
					},
				})
				if err != nil {
					t.Fatalf("Embedding returned error: %v", llmtests.GetErrorMessage(err))
				}
				if resp == nil || len(resp.Data) != 1 || len(resp.Data[0].Embedding.EmbeddingArray) != 3 {
					t.Fatalf("unexpected embedding response: %#v", resp)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestedPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestedPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/completions":
					_, _ = fmt.Fprint(w, `{"id":"cmpl_1","object":"text_completion","created":1,"model":"accounts/fireworks/models/deepseek-v3p2","choices":[{"text":" banana","index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
				case "/v1/responses":
					_, _ = fmt.Fprint(w, `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"accounts/fireworks/models/deepseek-v3p2","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"previous_response_id":"resp_previous","max_tool_calls":2,"store":true,"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0,"cached_read_tokens":0,"cached_write_tokens":0},"output_tokens":1,"total_tokens":2}}`)
				case "/v1/embeddings":
					_, _ = fmt.Fprint(w, `{"object":"list","model":"accounts/fireworks/models/nomic-embed-text-v1.5","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			provider := newTestFireworksProvider(t, server.URL)
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			key := schemas.Key{Value: *schemas.NewSecretVar("test-key")}

			tt.run(t, provider, ctx, key)

			if requestedPath != tt.expectedPath {
				t.Fatalf("expected request path %q, got %q", tt.expectedPath, requestedPath)
			}
		})
	}
}

// TestFireworksResponsesStreamUsesNativeResponsesEndpoint verifies that Fireworks responses streaming targets the native responses endpoint.
func TestFireworksResponsesStreamUsesNativeResponsesEndpoint(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":1,\"status\":\"completed\",\"model\":\"accounts/fireworks/models/deepseek-v3p2\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"status\":\"completed\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\",\"annotations\":[],\"logprobs\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0,\"cached_read_tokens\":0,\"cached_write_tokens\":0},\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	provider := newTestFireworksProvider(t, server.URL)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	key := schemas.Key{Value: *schemas.NewSecretVar("test-key")}
	postHookRunner := func(_ *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		return result, err
	}

	stream, err := provider.ResponsesStream(ctx, postHookRunner, nil, key, &schemas.BifrostResponsesRequest{
		Provider: schemas.Fireworks,
		Model:    "accounts/fireworks/models/deepseek-v3p2",
		Input: []schemas.ResponsesMessage{
			llmtests.CreateBasicResponsesMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("ResponsesStream returned error: %v", llmtests.GetErrorMessage(err))
	}

	sawCompleted := false
	for chunk := range stream {
		if chunk != nil && chunk.BifrostResponsesStreamResponse != nil &&
			chunk.BifrostResponsesStreamResponse.Type == schemas.ResponsesStreamResponseTypeCompleted {
			sawCompleted = true
		}
	}

	if requestedPath != "/v1/responses" {
		t.Fatalf("expected responses stream to hit /v1/responses, got %q", requestedPath)
	}
	if !sawCompleted {
		t.Fatal("expected a completed responses stream chunk")
	}
}

// newTestFireworksProvider creates a Fireworks provider configured to hit a local test server.
func newTestFireworksProvider(t *testing.T, baseURL string) *fireworksprovider.FireworksProvider {
	t.Helper()

	provider, err := fireworksprovider.NewFireworksProvider(&schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        baseURL,
			DefaultRequestTimeoutInSeconds: 300,
		},
	}, bifrost.NewNoOpLogger())
	if err != nil {
		t.Fatalf("failed to create Fireworks provider: %v", err)
	}
	return provider
}
