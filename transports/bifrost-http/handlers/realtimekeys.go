package handlers

import (
	"errors"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/valyala/fasthttp"
)

const realtimeKeySelectionUnavailableMessage = "provider credentials are temporarily unavailable"

func classifyRealtimeKeySelectionError(err error, mappedEphemeralCredential bool) (int, string, string) {
	if errors.Is(err, errRealtimeEphemeralKeyUnknown) ||
		(mappedEphemeralCredential && errors.Is(err, bifrost.ErrPinnedAPIKeyUnavailable)) {
		return fasthttp.StatusUnauthorized, "invalid_request_error", errRealtimeEphemeralKeyUnknown.Error()
	}
	if errors.Is(err, bifrost.ErrKeySelectionUnavailable) {
		return fasthttp.StatusInternalServerError, "server_error", realtimeKeySelectionUnavailableMessage
	}
	return fasthttp.StatusBadRequest, "invalid_request_error", err.Error()
}
