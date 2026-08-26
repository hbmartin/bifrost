package bedrock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reviewRegressionTestModel = "amazon.nova-lite-v1:0"

func TestChatReasoningSummaryKeepsEncryptedReplayTokenSeparate(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("first")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("second")},
		{Type: schemas.BifrostReasoningDetailsTypeEncrypted, Data: schemas.Ptr("replay-token")},
	})

	require.Len(t, blocks, 3)
	require.Equal(t, "first", *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Nil(t, blocks[0].ReasoningContent.ReasoningText.Signature)
	require.Nil(t, blocks[1].ReasoningContent.ReasoningText.Signature)
	require.Empty(t, *blocks[2].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "replay-token", *blocks[2].ReasoningContent.ReasoningText.Signature)
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

func TestChatReasoningSignatureOnlyTextDoesNotShadowSummaries(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Type: schemas.BifrostReasoningDetailsTypeText, Signature: schemas.Ptr("stream-signature")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("first summary")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("second summary")},
	})

	require.Len(t, blocks, 3)
	require.Equal(t, "first summary", *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Nil(t, blocks[0].ReasoningContent.ReasoningText.Signature)
	require.Equal(t, "second summary", *blocks[1].ReasoningContent.ReasoningText.Text)
	require.Nil(t, blocks[1].ReasoningContent.ReasoningText.Signature)
	require.Empty(t, *blocks[2].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "stream-signature", *blocks[2].ReasoningContent.ReasoningText.Signature)
}

func TestChatReasoningSignatureDeltaAttachesToVisibleTextAtSameIndex(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Index: 2, Type: schemas.BifrostReasoningDetailsTypeText, Text: schemas.Ptr("visible thinking")},
		{Index: 2, Type: schemas.BifrostReasoningDetailsTypeText, Signature: schemas.Ptr("stream-signature")},
	})

	require.Len(t, blocks, 1)
	require.Equal(t, "visible thinking", *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "stream-signature", *blocks[0].ReasoningContent.ReasoningText.Signature)
}

func TestChatReasoningDistinctSignedBlocksSurviveReusedIndex(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Index: 0, Type: schemas.BifrostReasoningDetailsTypeText, Text: schemas.Ptr("first thinking"), Signature: schemas.Ptr("first-signature")},
		{Index: 0, Type: schemas.BifrostReasoningDetailsTypeText, Text: schemas.Ptr("second thinking"), Signature: schemas.Ptr("second-signature")},
	})

	require.Len(t, blocks, 2)
	require.Equal(t, "first thinking", *blocks[0].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "first-signature", *blocks[0].ReasoningContent.ReasoningText.Signature)
	require.Equal(t, "second thinking", *blocks[1].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "second-signature", *blocks[1].ReasoningContent.ReasoningText.Signature)
}

func TestBedrockChatStreamReasoningUsesContentBlockIndex(t *testing.T) {
	for _, index := range []int{2, 7} {
		text := "thinking"
		response, bifrostErr, finished := (&BedrockStreamEvent{
			ContentBlockIndex: &index,
			Delta: &BedrockContentBlockDelta{
				ReasoningContent: &BedrockReasoningContentText{Text: &text},
			},
		}).ToBifrostChatCompletionStream(NewBedrockStreamState())

		require.Nil(t, bifrostErr)
		require.False(t, finished)
		require.NotNil(t, response)
		require.Len(t, response.Choices, 1)
		require.Len(t, response.Choices[0].ChatStreamResponseChoice.Delta.ReasoningDetails, 1)
		require.Equal(t, index, response.Choices[0].ChatStreamResponseChoice.Delta.ReasoningDetails[0].Index)
	}
}

func TestChatReasoningSummarySignaturesStayWithTheirText(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("unsigned first")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("signed second"), Signature: schemas.Ptr("second-signature")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("signed third"), Signature: schemas.Ptr("third-signature")},
	})

	require.Len(t, blocks, 3)
	require.Nil(t, blocks[0].ReasoningContent.ReasoningText.Signature)
	require.Equal(t, "second-signature", *blocks[1].ReasoningContent.ReasoningText.Signature)
	require.Equal(t, "third-signature", *blocks[2].ReasoningContent.ReasoningText.Signature)
}

