package bifrost

import (
	"fmt"
	"slices"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

// enforceSerialToolConstraintOnAttempt runs inside the worker's per-attempt
// retry closure, after key selection and alias resolution, so it evaluates
// exactly the wire model and ResolvedAlias the provider will see (a pre-flight
// check would have to shadow key selection and alias resolution and inevitably
// drift from them). Transports whose downstream consumer accepts only one tool
// call per turn opt into the policy via the reserved
// BifrostContextKeyRequireSerialToolCalls flag without coupling core to an
// integration name.
func enforceSerialToolConstraintOnAttempt(
	ctx *schemas.BifrostContext,
	provider, baseProvider schemas.ModelProvider,
	requestedModel, resolvedModel string,
	req *schemas.BifrostRequest,
) *schemas.BifrostError {
	requireSerial, _ := ctx.Value(schemas.BifrostContextKeyRequireSerialToolCalls).(bool)
	if !requireSerial || req == nil || req.ChatRequest == nil || req.ChatRequest.Params == nil || len(req.ChatRequest.Params.Tools) == 0 {
		return nil
	}

	if !providerSupportsSingleToolControl(ctx, provider, baseProvider, resolvedModel) {
		detail := ""
		if resolvedModel != requestedModel {
			detail = fmt.Sprintf(" (key alias resolves to %q)", resolvedModel)
		}
		return providerUtils.NewBifrostBadRequestError(fmt.Sprintf(
			"provider %q model %q cannot guarantee serial tool execution%s",
			provider,
			requestedModel,
			detail,
		))
	}

	req.ChatRequest.Params.ParallelToolCalls = schemas.Ptr(false)
	return nil
}

func providerSupportsSingleToolControl(ctx *schemas.BifrostContext, provider, baseProvider schemas.ModelProvider, model string) bool {
	// Anthropic's Messages wire format expresses the inverse setting as
	// tool_choice.disable_parallel_tool_use. These providers all use the shared
	// Anthropic request builder for Anthropic-family models.
	if baseProvider == schemas.Anthropic ||
		((baseProvider == schemas.Azure || baseProvider == schemas.Vertex || baseProvider == schemas.BedrockMantle) &&
			schemas.IsAnthropicModelFamily(ctx, model)) {
		return true
	}

	if !usesParallelToolCallsWire(ctx, baseProvider, model) {
		return false
	}

	modelInfo := ctx.GetModelInfo(provider, model)
	if modelInfo == nil && baseProvider != provider {
		modelInfo = ctx.GetModelInfo(baseProvider, model)
	}
	// An absent catalog entry is common for self-hosted and custom providers.
	// Their OpenAI-compatible wire still supports parallel_tool_calls=false.
	return modelInfo == nil || slices.Contains(modelInfo.SupportedParameters, "parallel_tool_calls")
}

func usesParallelToolCallsWire(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.OpenAI,
		schemas.Azure,
		schemas.BedrockMantle,
		schemas.Cerebras,
		schemas.DeepSeek,
		schemas.Fireworks,
		schemas.Groq,
		schemas.Mistral,
		schemas.Nebius,
		schemas.Ollama,
		schemas.OpencodeGo,
		schemas.OpencodeZen,
		schemas.OpenRouter,
		schemas.Parasail,
		schemas.Perplexity,
		schemas.Sarvam,
		schemas.SGL,
		schemas.VLLM,
		schemas.Wafer,
		schemas.XAI:
		return true
	case schemas.Bedrock:
		return schemas.IsOpenAIModelFamily(ctx, model) || strings.Contains(schemas.ResolveCanonicalModel(ctx, model), "gemma-4")
	case schemas.Vertex:
		// Mirrors the Vertex chat routing predicate: Gemini- and Gemma-family
		// names and all-digit fine-tuned endpoint IDs use the Gemini request
		// builder, which has no parallel_tool_calls field.
		return !schemas.IsGeminiModelFamily(ctx, model) && !schemas.IsAllDigitsASCII(model) && !schemas.IsGemmaModelFamily(ctx, model)
	default:
		return false
	}
}
