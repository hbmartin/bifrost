package bedrock

import (
	"context"
	"fmt"
	"strings"
	"testing"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

// TestToolResultStatusFromIsError verifies that a tool message carrying
// IsError converts to a Converse toolResult with status "error", and that
// non-error results keep the "success" default. Before IsError existed on
// ChatToolMessage the status was hard-coded to "success", so failed tool
// calls replayed through Bedrock looked successful to the model.
func TestToolResultStatusFromIsError(t *testing.T) {
	msgs := []schemas.ChatMessage{
		{
			Role:            schemas.ChatMessageRoleTool,
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("toolu_failed"), IsError: schemas.Ptr(true)},
			Content:         &schemas.ChatMessageContent{ContentStr: schemas.Ptr("command exited with code 1")},
		},
		{
			Role:            schemas.ChatMessageRoleTool,
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("toolu_ok")},
			Content:         &schemas.ChatMessageContent{ContentStr: schemas.Ptr("done")},
		},
	}

	converted, err := convertToolMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("convert tool messages: %v", err)
	}

	var results []*BedrockToolResult
	for _, block := range converted.Content {
		if block.ToolResult != nil {
			results = append(results, block.ToolResult)
		}
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 toolResult blocks, got %d", len(results))
	}

	if results[0].ToolUseID != "toolu_failed" {
		t.Fatalf("expected first toolResult for toolu_failed, got %s", results[0].ToolUseID)
	}
	if results[0].Status == nil || *results[0].Status != "error" {
		t.Fatalf("failed tool call must map to status \"error\", got %v", results[0].Status)
	}
	if results[1].Status == nil || *results[1].Status != "success" {
		t.Fatalf("non-error tool call must keep status \"success\", got %v", results[1].Status)
	}
}

func TestToolResultWithoutContentStillEmitsResult(t *testing.T) {
	converted, err := convertToolMessages(context.Background(), []schemas.ChatMessage{
		{
			Role:            schemas.ChatMessageRoleTool,
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("toolu_void")},
		},
	})
	if err != nil {
		t.Fatalf("convert content-less tool message: %v", err)
	}
	if len(converted.Content) != 1 || converted.Content[0].ToolResult == nil {
		t.Fatalf("content-less tool message must emit one toolResult, got %#v", converted.Content)
	}
	result := converted.Content[0].ToolResult
	if result.ToolUseID != "toolu_void" {
		t.Fatalf("expected toolu_void, got %q", result.ToolUseID)
	}
	if len(result.Content) != 1 || string(result.Content[0].JSON) != `{}` {
		t.Fatalf("expected empty JSON tool-result content, got %#v", result.Content)
	}

	wire, err := providerUtils.MarshalSorted(result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	if strings.Contains(string(wire), `"content":null`) || !strings.Contains(string(wire), `"content":[{"json":{}}]`) {
		t.Fatalf("content-less tool result must carry a valid content array, got %s", wire)
	}
}

func TestBlankToolResultContentUsesEmptyJSON(t *testing.T) {
	for _, content := range []string{"", " \t\n "} {
		t.Run(fmt.Sprintf("content_%q", content), func(t *testing.T) {
			converted, err := convertToolMessages(context.Background(), []schemas.ChatMessage{
				{
					Role:            schemas.ChatMessageRoleTool,
					ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("toolu_blank")},
					Content:         &schemas.ChatMessageContent{ContentStr: &content},
				},
			})
			if err != nil {
				t.Fatalf("convert blank tool result: %v", err)
			}
			result := converted.Content[0].ToolResult
			if len(result.Content) != 1 || string(result.Content[0].JSON) != `{}` {
				t.Fatalf("expected empty JSON tool-result content, got %#v", result.Content)
			}
		})
	}
}