func TestChatReasoningDetachedSignatureDoesNotSignDifferentSummary(t *testing.T) {
	blocks := convertChatReasoningDetailsToBedrock([]schemas.ChatReasoningDetails{
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("first"), Signature: schemas.Ptr("first-signature")},
		{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("second"), Signature: schemas.Ptr("second-signature")},
		{Type: schemas.BifrostReasoningDetailsTypeEncrypted, Data: schemas.Ptr("detached-signature")},
	})

	require.Len(t, blocks, 3)
	require.Equal(t, "first-signature", *blocks[0].ReasoningContent.ReasoningText.Signature)
	require.Equal(t, "second-signature", *blocks[1].ReasoningContent.ReasoningText.Signature)
	require.Empty(t, *blocks[2].ReasoningContent.ReasoningText.Text)
	require.Equal(t, "detached-signature", *blocks[2].ReasoningContent.ReasoningText.Signature)
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
		converted, err := convertMessage(context.Background(), reviewRegressionTestModel, schemas.ChatMessage{
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
		conversion, err := convertBifrostResponsesMessageContentBlocksToBedrockContentBlocksWithDisposition(context.Background(), reviewRegressionTestModel, schemas.ResponsesMessageContent{
			ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesInputMessageContentBlockTypeAudio,
				Audio: &schemas.ResponsesInputMessageContentBlockAudio{
					Format: "",
					Data:   "data:audio/mpeg;base64,SUQz",
				},
			}},
		})
		require.NoError(t, err)
		blocks := conversion.Blocks
		require.Len(t, blocks, 1)
		require.NotNil(t, blocks[0].Audio)
		assert.Equal(t, "mp3", blocks[0].Audio.Format)
		assert.Equal(t, "SUQz", *blocks[0].Audio.Source.Bytes)
	})

	t.Run("MIME aliases use Nova wire formats", func(t *testing.T) {
		assert.Equal(t, "mp3", normalizeBedrockAudioFormat("audio/mpeg; codecs=mp3"))
		assert.Equal(t, "aac", normalizeBedrockAudioFormat("audio/x-aac"))
		assert.Equal(t, "mp4", normalizeBedrockAudioFormat("audio/x-m4a"))
	})

	t.Run("Bedrock API formats remain accepted", func(t *testing.T) {
		for _, format := range []string{"mka", "pcm", "webm"} {
			audio, err := convertInputAudioToBedrock("UklGRg==", &format)
			require.NoError(t, err, format)
			require.NotNil(t, audio, format)
			assert.Equal(t, format, audio.Format)
		}
	})

	t.Run("responses structured tool output audio", func(t *testing.T) {
		msgType := schemas.ResponsesMessageTypeFunctionCallOutput
		messages, _, err := ConvertBifrostMessagesToBedrockMessages(context.Background(), reviewRegressionTestModel, []schemas.ResponsesMessage{{
			Type: &msgType,
			ResponsesToolMessage: &schemas.ResponsesToolMessage{
				CallID: schemas.Ptr("tool_audio"),
				Output: &schemas.ResponsesToolMessageOutputStruct{
					ResponsesFunctionToolCallOutputBlocks: []schemas.ResponsesMessageContentBlock{{
						Type: schemas.ResponsesInputMessageContentBlockTypeAudio,
						Audio: &schemas.ResponsesInputMessageContentBlockAudio{
							Data: "UklGRg==", Format: "wav",
						},
					}},
				},
			},
		}}, false)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.NotNil(t, messages[0].Content[0].ToolResult)
		require.Len(t, messages[0].Content[0].ToolResult.Content, 1)
		require.NotNil(t, messages[0].Content[0].ToolResult.Content[0].Audio)
	})
}

func TestBedrockAudioScanMatchesConvertedContent(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	audio := schemas.ChatContentBlock{
		Type:       schemas.ChatContentBlockTypeInputAudio,
		InputAudio: &schemas.ChatInputAudio{Data: "UklGRg==", Format: schemas.Ptr("wav")},
	}

	t.Run("chat ignores non-text system content", func(t *testing.T) {
		request, err := ToBedrockChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-sonnet-4-v1:0",
			Input: []schemas.ChatMessage{
				{Role: schemas.ChatMessageRoleSystem, Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{
					{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("system")}, audio,
				}}},
				{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("hello")}},
			},
		})
		require.NoError(t, err)
		require.Len(t, request.System, 1)
		require.Equal(t, "system", *request.System[0].Text)
	})

	t.Run("responses ignores non-text system content", func(t *testing.T) {
		msgType := schemas.ResponsesMessageTypeMessage
		systemRole := schemas.ResponsesInputMessageRoleSystem
		userRole := schemas.ResponsesInputMessageRoleUser
		request, err := ToBedrockResponsesRequest(ctx, &schemas.BifrostResponsesRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-sonnet-4-v1:0",
			Input: []schemas.ResponsesMessage{
				{Type: &msgType, Role: &systemRole, Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{
					{Type: schemas.ResponsesInputMessageContentBlockTypeText, Text: schemas.Ptr("system")},
					{Type: schemas.ResponsesInputMessageContentBlockTypeAudio, Audio: &schemas.ResponsesInputMessageContentBlockAudio{Data: "UklGRg==", Format: "wav"}},
				}}},
				{Type: &msgType, Role: &userRole, Content: &schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("hello")}},
			},
		})
		require.NoError(t, err)
		require.Len(t, request.System, 1)
		require.Equal(t, "system", *request.System[0].Text)
	})
}

