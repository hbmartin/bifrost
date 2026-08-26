// Package governance provides utility functions for the governance plugin
package governance

import (
	"context"
	"fmt"
	"math"
	"strings"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
)

// ParseVirtualKeyFromFastHTTPRequest parses the virtual key from FastHTTP request headers.
// Parameters:
//   - req: The FastHTTP request containing headers to parse
//
// Returns:
//   - *string: The virtual key if found, nil otherwise
func ParseVirtualKeyFromFastHTTPRequest(req *fasthttp.RequestCtx) *string {
	vkHeader := string(req.Request.Header.Peek("x-bf-vk"))
	if vkHeader != "" && strings.HasPrefix(strings.ToLower(vkHeader), VirtualKeyPrefix) {
		return bifrost.Ptr(vkHeader)
	}
	authHeader := string(req.Request.Header.Peek("Authorization"))
	if authHeader != "" {
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			authHeaderValue := strings.TrimSpace(authHeader[7:]) // Remove "Bearer " prefix
			if authHeaderValue != "" && strings.HasPrefix(strings.ToLower(authHeaderValue), VirtualKeyPrefix) {
				return bifrost.Ptr(authHeaderValue)
			}
		}
	}
	xAPIKey := string(req.Request.Header.Peek("x-api-key"))
	if xAPIKey != "" && strings.HasPrefix(strings.ToLower(xAPIKey), VirtualKeyPrefix) {
		return bifrost.Ptr(xAPIKey)
	}
	xGoogleAPIKey := string(req.Request.Header.Peek("x-goog-api-key"))
	if xGoogleAPIKey != "" && strings.HasPrefix(strings.ToLower(xGoogleAPIKey), VirtualKeyPrefix) {
		return bifrost.Ptr(xGoogleAPIKey)
	}
	azureAPIKey := string(req.Request.Header.Peek("api-key"))
	if azureAPIKey != "" && strings.HasPrefix(strings.ToLower(azureAPIKey), VirtualKeyPrefix) {
		return bifrost.Ptr(azureAPIKey)
	}
	return nil
}

// IsModelRequiredForRequest checks if the requested model is required for this request
func IsModelRequiredForRequest(requestType schemas.RequestType) bool {
	// Here we will have to check for some requests which do not need model
	// For example, batches, container, files, videos, passthrough requests
	// For these requests, we will only check for provider filtering
	// Cached content list/retrieve/update/delete target a resource name (cachedContents/{id}),
	// not a model, so they carry no model to filter on; only create binds a cache to a model.
	// Responses retrieve/delete/cancel/input_items target a response_id, not a model.
	// Video edit's model is optional too — the OpenAI SDKs send none and the provider infers it from
	// the source video — so it is evaluated only when the caller supplies one, same as passthrough.
	if requestType == schemas.ListModelsRequest || requestType == schemas.MCPToolExecutionRequest || requestType == schemas.BatchCreateRequest || requestType == schemas.BatchListRequest || requestType == schemas.BatchRetrieveRequest || requestType == schemas.BatchCancelRequest || requestType == schemas.BatchResultsRequest || requestType == schemas.FileUploadRequest || requestType == schemas.FileListRequest || requestType == schemas.FileRetrieveRequest || requestType == schemas.FileDeleteRequest || requestType == schemas.FileContentRequest || requestType == schemas.ContainerCreateRequest || requestType == schemas.ContainerListRequest || requestType == schemas.ContainerRetrieveRequest || requestType == schemas.ContainerDeleteRequest || requestType == schemas.ContainerFileCreateRequest || requestType == schemas.ContainerFileListRequest || requestType == schemas.ContainerFileRetrieveRequest || requestType == schemas.ContainerFileContentRequest || requestType == schemas.ContainerFileDeleteRequest || requestType == schemas.CachedContentListRequest || requestType == schemas.CachedContentRetrieveRequest || requestType == schemas.CachedContentUpdateRequest || requestType == schemas.CachedContentDeleteRequest || requestType == schemas.ResponsesRetrieveRequest || requestType == schemas.ResponsesRetrieveStreamRequest || requestType == schemas.ResponsesDeleteRequest || requestType == schemas.ResponsesCancelRequest || requestType == schemas.ResponsesInputItemsRequest || requestType == schemas.VideoRetrieveRequest || requestType == schemas.VideoDownloadRequest || requestType == schemas.VideoListRequest || requestType == schemas.VideoDeleteRequest || requestType == schemas.VideoRemixRequest || requestType == schemas.VideoEditRequest || requestType == schemas.PassthroughRequest || requestType == schemas.PassthroughStreamRequest {
		return false
	}
	return true
}

