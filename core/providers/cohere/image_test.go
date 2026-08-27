package cohere

import (
	"testing"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRawPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9ZQmcAAAAASUVORK5CYII="

func TestToCohereChatCompletionRequestPreservesHistoryAndCurrentImages(t *testing.T) {
	detail := "high"
	req, err := ToCohereChatCompletionRequest(&schemas.BifrostChatRequest{
		Provider: schemas.Cohere,
		Model:    "command-a-vision-07-2025",
		Input: []schemas.ChatMessage{
			{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{
					{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("describe the first image")},
					{Type: schemas.ChatContentBlockTypeImage, ImageURLStruct: &schemas.ChatInputImage{URL: "HTTPS://example.com/first.png", Detail: &detail}},
				}},
			},
			{
				Role:    schemas.ChatMessageRoleAssistant,
				Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("first response")},
			},
			{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{
					{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("describe the second image")},
					{Type: schemas.ChatContentBlockTypeImage, ImageURLStruct: &schemas.ChatInputImage{URL: testRawPNGBase64}},
				}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, req.Messages, 3)

	historyBlocks := req.Messages[0].Content.GetBlocks()
	require.Len(t, historyBlocks, 2)
	require.NotNil(t, historyBlocks[1].ImageURL)
	assert.Equal(t, "https://example.com/first.png", historyBlocks[1].ImageURL.URL)
	assert.Equal(t, detail, *historyBlocks[1].ImageURL.Detail)

	currentBlocks := req.Messages[2].Content.GetBlocks()
	require.Len(t, currentBlocks, 2)
	require.NotNil(t, currentBlocks[1].ImageURL)
	assert.Equal(t, "data:image/png;base64,"+testRawPNGBase64, currentBlocks[1].ImageURL.URL)

	raw, err := providerUtils.MarshalSorted(req)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"messages"`)
	assert.NotContains(t, string(raw), `"chat_history"`)
}

func TestToCohereChatCompletionRequestRejectsInvalidImages(t *testing.T) {
	for _, imageURL := range []string{
		"",
		"QUJD",
		"data:image/bmp;base64,Qk0=",
		"data:image/svg+xml;base64,PHN2Zy8+",
	} {
		t.Run(imageURL, func(t *testing.T) {
			_, err := ToCohereChatCompletionRequest(&schemas.BifrostChatRequest{
				Provider: schemas.Cohere,
				Model:    "command-a-vision-07-2025",
				Input: []schemas.ChatMessage{{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{
						Type:           schemas.ChatContentBlockTypeImage,
						ImageURLStruct: &schemas.ChatInputImage{URL: imageURL},
					}}},
				}},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid Cohere image")
			badRequest, ok := providerUtils.AsBifrostBadRequestError(err)
			require.True(t, ok)
			require.NotNil(t, badRequest.StatusCode)
			assert.Equal(t, 400, *badRequest.StatusCode)
		})
	}
}

func TestToCohereChatCompletionRequestRejectsMissingImageAndInvalidDetail(t *testing.T) {
	tests := []schemas.ChatContentBlock{
		{Type: schemas.ChatContentBlockTypeImage},
		{
			Type: schemas.ChatContentBlockTypeImage,
			ImageURLStruct: &schemas.ChatInputImage{
				URL:    "https://example.com/image.png",
				Detail: schemas.Ptr("medium"),
			},
		},
	}
	for _, block := range tests {
		_, err := ToCohereChatCompletionRequest(&schemas.BifrostChatRequest{
			Provider: schemas.Cohere,
			Model:    "command-a-vision-07-2025",
			Input: []schemas.ChatMessage{{
				Role:    schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{block}},
			}},
		})
		require.Error(t, err)
		_, ok := providerUtils.AsBifrostBadRequestError(err)
		assert.True(t, ok)
	}
}

func TestToCohereResponsesRequestNormalizesImagesWithoutMutatingInput(t *testing.T) {
	detail := "low"
	originalURL := testRawPNGBase64
	request := &schemas.BifrostResponsesRequest{
		Provider: schemas.Cohere,
		Model:    "command-a-vision-07-2025",
		Input: []schemas.ResponsesMessage{{
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleUser),
			Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesInputMessageContentBlockTypeImage,
				ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
					ImageURL: &originalURL,
					Detail:   &detail,
				},
			}}},
		}},
	}

	converted, err := ToCohereResponsesRequest(request)
	require.NoError(t, err)
	assert.Equal(t, testRawPNGBase64, *request.Input[0].Content.ContentBlocks[0].ResponsesInputMessageContentBlockImage.ImageURL)

	blocks := converted.Messages[0].Content.GetBlocks()
	require.Len(t, blocks, 1)
	require.NotNil(t, blocks[0].ImageURL)
	assert.Equal(t, "data:image/png;base64,"+testRawPNGBase64, blocks[0].ImageURL.URL)
	assert.Equal(t, detail, *blocks[0].ImageURL.Detail)
}

func TestNormalizeCohereResponsesImagesCopiesToolOutputImages(t *testing.T) {
	originalURL := testRawPNGBase64
	messages := []schemas.ResponsesMessage{{
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			Output: &schemas.ResponsesToolMessageOutputStruct{
				ResponsesFunctionToolCallOutputBlocks: []schemas.ResponsesMessageContentBlock{{
					Type: schemas.ResponsesInputMessageContentBlockTypeImage,
					ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
						ImageURL: &originalURL,
					},
				}},
			},
		},
	}}

	normalized, err := normalizeCohereResponsesImages(messages)
	require.NoError(t, err)
	assert.Equal(t, testRawPNGBase64, *messages[0].ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks[0].ResponsesInputMessageContentBlockImage.ImageURL)
	assert.Equal(t, "data:image/png;base64,"+testRawPNGBase64, *normalized[0].ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks[0].ResponsesInputMessageContentBlockImage.ImageURL)
}

func TestNormalizeCohereResponsesImagesDoesNotCopyTextOnlyMessages(t *testing.T) {
	messages := []schemas.ResponsesMessage{{
		Role:    schemas.Ptr(schemas.ResponsesInputMessageRoleUser),
		Content: &schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("hello")},
	}}

	normalized, err := normalizeCohereResponsesImages(messages)
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.True(t, &messages[0] == &normalized[0], "text-only normalization should reuse the caller's message slice")
}

func TestCohereInboundImageDetailIsPreserved(t *testing.T) {
	detail := "high"
	message := (&CohereMessage{
		Role: "user",
		Content: NewBlocksContent([]CohereContentBlock{{
			Type:     CohereContentBlockTypeImage,
			ImageURL: &CohereImageURL{URL: "https://example.com/image.png", Detail: &detail},
		}}),
	}).ToBifrostChatMessage()
	require.NotNil(t, message)
	require.NotNil(t, message.Content)
	require.Len(t, message.Content.ContentBlocks, 1)
	require.NotNil(t, message.Content.ContentBlocks[0].ImageURLStruct)
	assert.Equal(t, detail, *message.Content.ContentBlocks[0].ImageURLStruct.Detail)

	responsesBlock := convertCohereContentBlockToBifrost(CohereContentBlock{
		Type:     CohereContentBlockTypeImage,
		ImageURL: &CohereImageURL{URL: "https://example.com/image.png", Detail: &detail},
	})
	require.NotNil(t, responsesBlock.ResponsesInputMessageContentBlockImage)
	assert.Equal(t, detail, *responsesBlock.ResponsesInputMessageContentBlockImage.Detail)
}