func TestBedrockResponsesAudioScanMatchesActiveUnionArm(t *testing.T) {
	audioBlock := schemas.ResponsesMessageContentBlock{
		Type:  schemas.ResponsesInputMessageContentBlockTypeAudio,
		Audio: &schemas.ResponsesInputMessageContentBlockAudio{Data: "UklGRg==", Format: "wav"},
	}
	msgType := schemas.ResponsesMessageTypeMessage
	functionOutputType := schemas.ResponsesMessageTypeFunctionCallOutput
	userRole := schemas.ResponsesInputMessageRoleUser

	assert.False(t, bifrostResponsesMessagesHaveAudio([]schemas.ResponsesMessage{{
		Type: &msgType, Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{audioBlock}},
	}}), "a role-less message is skipped by the converter")

	assert.True(t, bifrostResponsesMessagesHaveAudio([]schemas.ResponsesMessage{{
		Type: &msgType, Role: &userRole,
		Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{audioBlock}},
	}}))

	assert.True(t, bifrostResponsesMessagesHaveAudio([]schemas.ResponsesMessage{{
		Type: &functionOutputType,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{Output: &schemas.ResponsesToolMessageOutputStruct{
			ResponsesFunctionToolCallOutputBlocks: []schemas.ResponsesMessageContentBlock{audioBlock},
		}},
	}}))

	textPreferredAudioBlock := audioBlock
	textPreferredAudioBlock.Text = schemas.Ptr("text wins in the structured-output converter")
	assert.False(t, bifrostResponsesMessagesHaveAudio([]schemas.ResponsesMessage{{
		Type: &functionOutputType,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{Output: &schemas.ResponsesToolMessageOutputStruct{
			ResponsesFunctionToolCallOutputBlocks: []schemas.ResponsesMessageContentBlock{textPreferredAudioBlock},
		}},
	}}))

	plainOutput := "plain text wins over the inactive structured arm"
	assert.False(t, bifrostResponsesMessagesHaveAudio([]schemas.ResponsesMessage{{
		Type: &functionOutputType,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{Output: &schemas.ResponsesToolMessageOutputStruct{
			ResponsesToolCallOutputStr:            &plainOutput,
			ResponsesFunctionToolCallOutputBlocks: []schemas.ResponsesMessageContentBlock{audioBlock},
		}},
	}}))

	envelope, err := encodeBedrockToolResultEnvelope([]BedrockContentBlock{{
		Audio: &BedrockAudioBlock{Format: "wav", Source: BedrockAudioSource{Bytes: schemas.Ptr("UklGRg==")}},
	}})
	require.NoError(t, err)
	assert.True(t, bedrockToolResultEnvelopeHasAudio(envelope))

	jsonOnlyEnvelope, err := encodeBedrockToolResultEnvelope([]BedrockContentBlock{{JSON: json.RawMessage(`{"audio":{"metadata":"not a content block"}}`)}})
	require.NoError(t, err)
	assert.False(t, bedrockToolResultEnvelopeHasAudio(jsonOnlyEnvelope))
}

