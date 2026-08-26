package bedrock

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatReasoningSummaryCarriesEncryptedReplayTokenOnce(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("first")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("second")},
		{Type: schemas.BifrostReasoningDetailsTypeEncrypted, Data: schemas.Ptr("replay-token")},
	})

	require.Len(t, blocks, 2)
	require.Equal(t, "first", *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "replay-token", *blocks[0].ReasoningContent.ReasoningText.Signature)
	require.Nil(t, blocks[1].ReasoningContent.ReasoningText.Signature)
}

func TestChatReasoningTextTakesPrecedenceOverSummaryAndEncrypted(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Type: schemas.BifrostReasoningDetailsTypeText, Text: schemas.Ptr("signed thinking"), Signature: schemas.Ptr("native-signature")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("summary")},
		{Type: schemas.BifrostReasoningDetailsTypeEncrypted, Data: schemas.Ptr("fallback-token")},
	})

	require.Len(t, blocks, 1)
	require.Equal(t, "signed thinking", *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "native-signature", *blocks[0].ReasoningContent.ReasoningText.Signature)
}

func TestChatReasoningEncryptedOnlyEmitsRequiredEmptyText(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{{
		Type: schemas.BifrostReasoningDetailsTypeEncrypted,
		Data: schemas.Ptr("replay-token"),
	}})

	require.Len(t, blocks, 1)
	require.NotNil(t, blocks[0].ReasoningContent.ReasoningText.Text)
	assert.Empty(t, *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "replay-token", *blocks[0].ReasoningContent.ReasoningText.Signature)
}

func TestBedrockInputAudioConversion(t *testing.T) {
	t.Run("chat mixed text and audio", func(t *testing.T) {
		converted, err := convertMessage(context.Background(), schemas.ChatMessage{
			Role: schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{
				{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("transcribe this")},
				{Type: schemas.ChatContentBlockTypeInputAudio, InputAudio: &schemas.ChatInputAudio{Data: "UklGRg==", Format: schemas.Ptr("wav")}},
			}},
		})
		require.NoError(t, err)
		require.Len(t, converted.Content, 2)
		require.NotNil(t, converted.Content[1].Audio)
		assert.Equal(t, "wav", converted.Content[1].Audio.Format)
		assert.Equal(t, "UklGRg==", *converted.Content[1].Audio.Source.Bytes)
	})

	t.Run("responses data URL", func(t *testing.T) {
		blocks, err := convertBifrostResponsesMessageContentBlocksToBedrockContentBlocks(context.Background(), schemas.ResponsesMessageContent{
			ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesInputMessageContentBlockTypeAudio,
				Audio: &schemas.ResponsesInputMessageContentBlockAudio{
					Format: "",
					Data:   "data:audio/mpeg;base64,SUQz",
				},
			}},
		})
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		require.NotNil(t, blocks[0].Audio)
		assert.Equal(t, "mpeg", blocks[0].Audio.Format)
		assert.Equal(t, "SUQz", *blocks[0].Audio.Source.Bytes)
	})
}

