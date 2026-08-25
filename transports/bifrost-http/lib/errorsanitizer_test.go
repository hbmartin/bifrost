package lib

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestSanitizeBifrostErrorForClientHidesInternalDetails(t *testing.T) {
	statusCode := fasthttp.StatusInternalServerError
	err := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: "failed to create customer: pq: duplicate key value violates unique constraint users_email_key",
			Error:   errors.New("goroutine 1 [running]:\nmain.handler\n\t/app/server.go:42"),
			Param:   "users_email_key",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized == err {
		t.Fatal("expected sanitizer to return a copy")
	}
	if sanitized.Error.Message != ClientSafeInternalErrorMessage {
		t.Fatalf("expected generic message, got %q", sanitized.Error.Message)
	}
	if sanitized.Error.Error != nil {
		t.Fatalf("expected sensitive nested error to be removed, got %v", sanitized.Error.Error)
	}
	if sanitized.Error.Param != nil {
		t.Fatalf("expected param to be removed, got %v", sanitized.Error.Param)
	}
	if err.Error.Message == ClientSafeInternalErrorMessage || err.Error.Error == nil || err.Error.Param == nil {
		t.Fatal("expected original error to remain unchanged")
	}
}

func TestSanitizeBifrostErrorForClientPreservesClientValidationMessage(t *testing.T) {
	statusCode := fasthttp.StatusBadRequest
	err := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "model is required",
			Error:   errors.New("missing model"),
			Param:   "model",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized.Error.Message != "model is required" {
		t.Fatalf("expected validation message to be preserved, got %q", sanitized.Error.Message)
	}
	if sanitized.Error.Param != "model" {
		t.Fatalf("expected param to be preserved, got %v", sanitized.Error.Param)
	}
	if sanitized.Error.Error == nil {
		t.Fatal("expected non-sensitive nested error to be preserved")
	}
}

func TestSanitizeBifrostErrorForClientPreservesNonSensitiveServerMessage(t *testing.T) {
	statusCode := fasthttp.StatusInternalServerError
	err := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "failed to reload config",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized.Error.Message != "failed to reload config" {
		t.Fatalf("expected non-sensitive server message to be preserved, got %q", sanitized.Error.Message)
	}
}

func TestSanitizeBifrostErrorForClientNormalizesMissingContextStatus(t *testing.T) {
	explicitStatus := fasthttp.StatusConflict
	tests := []struct {
		name       string
		err        *schemas.BifrostError
		wantStatus *int
	}{
		{
			name: "bedrock-shaped cancellation type",
			err: &schemas.BifrostError{
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: schemas.ErrRequestCancelled,
				},
			},
			wantStatus: schemas.Ptr(clientClosedRequestStatusCode),
		},
		{
			name: "wrapped cancellation cause",
			err: &schemas.BifrostError{
				Error: &schemas.ErrorField{
					Message: "provider request stopped",
					Error:   fmt.Errorf("provider request: %w", context.Canceled),
				},
			},
			wantStatus: schemas.Ptr(clientClosedRequestStatusCode),
		},
		{
			name: "timeout type",
			err: &schemas.BifrostError{
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestTimedOut),
					Message: "provider request timed out",
				},
			},
			wantStatus: schemas.Ptr(gatewayTimeoutStatusCode),
		},
		{
			name: "wrapped deadline cause",
			err: &schemas.BifrostError{
				Error: &schemas.ErrorField{
					Message: "provider deadline elapsed",
					Error:   fmt.Errorf("provider request: %w", context.DeadlineExceeded),
				},
			},
			wantStatus: schemas.Ptr(gatewayTimeoutStatusCode),
		},
		{
			name: "explicit status wins",
			err: &schemas.BifrostError{
				StatusCode: &explicitStatus,
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: "plugin-selected status",
				},
			},
			wantStatus: &explicitStatus,
		},
		{
			name: "unrelated error remains unspecified",
			err: &schemas.BifrostError{
				Error: &schemas.ErrorField{Message: "invalid model"},
			},
			wantStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalStatus := tt.err.StatusCode
			sanitized := SanitizeBifrostErrorForClient(tt.err)

			if tt.wantStatus == nil {
				if sanitized.StatusCode != nil {
					t.Fatalf("expected no status, got %d", *sanitized.StatusCode)
				}
			} else if sanitized.StatusCode == nil || *sanitized.StatusCode != *tt.wantStatus {
				t.Fatalf("status = %v, want %d", sanitized.StatusCode, *tt.wantStatus)
			}

			if tt.err.StatusCode != originalStatus {
				t.Fatal("expected original error status pointer to remain unchanged")
			}
			if originalStatus == nil && tt.err.StatusCode != nil {
				t.Fatal("expected original error to remain without a status")
			}
		})
	}
}