func TestBedrockInputAudioIsModelGated(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	audioMessage := schemas.ChatMessage{
		Role: schemas.ChatMessageRoleUser,
		Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{
			Type: schemas.ChatContentBlockTypeInputAudio,
			InputAudio: &schemas.ChatInputAudio{
				Data: "UklGRg==", Format: schemas.Ptr("wav"),
			},
		}}},
	}

	_, err := ToBedrockChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.Bedrock,
		Model:    "anthropic.claude-sonnet-4-v1:0",
		Input:    []schemas.ChatMessage{audioMessage},
	})
	require.ErrorContains(t, err, "does not support audio message input")

	request, err := ToBedrockChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.Bedrock,
		Model:    "amazon.nova-2-sonic-v1:0",
		Input:    []schemas.ChatMessage{audioMessage},
	})
	require.NoError(t, err)
	require.Len(t, request.Messages, 1)
	require.NotNil(t, request.Messages[0].Content[0].Audio)

	responsesMessageType := schemas.ResponsesMessageTypeMessage
	responsesRole := schemas.ResponsesInputMessageRoleUser
	responsesInput := []schemas.ResponsesMessage{{
		Type: &responsesMessageType,
		Role: &responsesRole,
		Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
			Type: schemas.ResponsesInputMessageContentBlockTypeAudio,
			Audio: &schemas.ResponsesInputMessageContentBlockAudio{
				Data: "UklGRg==", Format: "wav",
			},
		}}},
	}}
	_, err = ToBedrockResponsesRequest(ctx, &schemas.BifrostResponsesRequest{
		Provider: schemas.Bedrock,
		Model:    "anthropic.claude-sonnet-4-v1:0",
		Input:    responsesInput,
	})
	require.ErrorContains(t, err, "does not support audio message input")

	responsesRequest, err := ToBedrockResponsesRequest(ctx, &schemas.BifrostResponsesRequest{
		Provider: schemas.Bedrock,
		Model:    "amazon.nova-2-sonic-v1:0",
		Input:    responsesInput,
	})
	require.NoError(t, err)
	require.Len(t, responsesRequest.Messages, 1)
	require.NotNil(t, responsesRequest.Messages[0].Content[0].Audio)
}

func TestBedrockInputAudioIsRejectedBeforeRemoteAssetsAreFetched(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("not reached"))
	}))
	defer server.Close()

	t.Run("chat reaches fetch path without rejecting audio", func(t *testing.T) {
		requests.Store(0)
		_, err := ToBedrockChatCompletionRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), &schemas.BifrostChatRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-sonnet-4-v1:0",
			Input: []schemas.ChatMessage{{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{
					Type: schemas.ChatContentBlockTypeImage, ImageURLStruct: &schemas.ChatInputImage{URL: server.URL + "/image.png"},
				}}},
			}},
		})
		require.ErrorContains(t, err, "blocked connection to non-public address")
		assert.Zero(t, requests.Load())
	})

	t.Run("chat rejects before fetch", func(t *testing.T) {
		requests.Store(0)
		_, err := ToBedrockChatCompletionRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), &schemas.BifrostChatRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-sonnet-4-v1:0",
			Input: []schemas.ChatMessage{{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{
					{Type: schemas.ChatContentBlockTypeImage, ImageURLStruct: &schemas.ChatInputImage{URL: server.URL + "/image.png"}},
					{Type: schemas.ChatContentBlockTypeInputAudio, InputAudio: &schemas.ChatInputAudio{Data: "UklGRg==", Format: schemas.Ptr("wav")}},
				}},
			}},
		})
		require.ErrorContains(t, err, "does not support audio message input")
		assert.Zero(t, requests.Load())
	})

	t.Run("responses reaches fetch path without rejecting audio", func(t *testing.T) {
		requests.Store(0)
		msgType := schemas.ResponsesMessageTypeMessage
		role := schemas.ResponsesInputMessageRoleUser
		_, err := ToBedrockResponsesRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), &schemas.BifrostResponsesRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-sonnet-4-v1:0",
			Input: []schemas.ResponsesMessage{{
				Type: &msgType,
				Role: &role,
				Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
					Type: schemas.ResponsesInputMessageContentBlockTypeImage,
					ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
						ImageURL: schemas.Ptr(server.URL + "/image.png"),
					},
				}}},
			}},
		})
		require.ErrorContains(t, err, "blocked connection to non-public address")
		assert.Zero(t, requests.Load())
	})

	t.Run("responses rejects before fetch", func(t *testing.T) {
		requests.Store(0)
		msgType := schemas.ResponsesMessageTypeMessage
		role := schemas.ResponsesInputMessageRoleUser
		_, err := ToBedrockResponsesRequest(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), &schemas.BifrostResponsesRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-sonnet-4-v1:0",
			Input: []schemas.ResponsesMessage{{
				Type: &msgType,
				Role: &role,
				Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{
					{
						Type: schemas.ResponsesInputMessageContentBlockTypeImage,
						ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
							ImageURL: schemas.Ptr(server.URL + "/image.png"),
						},
					},
					{
						Type:  schemas.ResponsesInputMessageContentBlockTypeAudio,
						Audio: &schemas.ResponsesInputMessageContentBlockAudio{Data: "UklGRg==", Format: "wav"},
					},
				}},
			}},
		})
		require.ErrorContains(t, err, "does not support audio message input")
		assert.Zero(t, requests.Load())
	})
}

