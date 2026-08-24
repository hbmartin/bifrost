package openai

import (
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func assertStreamUnmarshalError(t *testing.T, chunks []*schemas.BifrostStreamChunk) {
	t.Helper()
	if len(chunks) != 1 {
		t.Fatalf("expected exactly one error chunk, got %d: %+v", len(chunks), chunks)
	}
	bifrostErr := chunks[0].BifrostError
	if bifrostErr == nil || bifrostErr.Error == nil {
		t.Fatalf("expected a stream unmarshal error, got %+v", chunks[0])
	}
	if bifrostErr.Error.Message != schemas.ErrProviderResponseUnmarshal {
		t.Fatalf("expected error message %q, got %+v", schemas.ErrProviderResponseUnmarshal, bifrostErr.Error)
	}
	if bifrostErr.IsBifrostError {
		t.Error("provider response decoding failures must remain retryable when they are the first stream chunk")
	}
	if bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != 502 {
		t.Fatalf("expected upstream status 502, got %v", bifrostErr.StatusCode)
	}
	if bifrostErr.Error.Type == nil || *bifrostErr.Error.Type != schemas.ProviderResponseInvalid {
		t.Fatalf("expected error type %q, got %v", schemas.ProviderResponseInvalid, bifrostErr.Error.Type)
	}
}

func TestChatStreamMalformedChunkTerminatesWithError(t *testing.T) {
	server := completeSSEServer(t, "data: {not-json}\n\ndata: [DONE]\n\n")
	defer server.Close()

	provider := newStreamTestProvider(server.URL)
	provider.sendBackRawResponse = true
	stream, bifrostErr := provider.ChatCompletionStream(newStreamTestContext(), passthroughPostHook, nil, testKey(), basicChatRequest())
	if bifrostErr != nil {
		t.Fatalf("stream setup failed: %v", bifrostErr)
	}

	chunks := collectChunks(t, stream)
	assertStreamUnmarshalError(t, chunks)
	if chunks[0].BifrostError.ExtraFields.RawResponse != "{not-json}" {
		t.Fatalf("expected malformed raw response to be retained as a string, got %#v", chunks[0].BifrostError.ExtraFields.RawResponse)
	}
	if _, err := schemas.Marshal(chunks[0].BifrostError); err != nil {
		t.Fatalf("stream decode error must remain serializable: %v", err)
	}
}

func TestTextStreamMalformedChunkTerminatesWithError(t *testing.T) {
	server := completeSSEServer(t, "data: {not-json}\n\ndata: [DONE]\n\n")
	defer server.Close()

	provider := newStreamTestProvider(server.URL)
	request := &schemas.BifrostTextCompletionRequest{
		Provider: schemas.OpenAI,
		Model:    "repro-model",
		Input:    &schemas.TextCompletionInput{PromptStr: schemas.Ptr("hi")},
	}
	stream, bifrostErr := provider.TextCompletionStream(newStreamTestContext(), passthroughPostHook, nil, testKey(), request)
	if bifrostErr != nil {
		t.Fatalf("stream setup failed: %v", bifrostErr)
	}

	assertStreamUnmarshalError(t, collectChunks(t, stream))
}

func TestChatStreamNilFinalResponseConverterLeavesFinalChunkUnmodified(t *testing.T) {
	stop := "stop"
	server := completeSSEServer(t, chatChunk("hello", nil)+chatChunk("", &stop)+"data: [DONE]\n\n")
	defer server.Close()

	provider := newStreamTestProvider(server.URL)
	var terminalPostHooks atomic.Int32
	postHook := func(ctx *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); ended {
			terminalPostHooks.Add(1)
		}
		return response, bifrostErr
	}
	stream, bifrostErr := HandleOpenAIChatCompletionStreaming(
		newStreamTestContext(),
		provider.streamingClient,
		server.URL+"/v1/chat/completions",
		basicChatRequest(),
		BearerAuthHeader(testKey()),
		nil,
		0,
		false,
		false,
		schemas.OpenAI,
		postHook,
		nil,
		nil,
		nil,
		nil,
		func(response *schemas.BifrostChatResponse) *schemas.BifrostChatResponse {
			if response.Usage != nil {
				return nil
			}
			return response
		},
		nil,
		testNoopLogger{},
		nil,
	)
	if bifrostErr != nil {
		t.Fatalf("stream setup failed: %v", bifrostErr)
	}

	chunks := collectChunks(t, stream)
	if len(chunks) == 0 {
		t.Fatal("expected the converted stream to retain its content chunk")
	}
	var final *schemas.BifrostChatResponse
	for _, chunk := range chunks {
		if chunk.BifrostError != nil {
			t.Fatalf("unexpected stream error after nil converter result: %+v", chunk.BifrostError)
		}
		if chunk.BifrostChatResponse != nil && chunk.BifrostChatResponse.Usage != nil {
			final = chunk.BifrostChatResponse
		}
	}
	if final == nil {
		t.Fatal("converter returned nil for the final chunk, but the unmodified terminal usage chunk was not forwarded")
	}
	if got := terminalPostHooks.Load(); got != 1 {
		t.Fatalf("terminal post-hook calls = %d, want 1", got)
	}
}