// parseVirtualKeyFromHTTPRequest parses the virtual key from HTTP request headers.
// It checks multiple headers in order: x-bf-vk, Authorization (Bearer token), x-api-key, and x-goog-api-key.
// Parameters:
//   - req: The HTTP request containing headers to parse
//
// Returns:
//   - *string: The virtual key if found, nil otherwise
func parseVirtualKeyFromHTTPRequest(req *schemas.HTTPRequest) *string {
	var virtualKeyValue string
	vkHeader := req.CaseInsensitiveHeaderLookup("x-bf-vk")
	if vkHeader != "" && strings.HasPrefix(strings.ToLower(vkHeader), VirtualKeyPrefix) {
		return new(vkHeader)
	}
	authHeader := req.CaseInsensitiveHeaderLookup("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			authHeaderValue := strings.TrimSpace(authHeader[7:]) // Remove "Bearer " prefix
			if authHeaderValue != "" && strings.HasPrefix(strings.ToLower(authHeaderValue), VirtualKeyPrefix) {
				virtualKeyValue = authHeaderValue
			}
		}
	}
	if virtualKeyValue != "" {
		return new(virtualKeyValue)
	}
	xAPIKey := req.CaseInsensitiveHeaderLookup("x-api-key")
	if xAPIKey != "" && strings.HasPrefix(strings.ToLower(xAPIKey), VirtualKeyPrefix) {
		return new(xAPIKey)
	}
	// Checking x-goog-api-key header
	xGoogleAPIKey := req.CaseInsensitiveHeaderLookup("x-goog-api-key")
	if xGoogleAPIKey != "" && strings.HasPrefix(strings.ToLower(xGoogleAPIKey), VirtualKeyPrefix) {
		return new(xGoogleAPIKey)
	}
	return nil
}

// selectWeightedProviderConfigAt selects a primary provider from finite,
// strictly positive weights. Nil and zero weights do not participate in primary
// selection; zero weights remain eligible for the generated fallback chain.
//
// Keep this algorithm in lockstep with core/keyselectors.SelectPositiveWeightedAt.
// The governance module intentionally remains buildable against released core
// v1.7.11, which predates that exported generic helper; importing it here would
// make the standalone plugin module depend on an unreleased core version. Remove
// this local copy once governance can advance its minimum core dependency.
func selectWeightedProviderConfigAt(configs []configstoreTables.TableVirtualKeyProviderConfig, unitRandom float64) (configstoreTables.TableVirtualKeyProviderConfig, bool) {
	var zero configstoreTables.TableVirtualKeyProviderConfig
	totalWeight := 0.0
	maxWeight := 0.0
	lastEligible := -1
	for i := range configs {
		if configs[i].Weight == nil {
			continue
		}
		weight := *configs[i].Weight
		if !isPositiveFiniteProviderWeight(weight) {
			continue
		}
		totalWeight += weight
		if weight > maxWeight {
			maxWeight = weight
		}
		lastEligible = i
	}
	if lastEligible < 0 {
		return zero, false
	}

	if !math.IsInf(totalWeight, 1) {
		randomValue := unitRandom * totalWeight
		currentWeight := 0.0
		for i := range configs {
			if configs[i].Weight == nil || !isPositiveFiniteProviderWeight(*configs[i].Weight) {
				continue
			}
			currentWeight += *configs[i].Weight
			if randomValue < currentWeight {
				return configs[i], true
			}
		}
		return configs[lastEligible], true
	}

	// Normalize only the overflow path so even MaxFloat64-sized weights keep
	// their intended proportions without changing the ordinary hot path.
	totalWeight = 0
	for i := range configs {
		if configs[i].Weight != nil && isPositiveFiniteProviderWeight(*configs[i].Weight) {
			totalWeight += *configs[i].Weight / maxWeight
		}
	}
	randomValue := unitRandom * totalWeight
	currentWeight := 0.0
	for i := range configs {
		if configs[i].Weight == nil || !isPositiveFiniteProviderWeight(*configs[i].Weight) {
			continue
		}
		currentWeight += *configs[i].Weight / maxWeight
		if randomValue < currentWeight {
			return configs[i], true
		}
	}
	return configs[lastEligible], true
}

func isPositiveFiniteProviderWeight(weight float64) bool {
	return weight > 0 && !math.IsInf(weight, 0)
}

// providerFallbackConfigs returns assigned, non-negative finite provider
// weights. Zero-weight providers are excluded from primary traffic while a
// positive alternative exists and remain generated fallbacks.
func providerFallbackConfigs(configs []configstoreTables.TableVirtualKeyProviderConfig) []configstoreTables.TableVirtualKeyProviderConfig {
	fallbacks := make([]configstoreTables.TableVirtualKeyProviderConfig, 0, len(configs))
	for _, config := range configs {
		if config.Weight == nil {
			continue
		}
		weight := *config.Weight
		if weight == 0 || isPositiveFiniteProviderWeight(weight) {
			fallbacks = append(fallbacks, config)
		}
	}
	return fallbacks
}

