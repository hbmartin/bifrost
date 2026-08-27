package gemini

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToBifrostResponsesRequestSkipsNullParts(t *testing.T) {
	var request GeminiGenerationRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"systemInstruction":{"parts":[null,{"text":"be concise"}]},
		"contents":[{"role":"user","parts":[null,{"text":"hello"},null]}]
	}`), &request))

	result := request.ToBifrostResponsesRequest(nil)
	require.NotNil(t, result)
	require.Len(t, result.Input, 2)
	require.NotNil(t, result.Input[0].Content)
	assert.Equal(t, "be concise", *result.Input[0].Content.ContentStr)
	require.NotNil(t, result.Input[1].Content)
	require.Len(t, result.Input[1].Content.ContentBlocks, 1)
	assert.Equal(t, "hello", *result.Input[1].Content.ContentBlocks[0].Text)

	assert.Empty(t, processGeminiPart(nil, &GeminiResponsesStreamState{}, 0))
}

func TestToBifrostResponsesRequestKeepsMultimodalPartOrderInOneMessage(t *testing.T) {
	request := &GeminiGenerationRequest{Contents: []Content{{
		Role: "user",
		Parts: []*Part{
			{Text: "before"},
			{InlineData: &Blob{MIMEType: "image/png", Data: "aW1hZ2U="}},
			{Text: "after"},
		},
	}}}

	result := request.ToBifrostResponsesRequest(nil)
	require.NotNil(t, result)
	require.Len(t, result.Input, 1, "one Gemini turn must remain one Bifrost message")
	require.NotNil(t, result.Input[0].Content)
	blocks := result.Input[0].Content.ContentBlocks
	require.Len(t, blocks, 3)
	assert.Equal(t, schemas.ResponsesInputMessageContentBlockTypeText, blocks[0].Type)
	assert.Equal(t, "before", *blocks[0].Text)
	assert.Equal(t, schemas.ResponsesInputMessageContentBlockTypeImage, blocks[1].Type)
	assert.Equal(t, "data:image/png;base64,aW1hZ2U=", *blocks[1].ResponsesInputMessageContentBlockImage.ImageURL)
	assert.Equal(t, schemas.ResponsesInputMessageContentBlockTypeText, blocks[2].Type)
	assert.Equal(t, "after", *blocks[2].Text)
}

func TestToBifrostResponsesRequestCorrelatesParallelIDlessCalls(t *testing.T) {
	request := &GeminiGenerationRequest{Contents: []Content{
		{
			Role: "model",
			Parts: []*Part{
				{FunctionCall: &FunctionCall{Name: "lookup", Args: json.RawMessage(`{"value":1}`)}},
				{FunctionCall: &FunctionCall{Name: "lookup", Args: json.RawMessage(`{"value":2}`)}},
			},
		},
		{
			Role: "user",
			Parts: []*Part{
				{FunctionResponse: &FunctionResponse{Name: "lookup", Response: json.RawMessage(`{"output":"first"}`)}},
				{FunctionResponse: &FunctionResponse{Name: "lookup", Response: json.RawMessage(`{"output":"second"}`)}},
			},
		},
	}}

	result := request.ToBifrostResponsesRequest(nil)
	require.NotNil(t, result)
	require.Len(t, result.Input, 4)

	firstCallID := *result.Input[0].ResponsesToolMessage.CallID
	secondCallID := *result.Input[1].ResponsesToolMessage.CallID
	assert.NotEmpty(t, firstCallID)
	assert.NotEmpty(t, secondCallID)
	assert.NotEqual(t, firstCallID, secondCallID)
	assert.Equal(t, firstCallID, *result.Input[2].ResponsesToolMessage.CallID)
	assert.Equal(t, secondCallID, *result.Input[3].ResponsesToolMessage.CallID)
}

func TestToBifrostResponsesRequestSkipsNullFunctionResponseParts(t *testing.T) {
	request := &GeminiGenerationRequest{Contents: []Content{{
		Role: "user",
		Parts: []*Part{{FunctionResponse: &FunctionResponse{
			ID:       "call_1",
			Name:     "read_file",
			Response: json.RawMessage(`{"output":"done"}`),
			Parts: []*Part{
				nil,
				{InlineData: &Blob{MIMEType: "image/png", Data: "aW1hZ2U="}},
			},
		}}},
	}}}

	result := request.ToBifrostResponsesRequest(nil)
	require.NotNil(t, result)
	require.Len(t, result.Input, 1)
	output := result.Input[0].ResponsesToolMessage.Output
	require.NotNil(t, output)
	require.Len(t, output.ResponsesFunctionToolCallOutputBlocks, 2)
	assert.Equal(t, schemas.ResponsesInputMessageContentBlockTypeText, output.ResponsesFunctionToolCallOutputBlocks[0].Type)
	assert.Equal(t, schemas.ResponsesInputMessageContentBlockTypeImage, output.ResponsesFunctionToolCallOutputBlocks[1].Type)
}