func TestPreparedAssetsConvertWithoutSyntheticDataURL(t *testing.T) {
	t.Run("image", func(t *testing.T) {
		blocks, err := convertContentBlock(reviewRegressionTestModel, schemas.ChatContentBlock{
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
		blocks, err := convertContentBlock(reviewRegressionTestModel, schemas.ChatContentBlock{
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

	t.Run("resolved document wins over stale URL", func(t *testing.T) {
		staleURL := "ftp://example.com/stale.pdf"
		blocks, err := convertContentBlock(reviewRegressionTestModel, schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeFile,
			File: &schemas.ChatInputFile{
				FileURL:  &staleURL,
				Filename: schemas.Ptr("report.pdf"),
				ResolvedAsset: &schemas.ResolvedInputAsset{
					Data: "cmVzb2x2ZWQ=", MediaType: "application/pdf",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		require.NotNil(t, blocks[0].Document)
		assert.Equal(t, "cmVzb2x2ZWQ=", *blocks[0].Document.Source.Bytes)
	})
}

func TestBedrockDocumentConversionsPreserveCacheControl(t *testing.T) {
	ttl := "1h"
	cacheControl := &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral, TTL: &ttl}
	for _, tc := range []struct {
		name string
		file *schemas.ChatInputFile
	}{
		{
			name: "resolved asset",
			file: &schemas.ChatInputFile{Filename: schemas.Ptr("report.pdf"), ResolvedAsset: &schemas.ResolvedInputAsset{
				Data: "ZmlsZQ==", MediaType: "application/pdf",
			}},
		},
		{name: "s3", file: &schemas.ChatInputFile{FileURL: schemas.Ptr("s3://bucket/report.pdf")}},
		{name: "data URL", file: &schemas.ChatInputFile{FileData: schemas.Ptr("data:application/pdf;base64,ZmlsZQ==")}},
		{name: "inline data", file: &schemas.ChatInputFile{Filename: schemas.Ptr("report.pdf"), FileData: schemas.Ptr("ZmlsZQ==")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks, err := convertContentBlock(reviewRegressionTestModel, schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeFile, File: tc.file, CacheControl: cacheControl,
			})
			require.NoError(t, err)
			require.Len(t, blocks, 2)
			require.NotNil(t, blocks[0].Document)
			require.NotNil(t, blocks[1].CachePoint)
			require.NotNil(t, blocks[1].CachePoint.TTL)
			assert.Equal(t, ttl, *blocks[1].CachePoint.TTL)
		})
	}
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

	converted, err := convertBifrostMessageToBedrockMessage(context.Background(), reviewRegressionTestModel, &msg)
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

	_, err := convertBifrostMessageToBedrockMessage(context.Background(), reviewRegressionTestModel, &msg)
	require.ErrorContains(t, err, "content must not be blank or unsupported")
}

func TestResponsesFallbackMarkerIsTransparentToLeadingSystemState(t *testing.T) {
	assistantRole := schemas.ResponsesInputMessageRoleAssistant
	systemRole := schemas.ResponsesInputMessageRoleSystem
	userRole := schemas.ResponsesInputMessageRoleUser
	msgType := schemas.ResponsesMessageTypeMessage
	messages, system, err := ConvertBifrostMessagesToBedrockMessages(context.Background(), reviewRegressionTestModel, []schemas.ResponsesMessage{
		{
			Type: &msgType,
			Role: &assistantRole,
			Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type:                                  schemas.ResponsesOutputMessageContentTypeFallback,
				ResponsesOutputMessageContentFallback: &schemas.ResponsesOutputMessageContentFallback{FromModel: "old", ToModel: "new"},
			}}},
		},
		{Type: &msgType, Role: &systemRole, Content: &schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("leading system")}},
		{Type: &msgType, Role: &userRole, Content: &schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("hello")}},
	}, true)
	require.NoError(t, err)
	require.Len(t, system, 1)
	require.Equal(t, "leading system", *system[0].Text)
	require.Len(t, messages, 1)
	require.Equal(t, BedrockMessageRoleUser, messages[0].Role)
	require.Equal(t, "hello", *messages[0].Content[0].Text)
}

func TestResponsesContentConverterReportsDroppableVersusUnknown(t *testing.T) {
	fallback := schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
		Type: schemas.ResponsesOutputMessageContentTypeFallback,
	}}}
	conversion, err := convertBifrostResponsesMessageContentBlocksToBedrockContentBlocksWithDisposition(context.Background(), reviewRegressionTestModel, fallback)
	require.NoError(t, err)
	assert.Equal(t, bedrockResponsesContentIntentionallySkipped, conversion.Disposition)

	unknown := schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
		Type: schemas.ResponsesMessageContentBlockType("future_unknown"),
	}}}
	conversion, err = convertBifrostResponsesMessageContentBlocksToBedrockContentBlocksWithDisposition(context.Background(), reviewRegressionTestModel, unknown)
	require.NoError(t, err)
	assert.Equal(t, bedrockResponsesContentUnsupported, conversion.Disposition)

	var zeroValue bedrockResponsesContentConversion
	assert.Equal(t, bedrockResponsesContentUnknown, zeroValue.Disposition)
}