func TestToolResultContentBlocksUseSharedConverter(t *testing.T) {
	converted, err := convertToolMessages(context.Background(), []schemas.ChatMessage{
		{
			Role:            schemas.ChatMessageRoleTool,
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("toolu_blocks")},
			Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{
				{
					Type: schemas.ChatContentBlockTypeFile,
					File: &schemas.ChatInputFile{
						Filename: schemas.Ptr("result.pdf"),
						FileType: schemas.Ptr("application/pdf"),
						FileData: schemas.Ptr("cGRm"),
					},
				},
				{CachePoint: &schemas.CachePoint{}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("convert tool-result blocks: %v", err)
	}

	if len(converted.Content) != 2 || converted.Content[0].ToolResult == nil || converted.Content[1].CachePoint == nil {
		t.Fatalf("expected tool result and sibling cache point, got %#v", converted.Content)
	}
	content := converted.Content[0].ToolResult.Content
	if len(content) != 2 || content[0].Text == nil || content[1].Document == nil {
		t.Fatalf("expected placeholder text and document inside the tool result, got %#v", content)
	}
	if strings.TrimSpace(*content[0].Text) == "" {
		t.Fatalf("document placeholder must be usable text, got %q", *content[0].Text)
	}
}

func TestToolResultHTTPFileURLUsesSharedSafeFetcher(t *testing.T) {
	fileURL := "http://127.0.0.1:1/result.pdf"
	_, err := convertToolMessages(context.Background(), []schemas.ChatMessage{{
		Role:            schemas.ChatMessageRoleTool,
		ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("toolu_url")},
		Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{
			Type: schemas.ChatContentBlockTypeFile,
			File: &schemas.ChatInputFile{FileURL: &fileURL},
		}}},
	}})
	if err == nil {
		t.Fatal("expected the SSRF-safe fetcher to block a loopback URL")
	}
	if strings.Contains(err.Error(), "HTTP(S) file URLs are not supported") {
		t.Fatalf("tool-result file URL was rejected before shared conversion: %v", err)
	}
}

func TestAssistantMetadataOnlyMessagesConvertUsableText(t *testing.T) {
	tests := []struct {
		name    string
		message schemas.ChatMessage
		want    string
	}{
		{
			name: "refusal",
			message: schemas.ChatMessage{Role: schemas.ChatMessageRoleAssistant, ChatAssistantMessage: &schemas.ChatAssistantMessage{
				Refusal: schemas.Ptr("I cannot help with that."),
			}},
			want: "I cannot help with that.",
		},
		{
			name: "audio transcript",
			message: schemas.ChatMessage{Role: schemas.ChatMessageRoleAssistant, ChatAssistantMessage: &schemas.ChatAssistantMessage{
				Audio: &schemas.ChatAudioMessageAudio{Transcript: "Spoken answer"},
			}},
			want: "Spoken answer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			messages, _, err := convertMessages(context.Background(), []schemas.ChatMessage{tc.message})
			if err != nil {
				t.Fatalf("convert assistant metadata: %v", err)
			}
			if len(messages) != 1 || len(messages[0].Content) != 1 || messages[0].Content[0].Text == nil || *messages[0].Content[0].Text != tc.want {
				t.Fatalf("converted assistant metadata = %#v, want text %q", messages, tc.want)
			}
		})
	}
}

func TestAssistantReasoningAliasAndSummaryConvert(t *testing.T) {
	tests := []schemas.ChatAssistantMessage{
		{Reasoning: schemas.Ptr("programmatic reasoning")},
		{ReasoningDetails: []schemas.ChatReasoningDetails{{Type: schemas.BifrostReasoningDetailsTypeSummary, Summary: schemas.Ptr("summary reasoning")}}},
	}
	for _, assistant := range tests {
		converted, err := convertMessage(context.Background(), schemas.ChatMessage{
			Role:                 schemas.ChatMessageRoleAssistant,
			ChatAssistantMessage: &assistant,
		})
		if err != nil {
			t.Fatalf("convert reasoning-only assistant message: %v", err)
		}
		if len(converted.Content) != 1 || converted.Content[0].ReasoningContent == nil || converted.Content[0].ReasoningContent.ReasoningText == nil {
			t.Fatalf("expected reasoning content, got %#v", converted.Content)
		}
	}
}