func assignedProviderWeightCount(configs []configstoreTables.TableVirtualKeyProviderConfig) int {
	assigned := 0
	for i := range configs {
		if configs[i].Weight != nil {
			assigned++
		}
	}
	return assigned
}

// stampGovernanceCtxFromVK copies team/customer identifiers from the VK onto ctx so
// downstream plugins (logging, observability) see the governance scope.
func stampGovernanceCtxFromVK(ctx *schemas.BifrostContext, vk *configstoreTables.TableVirtualKey) {
	if vk == nil {
		return
	}
	if vk.TeamID != nil {
		ctx.SetValue(schemas.BifrostContextKeyGovernanceTeamID, *vk.TeamID)
	}
	if vk.Team != nil {
		ctx.SetValue(schemas.BifrostContextKeyGovernanceTeamName, vk.Team.Name)
		if vk.Team.CustomerID != nil {
			ctx.SetValue(schemas.BifrostContextKeyGovernanceCustomerID, *vk.Team.CustomerID)
			if vk.Team.Customer != nil {
				ctx.SetValue(schemas.BifrostContextKeyGovernanceCustomerName, vk.Team.Customer.Name)
			}
		}
	} else {
		if vk.CustomerID != nil {
			ctx.SetValue(schemas.BifrostContextKeyGovernanceCustomerID, *vk.CustomerID)
		}
		if vk.Customer != nil {
			ctx.SetValue(schemas.BifrostContextKeyGovernanceCustomerName, vk.Customer.Name)
		}
	}
}

// filterModelsForVirtualKey filters models based on virtual key's provider configs
// Returns only models that are allowed by the virtual key's ProviderConfigs
func (p *GovernancePlugin) filterModelsForVirtualKey(
	ctx context.Context,
	models []schemas.Model,
	virtualKeyValue string,
) []schemas.Model {
	// Get virtual key configuration
	vk, exists := p.store.GetVirtualKey(ctx, virtualKeyValue)
	if !exists {
		p.logger.Warn("[Governance] Virtual key not found for list models filtering: %s", virtualKeyValue)
		return []schemas.Model{} // VK not found, return empty list
	}

	// Empty ProviderConfigs means no models are allowed (deny-by-default)
	if len(vk.ProviderConfigs) == 0 {
		return []schemas.Model{}
	}

	// Filter models based on ProviderConfigs
	filteredModels := make([]schemas.Model, 0, len(models))
	for _, model := range models {
		provider, modelName := schemas.ParseModelString(model.ID, "")

		// Pre-pass: if any matching config blacklists the model, block it entirely.
		isBlocked := false
		for _, pc := range vk.ProviderConfigs {
			if pc.Provider == string(provider) && pc.BlacklistedModels.IsBlocked(modelName) {
				isBlocked = true
				break
			}
		}
		if isBlocked {
			continue
		}

		// Allowlist check — model is allowed if any matching config permits it.
		isAllowed := false
		for _, pc := range vk.ProviderConfigs {
			if pc.Provider == string(provider) {
				if p.modelCatalog != nil && p.inMemoryStore != nil {
					providerConfig, ok := p.inMemoryStore.GetConfiguredProviders()[provider]
					providerConfigPtr := &providerConfig
					if !ok {
						providerConfigPtr = nil
					}
					if p.modelCatalog.IsModelAllowedForProvider(provider, modelName, providerConfigPtr, pc.AllowedModels) {
						isAllowed = true
						break
					}
				} else {
					if pc.AllowedModels.IsAllowed(modelName) {
						isAllowed = true
						break
					}
				}
			}
		}

		if isAllowed {
			filteredModels = append(filteredModels, model)
		}
	}

	return filteredModels
}

// validateRequiredHeaders checks that all configured required headers are present in the request.
// Headers are compared case-insensitively (both sides lowercased).
// Returns a BifrostError with status 400 if any required headers are missing, or nil if all present.
func (p *GovernancePlugin) validateRequiredHeaders(ctx *schemas.BifrostContext) *schemas.BifrostError {
	if p.requiredHeaders == nil || len(*p.requiredHeaders) == 0 {
		return nil
	}
	headers, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	if headers == nil {
		headers = map[string]string{}
	}
	var missing []string
	for _, h := range *p.requiredHeaders {
		if _, ok := headers[strings.ToLower(h)]; !ok {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		return &schemas.BifrostError{
			Type:       bifrost.Ptr("missing_required_headers"),
			StatusCode: bifrost.Ptr(400),
			Error: &schemas.ErrorField{
				Message: fmt.Sprintf("missing required headers: %s", strings.Join(missing, ", ")),
			},
		}
	}
	return nil
}
