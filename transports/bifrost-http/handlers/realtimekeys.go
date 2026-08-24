package handlers

import (
	"errors"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const (
	realtimeKeySelectionUnavailableMessage = bifrost.KeySelectionUnavailableClientMessage
	realtimeKeySelectionInvalidMessage     = bifrost.KeySelectionInvalidClientMessage
)

func classifyRealtimeKeySelectionError(err error, mappedEphemeralCredential bool) (int, string, string) {
	if errors.Is(err, errRealtimeEphemeralKeyUnknown) ||
		(mappedEphemeralCredential &&
			(errors.Is(err, bifrost.ErrPinnedAPIKeyUnavailable) || errors.Is(err, bifrost.ErrPinnedAPIKeyIneligible))) {
		return fasthttp.StatusUnauthorized, "invalid_request_error", errRealtimeEphemeralKeyUnknown.Error()
	}
	return classifyKeySelectionError(err)
}

func classifyKeySelectionError(err error) (int, string, string) {
	if errors.Is(err, bifrost.ErrKeySelectionUnavailable) {
		return fasthttp.StatusServiceUnavailable, "server_error", realtimeKeySelectionUnavailableMessage
	}
	return fasthttp.StatusBadRequest, "invalid_request_error", realtimeKeySelectionInvalidMessage
}

func logKeySelectionFailure(flow string, provider schemas.ModelProvider, model string, mappedEphemeralCredential bool, status int, err error) {
	if status >= fasthttp.StatusInternalServerError {
		logger.Warn("%s key selection failed: provider=%s model=%s mapped_ephemeral=%t status=%d error=%v", flow, provider, model, mappedEphemeralCredential, status, err)
		return
	}
	logger.Debug("%s key selection rejected: provider=%s model=%s mapped_ephemeral=%t status=%d error=%v", flow, provider, model, mappedEphemeralCredential, status, err)
}