func TestTextStreamNilFinalResponseConverterLeavesFinalChunkUnmodified(t *testing.T) {
	body := `data: {"id":"cmpl-converter","object":"text_completion","created":1,"model":"repro-model","choices":[{"index":0,"text":"hello","finish_reason":null}]}` + "\n\n" +
		`data: {"id":"cmpl-converter","object":"text_completion","created":1,"model":"repro-model","choices":[{"index":0,"text":null,"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	server := completeSSEServer(t, body)
	defer server.Close()

	request := &schemas.BifrostTextCompletionRequest{
		Provider: schemas.OpenAI,
		Model:    "repro-model",
		Input:    &schemas.TextCompletionInput{PromptStr: schemas.Ptr("hi")},
	}
	provider := newStreamTestProvider(server.URL)
	var terminalPostHooks atomic.Int32
	postHook := func(ctx *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); ended {
			terminalPostHooks.Add(1)
		}
		return response, bifrostErr
	}
	stream, bifrostErr := HandleOpenAITextCompletionStreaming(
		newStreamTestContext(),
		provider.streamingClient,
		server.URL+"/v1/completions",
		request,
		BearerAuthHeader(testKey()),
		nil,
		0,
		false,
		false,
		schemas.OpenAI,
		nil,
		postHook,
		nil,
		func(response *schemas.BifrostTextCompletionResponse) *schemas.BifrostTextCompletionResponse {
			if response.Usage != nil {
				return nil
			}
			return response
		},
		testNoopLogger{},
		nil,
	)
	if bifrostErr != nil {
		t.Fatalf("stream setup failed: %v", bifrostErr)
	}

	chunks := collectChunks(t, stream)
	if len(chunks) == 0 {
		t.Fatal("expected the converted stream to retain its content chunk")
	}
	var final *schemas.BifrostTextCompletionResponse
	for _, chunk := range chunks {
		if chunk.BifrostError != nil {
			t.Fatalf("unexpected stream error after nil converter result: %+v", chunk.BifrostError)
		}
		if chunk.BifrostTextCompletionResponse != nil && chunk.BifrostTextCompletionResponse.Usage != nil {
			final = chunk.BifrostTextCompletionResponse
		}
	}
	if final == nil {
		t.Fatal("converter returned nil for the final chunk, but the unmodified terminal usage chunk was not forwarded")
	}
	if got := terminalPostHooks.Load(); got != 1 {
		t.Fatalf("terminal post-hook calls = %d, want 1", got)
	}
}

func TestOpenAIStreamChoiceStateIgnoresUnrecordedForwardedFinishReason(t *testing.T) {
	state := newOpenAIStreamChoiceState(1)
	stop := "stop"
	state.recordForwardedFinishReasons([]schemas.BifrostResponseChoice{{Index: 0, FinishReason: &stop}})

	if state.singleSeen || state.choices != nil {
		t.Fatalf("unrecorded finish reason mutated choice state: %#v", state)
	}
}

func TestOpenAIStreamChoiceStateUsesScalarSingleChoiceFastPath(t *testing.T) {
	state := newOpenAIStreamChoiceState(1)
	stop := "stop"
	state.record([]schemas.BifrostResponseChoice{{Index: 0}})
	finished := []schemas.BifrostResponseChoice{{Index: 0, FinishReason: &stop}}
	state.record(finished)
	state.recordForwardedFinishReasons(finished)

	if state.choices != nil {
		t.Fatalf("single-choice stream promoted to map-backed state: %#v", state)
	}
	if !state.allFinished() {
		t.Fatal("single-choice stream did not retain its finish reason")
	}
	if choices := state.createChatFinalChoices(); len(choices) != 1 || choices[0].Index != 0 {
		t.Fatalf("unexpected final choices: %#v", choices)
	}
}

func TestOpenAIStreamChoiceStateCountsEachFinishedChoiceOnce(t *testing.T) {
	state := newOpenAIStreamChoiceState(2)
	stop := "stop"
	finished := []schemas.BifrostResponseChoice{{Index: 0, FinishReason: &stop}}

	state.record(finished)
	state.record(finished)

	if state.finished != 1 {
		t.Fatalf("finished choice count = %d, want 1", state.finished)
	}
	if state.allFinished() {
		t.Fatal("duplicate finish reasons marked a two-choice stream complete")
	}
}

func TestOpenAIStreamChoiceStatePromotionPreservesFinishedCount(t *testing.T) {
	state := newOpenAIStreamChoiceState(1)
	stop := "stop"
	state.record([]schemas.BifrostResponseChoice{{Index: 0, FinishReason: &stop}})
	state.record([]schemas.BifrostResponseChoice{{Index: 1}})

	if state.choices == nil {
		t.Fatal("second choice did not promote stream state")
	}
	if state.finished != 1 {
		t.Fatalf("finished choice count after promotion = %d, want 1", state.finished)
	}
	if state.allFinished() {
		t.Fatal("promoted state ignored the unfinished second choice")
	}
}

func TestChatStreamPreservesAllChoices(t *testing.T) {
	body := `data: {"id":"chatcmpl-multi","object":"chat.completion.chunk","created":1,"model":"repro-model","choices":[{"index":0,"delta":{},"finish_reason":null},{"index":1,"delta":{"content":"second"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-multi","object":"chat.completion.chunk","created":1,"model":"repro-model","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"chatcmpl-multi","object":"chat.completion.chunk","created":1,"model":"repro-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"},{"index":1,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"id":"chatcmpl-multi","object":"chat.completion.chunk","created":1,"model":"repro-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n\n" +
		"data: [DONE]\n\n"
	server := completeSSEServer(t, body)
	defer server.Close()

	request := basicChatRequest()
	request.Params = &schemas.ChatParameters{N: schemas.Ptr(2)}
	provider := newStreamTestProvider(server.URL)
	stream, bifrostErr := provider.ChatCompletionStream(newStreamTestContext(), passthroughPostHook, nil, testKey(), request)
	if bifrostErr != nil {
		t.Fatalf("stream setup failed: %v", bifrostErr)
	}

	chunks := collectChunks(t, stream)
	var foundSecondChoice bool
	for _, chunk := range chunks {
		if chunk.BifrostError != nil {
			t.Fatalf("unexpected stream error: %+v", chunk.BifrostError)
		}
		if chunk.BifrostChatResponse == nil {
			continue
		}
		for _, choice := range chunk.BifrostChatResponse.Choices {
			if choice.Index == 1 && choice.ChatStreamResponseChoice != nil && choice.ChatStreamResponseChoice.Delta != nil &&
				choice.ChatStreamResponseChoice.Delta.Content != nil && *choice.ChatStreamResponseChoice.Delta.Content == "second" {
				foundSecondChoice = true
			}
		}
	}
	if !foundSecondChoice {
		t.Fatal("choice index 1 content was dropped when choice index 0 had an empty delta")
	}

	final := chunks[len(chunks)-1].BifrostChatResponse
	if final == nil || len(final.Choices) != 2 {
		t.Fatalf("expected a two-choice terminal chunk, got %+v", final)
	}
	for index, choice := range final.Choices {
		if choice.Index != index || choice.FinishReason == nil || *choice.FinishReason != "stop" {
			t.Errorf("unexpected terminal choice %d: %+v", index, choice)
		}
	}
}

func TestTextStreamPreservesAllChoices(t *testing.T) {
	body := `data: {"id":"cmpl-multi","object":"text_completion","created":1,"model":"repro-model","choices":[{"index":0,"text":null,"finish_reason":null},{"index":1,"text":"second","finish_reason":null}]}` + "\n\n" +
		`data: {"id":"cmpl-multi","object":"text_completion","created":1,"model":"repro-model","choices":[{"index":0,"text":"first","finish_reason":null}]}` + "\n\n" +
		`data: {"id":"cmpl-multi","object":"text_completion","created":1,"model":"repro-model","choices":[{"index":0,"text":null,"finish_reason":"stop"},{"index":1,"text":null,"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"id":"cmpl-multi","object":"text_completion","created":1,"model":"repro-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n\n" +
		"data: [DONE]\n\n"
	server := completeSSEServer(t, body)
	defer server.Close()

	request := &schemas.BifrostTextCompletionRequest{
		Provider: schemas.OpenAI,
		Model:    "repro-model",
		Input:    &schemas.TextCompletionInput{PromptStr: schemas.Ptr("hi")},
		Params:   &schemas.TextCompletionParameters{N: schemas.Ptr(2)},
	}
	provider := newStreamTestProvider(server.URL)
	stream, bifrostErr := provider.TextCompletionStream(newStreamTestContext(), passthroughPostHook, nil, testKey(), request)
	if bifrostErr != nil {
		t.Fatalf("stream setup failed: %v", bifrostErr)
	}

	chunks := collectChunks(t, stream)
	var foundSecondChoice bool
	for _, chunk := range chunks {
		if chunk.BifrostError != nil {
			t.Fatalf("unexpected stream error: %+v", chunk.BifrostError)
		}
		if chunk.BifrostTextCompletionResponse == nil {
			continue
		}
		for _, choice := range chunk.BifrostTextCompletionResponse.Choices {
			if choice.Index == 1 && choice.TextCompletionResponseChoice != nil && choice.TextCompletionResponseChoice.Text != nil &&
				*choice.TextCompletionResponseChoice.Text == "second" {
				foundSecondChoice = true
			}
		}
	}
	if !foundSecondChoice {
		t.Fatal("choice index 1 text was dropped when choice index 0 had no text")
	}

	final := chunks[len(chunks)-1].BifrostTextCompletionResponse
	if final == nil || len(final.Choices) != 2 {
		t.Fatalf("expected a two-choice terminal chunk, got %+v", final)
	}
	for index, choice := range final.Choices {
		if choice.Index != index || choice.FinishReason == nil || *choice.FinishReason != "stop" {
			t.Errorf("unexpected terminal choice %d: %+v", index, choice)
		}
	}
}