func TestAnnotationOnlyAssistantMessageReturnsExplicitError(t *testing.T) {
	_, err := convertMessage(context.Background(), schemas.ChatMessage{
		Role: schemas.ChatMessageRoleAssistant,
		ChatAssistantMessage: &schemas.ChatAssistantMessage{
			Annotations: []schemas.ChatAssistantMessageAnnotation{{Type: "url_citation"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "annotations without Bedrock-convertible content") {
		t.Fatalf("expected explicit annotation-only error, got %v", err)
	}
}

func TestResponsesRefusalOnlyMessageConvertsToText(t *testing.T) {
	role := schemas.ResponsesInputMessageRoleAssistant
	converted, err := convertBifrostMessageToBedrockMessage(context.Background(), &schemas.ResponsesMessage{
		Role: &role,
		Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{{
			Type: schemas.ResponsesOutputMessageContentTypeRefusal,
			ResponsesOutputMessageContentRefusal: &schemas.ResponsesOutputMessageContentRefusal{
				Refusal: "I cannot help with that.",
			},
		}}},
	})
	if err != nil {
		t.Fatalf("convert refusal-only Responses message: %v", err)
	}
	if converted == nil || len(converted.Content) != 1 || converted.Content[0].Text == nil || *converted.Content[0].Text != "I cannot help with that." {
		t.Fatalf("expected refusal text, got %#v", converted)
	}
}

func TestBlankResponsesMessageReturnsError(t *testing.T) {
	role := schemas.ResponsesInputMessageRoleAssistant
	_, err := convertBifrostMessageToBedrockMessage(context.Background(), &schemas.ResponsesMessage{
		Role:    &role,
		Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{}},
	})
	if err == nil || !strings.Contains(err.Error(), "content must not be blank or unsupported") {
		t.Fatalf("expected blank Responses message error, got %v", err)
	}
}

func TestBlankRegularMessageContentReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		message schemas.ChatMessage
	}{
		{name: "nil user content", message: schemas.ChatMessage{Role: schemas.ChatMessageRoleUser}},
		{name: "blank user text", message: schemas.ChatMessage{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(" \t\n ")}}},
		{name: "empty assistant blocks", message: schemas.ChatMessage{Role: schemas.ChatMessageRoleAssistant, Content: &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{}}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := convertMessages(context.Background(), []schemas.ChatMessage{tc.message})
			if err == nil || !strings.Contains(err.Error(), "message content must not be blank") {
				t.Fatalf("expected blank regular-message error, got %v", err)
			}
		})
	}
}

func TestNilSystemMessageContentReturnsError(t *testing.T) {
	_, _, err := convertMessages(context.Background(), []schemas.ChatMessage{{Role: schemas.ChatMessageRoleSystem}})
	if err == nil || !strings.Contains(err.Error(), "system message missing required content") {
		t.Fatalf("expected missing system content error, got %v", err)
	}
}

func TestMalformedToolMessagesReturnErrors(t *testing.T) {
	tests := []struct {
		name    string
		message schemas.ChatMessage
		want    string
	}{
		{
			name:    "missing tool message",
			message: schemas.ChatMessage{Role: schemas.ChatMessageRoleTool},
			want:    "missing required ChatToolMessage",
		},
		{
			name: "missing tool call id",
			message: schemas.ChatMessage{
				Role:            schemas.ChatMessageRoleTool,
				ChatToolMessage: &schemas.ChatToolMessage{},
			},
			want: "missing required ToolCallID",
		},
		{
			name: "empty tool call id",
			message: schemas.ChatMessage{
				Role:            schemas.ChatMessageRoleTool,
				ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr("  ")},
			},
			want: "missing required ToolCallID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := convertToolMessages(context.Background(), []schemas.ChatMessage{tc.message})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBlankAssistantToolCallIDReturnsError(t *testing.T) {
	for _, id := range []*string{nil, schemas.Ptr("  ")} {
		_, err := convertMessage(context.Background(), schemas.ChatMessage{
			Role: schemas.ChatMessageRoleAssistant,
			ChatAssistantMessage: &schemas.ChatAssistantMessage{ToolCalls: []schemas.ChatAssistantMessageToolCall{{
				ID: id,
				Function: schemas.ChatAssistantMessageToolCallFunction{
					Name:      schemas.Ptr("lookup"),
					Arguments: `{}`,
				},
			}}},
		})
		if err == nil || !strings.Contains(err.Error(), "assistant tool call missing required ID") {
			t.Fatalf("expected blank assistant tool-call ID error, got %v", err)
		}
	}
}