func TestResponsesAbsentContentAlwaysReturnsError(t *testing.T) {
	role := schemas.ResponsesInputMessageRoleAssistant
	for _, content := range []*schemas.ResponsesMessageContent{nil, &schemas.ResponsesMessageContent{}} {
		_, disposition, err := convertBifrostMessageToBedrockMessageWithDisposition(context.Background(), reviewRegressionTestModel, &schemas.ResponsesMessage{
			Role: &role, Content: content,
		})
		require.ErrorContains(t, err, "content must not be blank or unsupported")
		assert.Equal(t, bedrockResponsesContentAbsent, disposition)
	}
}

func TestResponsesMessageConverterRejectsMissingRoleWithoutPanicking(t *testing.T) {
	for _, msg := range []*schemas.ResponsesMessage{nil, {}} {
		_, disposition, err := convertBifrostMessageToBedrockMessageWithDisposition(context.Background(), reviewRegressionTestModel, msg)
		require.Error(t, err)
		assert.Equal(t, bedrockResponsesContentAbsent, disposition)
	}
}

func TestResponsesBlankToolCallIDsReturnErrors(t *testing.T) {
	for _, msgType := range []schemas.ResponsesMessageType{
		schemas.ResponsesMessageTypeFunctionCall,
		schemas.ResponsesMessageTypeFunctionCallOutput,
	} {
		t.Run(string(msgType), func(t *testing.T) {
			_, _, err := ConvertBifrostMessagesToBedrockMessages(context.Background(), reviewRegressionTestModel, []schemas.ResponsesMessage{{
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
	messages, _, err := ConvertBifrostMessagesToBedrockMessages(context.Background(), reviewRegressionTestModel, []schemas.ResponsesMessage{{
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
	originalToolResult := &BedrockToolResult{
		ToolUseID: "tooluse_1",
		Content:   []BedrockContentBlock{{CachePoint: &BedrockCachePoint{Type: BedrockCachePointTypeDefault}}},
	}
	input := []BedrockContentBlock{{ToolResult: originalToolResult}}
	blocks := injectContentBlockCachePoints(input, nil, "messages.0.content")

	require.Len(t, blocks, 2)
	require.NotNil(t, blocks[0].ToolResult)
	assert.NotSame(t, originalToolResult, blocks[0].ToolResult)
	require.Len(t, blocks[0].ToolResult.Content, 1)
	assert.JSONEq(t, `{}`, string(blocks[0].ToolResult.Content[0].JSON))
	require.NotNil(t, blocks[1].CachePoint)
	require.Len(t, originalToolResult.Content, 1)
	require.NotNil(t, originalToolResult.Content[0].CachePoint, "cache-point injection must not mutate the caller's ToolResult")

	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"content":[]`)
}

func TestEmptyToolResultPlaceholderIsFresh(t *testing.T) {
	first := ensureBedrockToolResultContent(nil)
	second := ensureBedrockToolResultContent(nil)
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	first[0].JSON[0] = '['
	assert.JSONEq(t, `{}`, string(second[0].JSON))
}

func TestUnsupportedChatFileURLReportsScheme(t *testing.T) {
	fileURL := "ftp://example.com/report.pdf"
	_, err := convertContentBlock(reviewRegressionTestModel, schemas.ChatContentBlock{
		Type: schemas.ChatContentBlockTypeFile,
		File: &schemas.ChatInputFile{FileURL: &fileURL, FileType: schemas.Ptr("application/pdf")},
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unsupported URL scheme") && strings.Contains(err.Error(), `"ftp"`), err.Error())
}