func TestPreparedAssetsConvertWithoutSyntheticDataURL(t *testing.T) {
	t.Run("image", func(t *testing.T) {
		blocks, err := convertContentBlock(schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeImage,
			ImageURLStruct: &schemas.ChatInputImage{
				URL: "https://example.com/large.png",
				ResolvedAsset: &schemas.ResolvedInputAsset{
					Data: "aW1hZ2U=", MediaType: "image/png",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		require.NotNil(t, blocks[0].Image)
		assert.Equal(t, "png", blocks[0].Image.Format)
		assert.Equal(t, "aW1hZ2U=", *blocks[0].Image.Source.Bytes)
	})

	t.Run("document", func(t *testing.T) {
		blocks, err := convertContentBlock(schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeFile,
			File: &schemas.ChatInputFile{
				Filename: schemas.Ptr("large.pdf"),
				ResolvedAsset: &schemas.ResolvedInputAsset{
					Data: "ZmlsZQ==", MediaType: "application/pdf",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		require.NotNil(t, blocks[0].Document)
		assert.Equal(t, "pdf", blocks[0].Document.Format)
		assert.Equal(t, "ZmlsZQ==", *blocks[0].Document.Source.Bytes)
	})
}

func TestResponsesFallbackOnlyMessageIsDropped(t *testing.T) {
	role := schemas.ResponsesInputMessageRoleAssistant
	msg := schemas.ResponsesMessage{
		Role: &role,
		Content: &schemas.ResponsesMessageContent{
			ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesOutputMessageContentTypeFallback,
				ResponsesOutputMessageContentFallback: &schemas.ResponsesOutputMessageContentFallback{
					FromModel: "old", ToModel: "new",
				},
			}},
		},
	}

	converted, err := convertBifrostMessageToBedrockMessage(context.Background(), &msg)
	require.NoError(t, err)
	assert.Nil(t, converted)
}

func TestResponsesUnknownOnlyMessageStillErrors(t *testing.T) {
	role := schemas.ResponsesInputMessageRoleAssistant
	msg := schemas.ResponsesMessage{
		Role: &role,
		Content: &schemas.ResponsesMessageContent{
			ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesMessageContentBlockType("future_unknown"),
			}},
		},
	}

	_, err := convertBifrostMessageToBedrockMessage(context.Background(), &msg)
	require.ErrorContains(t, err, "content must not be blank or unsupported")
}

func TestResponsesBlankToolCallIDsReturnErrors(t *testing.T) {
	for _, msgType := range []schemas.ResponsesMessageType{
		schemas.ResponsesMessageTypeFunctionCall,
		schemas.ResponsesMessageTypeFunctionCallOutput,
	} {
		t.Run(string(msgType), func(t *testing.T) {
			_, _, err := ConvertBifrostMessagesToBedrockMessages(context.Background(), []schemas.ResponsesMessage{{
				Type: &msgType,
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					CallID: schemas.Ptr("  "),
				},
			}}, false)
			require.ErrorContains(t, err, "missing required call_id")
		})
	}
}

func TestDecodedToolResultEnvelopeHoistsCachePoint(t *testing.T) {
	envelope, err := encodeBedrockToolResultEnvelope([]BedrockContentBlock{
		{SearchResult: &BedrockSearchResultBlock{Source: "source", Title: "title"}},
		{CachePoint: &BedrockCachePoint{Type: BedrockCachePointTypeDefault, TTL: schemas.Ptr("1h")}},
	})
	require.NoError(t, err)

	msgType := schemas.ResponsesMessageTypeFunctionCallOutput
	messages, _, err := ConvertBifrostMessagesToBedrockMessages(context.Background(), []schemas.ResponsesMessage{{
		Type: &msgType,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID: schemas.Ptr("tooluse_search"),
			Output: &schemas.ResponsesToolMessageOutputStruct{
				ResponsesToolCallOutputStr: &envelope,
			},
		},
	}}, false)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 2)
	toolResult := messages[0].Content[0].ToolResult
	require.NotNil(t, toolResult)
	require.Len(t, toolResult.Content, 1)
	require.NotNil(t, toolResult.Content[0].SearchResult)
	assert.Nil(t, toolResult.Content[0].CachePoint)
	require.NotNil(t, messages[0].Content[1].CachePoint)
	require.Equal(t, "1h", *messages[0].Content[1].CachePoint.TTL)
}

func TestCachePointOnlyToolResultGetsNonEmptyContent(t *testing.T) {
	blocks := injectContentBlockCachePoints([]BedrockContentBlock{{
		ToolResult: &BedrockToolResult{
			ToolUseID: "tooluse_1",
			Content:   []BedrockContentBlock{{CachePoint: &BedrockCachePoint{Type: BedrockCachePointTypeDefault}}},
		},
	}}, nil, "messages.0.content")

	require.Len(t, blocks, 2)
	require.NotNil(t, blocks[0].ToolResult)
	require.Len(t, blocks[0].ToolResult.Content, 1)
	assert.JSONEq(t, `{}`, string(blocks[0].ToolResult.Content[0].JSON))
	require.NotNil(t, blocks[1].CachePoint)

	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"content":[]`)
}

func TestUnsupportedChatFileURLReportsScheme(t *testing.T) {
	fileURL := "ftp://example.com/report.pdf"
	_, err := convertContentBlock(schemas.ChatContentBlock{
		Type: schemas.ChatContentBlockTypeFile,
		File: &schemas.ChatInputFile{FileURL: &fileURL, FileType: schemas.Ptr("application/pdf")},
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unsupported URL scheme") && strings.Contains(err.Error(), `"ftp"`), err.Error())
}
