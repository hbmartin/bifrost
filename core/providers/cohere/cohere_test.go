package cohere_test

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/internal/llmtests"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestCohere(t *testing.T) {
	t.Parallel()
	if strings.TrimSpace(os.Getenv("COHERE_API_KEY")) == "" {
		t.Skip("Skipping Cohere tests because COHERE_API_KEY is not set")
	}

	client, ctx, cancel, err := llmtests.SetupTest()
	if err != nil {
		t.Fatalf("Error initializing test setup: %v", err)
	}
	defer cancel()
	defer client.Shutdown()

	testConfig := llmtests.ComprehensiveTestConfig{
		Provider:       schemas.Cohere,
		ChatModel:      "command-a-03-2025",
		VisionModel:    "command-a-vision-07-2025", // Cohere's latest vision model
		TextModel:      "",                         // Cohere focuses on chat
		EmbeddingModel: "embed-v4.0",
		RerankModel:    "rerank-v3.5",
		ReasoningModel: "command-a-reasoning-08-2025",
		Scenarios: llmtests.TestScenarios{
			TextCompletion:             false, // Not typical for Cohere
			SimpleChat:                 true,
			CompletionStream:           true,
			MultiTurnConversation:      true,
			ToolCalls:                  true,
			ToolCallsStreaming:         true,
			MultipleToolCalls:          true,
			MultipleToolCallsStreaming: true,
			End2EndToolCalling:         true,
			AutomaticFunctionCall:      true,  // May not support automatic
			ImageURL:                   true,  // Command A Vision accepts public HTTP(S) URLs
			ImageBase64:                true,  // Command A Vision accepts base64 data URLs
			MultipleImages:             true,  // Command A Vision supports up to 20 images per request
			FileBase64:                 false, // Not supported
			FileURL:                    false, // Not supported
			CompleteEnd2End:            false,
			Embedding:                  true,
			Rerank:                     true,
			Reasoning:                  true,
			ListModels:                 true,
			CountTokens:                true,
		},
	}

	t.Run("CohereTests", func(t *testing.T) {
		llmtests.RunAllComprehensiveTests(t, client, ctx, testConfig)
	})
}
