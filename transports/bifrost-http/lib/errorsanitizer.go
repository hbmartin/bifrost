package lib

import (
	"context"
	"errors"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const ClientSafeInternalErrorMessage = "internal server error"

const (
	// 499 is the de-facto HTTP status for a client that closes a request before
	// the server can produce a response. Go's standard status tables do not
	// define it, but Bifrost already uses it for context cancellation elsewhere.
	clientClosedRequestStatusCode = 499
	gatewayTimeoutStatusCode      = 504
)

// SanitizeBifrostErrorForClient returns a copy safe to serialize to API clients.
// Internal errors can contain stack traces or database details; keep those in logs only.
func SanitizeBifrostErrorForClient(err *schemas.BifrostError) *schemas.BifrostError {
	if err == nil {
		return nil
	}

	sanitized := *err
	if err.Error != nil {
		errorField := *err.Error
		setMissingContextTerminationStatus(&sanitized, &errorField)
		if shouldHideErrorDetails(err, err.Error) {
			errorField.Message = ClientSafeInternalErrorMessage
			errorField.Error = nil
			errorField.Param = nil
		}
		sanitized.Error = &errorField
	}

	return &sanitized
}

// setMissingContextTerminationStatus gives provider-originated context errors
// the same client-facing status contract as cancellation detected by core:
// cancellation is 499 and deadline expiry is 504. Explicit provider/plugin
// statuses always win, and this only mutates the sanitized response copy.
func setMissingContextTerminationStatus(err *schemas.BifrostError, field *schemas.ErrorField) {
	if err.StatusCode != nil || field == nil {
		return
	}

	errorType := ""
	if field.Type != nil {
		errorType = *field.Type
	}

	var statusCode int
	switch {
	case errorType == schemas.RequestTimedOut || errors.Is(field.Error, context.DeadlineExceeded):
		statusCode = gatewayTimeoutStatusCode
	case errorType == schemas.RequestCancelled || errors.Is(field.Error, context.Canceled):
		statusCode = clientClosedRequestStatusCode
	default:
		return
	}

	err.StatusCode = &statusCode
}

func shouldHideErrorDetails(_ *schemas.BifrostError, field *schemas.ErrorField) bool {
	message := field.Message
	if field.Error != nil {
		message += " " + field.Error.Error()
	}

	return containsStackTrace(message) || containsSQLDetails(message)
}

func containsStackTrace(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "stack trace") ||
		strings.Contains(lower, "traceback (most recent call last)") ||
		strings.Contains(lower, "runtime/debug.stack") ||
		strings.Contains(lower, "goroutine ") ||
		strings.Contains(lower, "panic:") ||
		strings.Contains(lower, ".go:")
}

func containsSQLDetails(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "sqlstate") ||
		strings.Contains(lower, "sql:") ||
		strings.Contains(lower, "pq:") ||
		strings.Contains(lower, "pgx:") ||
		strings.Contains(lower, "duplicate key value violates") ||
		strings.Contains(lower, "violates foreign key constraint") ||
		strings.Contains(lower, "violates unique constraint") ||
		strings.Contains(lower, "syntax error at or near") ||
		strings.Contains(lower, "relation does not exist") ||
		strings.Contains(lower, "database/sql")
}
