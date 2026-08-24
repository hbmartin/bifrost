package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestBatchClientRequestToken(t *testing.T) {
	t.Parallel()

	for _, extraParams := range []map[string]any{
		{"clientRequestToken": "native-token"},
		{"client_request_token": "bifrost-token"},
	} {
		request := &schemas.BifrostBatchCreateRequest{ExtraParams: extraParams}
		if token := BatchClientRequestToken(request); token == "" {
			t.Fatalf("BatchClientRequestToken(%v) returned an empty token", extraParams)
		}
	}

	requestBody, err := json.Marshal(BedrockBatchJobRequest{ClientRequestToken: "wire-token"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	assert.JSONEq(t, `{"jobName":"","clientRequestToken":"wire-token","modelId":null,"roleArn":"","inputDataConfig":{"s3InputDataConfig":{"s3Uri":""}},"outputDataConfig":{"s3OutputDataConfig":{"s3Uri":""}}}`, string(requestBody))
}

// TestToBedrockBatchJobRetrieveResponse_SurfacesFailureMessage verifies the
// AWS job failure reason carried in the normalized Errors field is mapped back
// to Bedrock's native message field, so callers can see why a job failed
// without dropping to the AWS CLI.
func TestToBedrockBatchJobRetrieveResponse_SurfacesFailureMessage(t *testing.T) {
	const failure = "Batch job arn:... contains less records (1) than the required minimum of: 100"

	resp := &schemas.BifrostBatchRetrieveResponse{
		ID:     "arn:aws:bedrock:us-east-1:123:model-invocation-job/abc",
		Status: schemas.BatchStatusFailed,
		Errors: &schemas.BatchErrors{
			Object: "list",
			Data:   []schemas.BatchError{{Message: failure}},
		},
	}

	out := ToBedrockBatchJobRetrieveResponse(resp)
	assert.Equal(t, "Failed", out.Status)
	assert.Equal(t, failure, out.Message)
}

// TestToBedrockBatchJobRetrieveResponse_NoErrors confirms the message stays
// empty when there is no failure reason.
func TestToBedrockBatchJobRetrieveResponse_NoErrors(t *testing.T) {
	resp := &schemas.BifrostBatchRetrieveResponse{
		ID:     "arn:aws:bedrock:us-east-1:123:model-invocation-job/abc",
		Status: schemas.BatchStatusCompleted,
	}

	out := ToBedrockBatchJobRetrieveResponse(resp)
	assert.Empty(t, out.Message)
}
