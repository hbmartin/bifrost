package bedrock

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storedBatchInputObject struct {
	body     []byte
	metadata map[string]string
	etag     string
}

type fakeBatchInputS3Client struct {
	objects   map[string]storedBatchInputObject
	putErrors []error
	putCalls  int
}

func (f *fakeBatchInputS3Client) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putCalls++
	if len(f.putErrors) > 0 {
		err := f.putErrors[0]
		f.putErrors = f.putErrors[1:]
		return nil, err
	}
	key := aws.ToString(input.Key)
	if input.IfNoneMatch != nil {
		if _, exists := f.objects[key]; exists {
			return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "object already exists"}
		}
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	etag := `"etag-` + key + `"`
	f.objects[key] = storedBatchInputObject{body: body, metadata: input.Metadata, etag: etag}
	return &s3.PutObjectOutput{ETag: aws.String(etag)}, nil
}

func (f *fakeBatchInputS3Client) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	object, exists := f.objects[aws.ToString(input.Key)]
	if !exists {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "object not found"}
	}
	return &s3.HeadObjectOutput{Metadata: object.metadata, ETag: aws.String(object.etag)}, nil
}

func (f *fakeBatchInputS3Client) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	key := aws.ToString(input.Key)
	object, exists := f.objects[key]
	if !exists {
		return &s3.DeleteObjectOutput{}, nil
	}
	if input.IfMatch != nil && aws.ToString(input.IfMatch) != object.etag {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "etag does not match"}
	}
	delete(f.objects, key)
	return &s3.DeleteObjectOutput{}, nil
}

func TestPutBatchInputS3ObjectRejectsTokenReuseWithDifferentContent(t *testing.T) {
	t.Parallel()

	client := &fakeBatchInputS3Client{objects: make(map[string]storedBatchInputObject)}
	ctx := context.Background()
	const key = "bifrost-batch-input/token-digest.jsonl"
	firstContent := []byte("first batch input")

	firstUpload, bifrostErr := putBatchInputS3Object(ctx, client, "bucket", key, firstContent, true)
	require.Nil(t, bifrostErr)
	require.True(t, firstUpload.created)

	replayedUpload, bifrostErr := putBatchInputS3Object(ctx, client, "bucket", key, firstContent, true)
	require.Nil(t, bifrostErr, "same-content replay should reuse the object")
	require.False(t, replayedUpload.created)

	_, bifrostErr = putBatchInputS3Object(ctx, client, "bucket", key, []byte("different batch input"), true)
	require.NotNil(t, bifrostErr, "different-content replay must fail before AWS reuses the original job")
	assert.False(t, bifrostErr.IsBifrostError)
	require.NotNil(t, bifrostErr.StatusCode)
	assert.Equal(t, 409, *bifrostErr.StatusCode)
	require.NotNil(t, bifrostErr.AllowFallbacks)
	assert.False(t, *bifrostErr.AllowFallbacks)
	assert.Equal(t, firstContent, client.objects[key].body, "idempotent replay must not overwrite the original input")
}

func TestPutBatchInputS3ObjectRetriesConditionalRequestConflict(t *testing.T) {
	t.Parallel()

	client := &fakeBatchInputS3Client{
		objects: make(map[string]storedBatchInputObject),
		putErrors: []error{
			&smithy.GenericAPIError{Code: "ConditionalRequestConflict", Message: "conflicting conditional write"},
		},
	}

	upload, bifrostErr := putBatchInputS3Object(context.Background(), client, "bucket", "key", []byte("content"), true)
	require.Nil(t, bifrostErr)
	require.NotNil(t, upload)
	assert.True(t, upload.created)
	assert.Equal(t, 2, client.putCalls)
}

func TestBatchInputS3UploadDeletesOnlyCreatedObject(t *testing.T) {
	t.Parallel()

	client := &fakeBatchInputS3Client{objects: make(map[string]storedBatchInputObject)}
	upload, bifrostErr := putBatchInputS3Object(context.Background(), client, "bucket", "key", []byte("content"), true)
	require.Nil(t, bifrostErr)
	require.Contains(t, client.objects, "key")

	require.Nil(t, upload.deleteIfCreated(context.Background()))
	assert.NotContains(t, client.objects, "key")
	assert.False(t, upload.created)
}

func TestBatchInputS3UploadDoesNotDeleteReplacement(t *testing.T) {
	t.Parallel()

	client := &fakeBatchInputS3Client{objects: make(map[string]storedBatchInputObject)}
	upload, bifrostErr := putBatchInputS3Object(context.Background(), client, "bucket", "key", []byte("original"), true)
	require.Nil(t, bifrostErr)
	client.objects["key"] = storedBatchInputObject{
		body:     []byte("replacement"),
		metadata: map[string]string{},
		etag:     `"replacement-etag"`,
	}

	cleanupErr := upload.deleteIfCreated(context.Background())
	require.NotNil(t, cleanupErr)
	assert.Equal(t, []byte("replacement"), client.objects["key"].body)
}

func TestReplayedBatchInputS3UploadDoesNotDeleteSharedObject(t *testing.T) {
	t.Parallel()

	client := &fakeBatchInputS3Client{objects: make(map[string]storedBatchInputObject)}
	_, bifrostErr := putBatchInputS3Object(context.Background(), client, "bucket", "key", []byte("content"), true)
	require.Nil(t, bifrostErr)
	replay, bifrostErr := putBatchInputS3Object(context.Background(), client, "bucket", "key", []byte("content"), true)
	require.Nil(t, bifrostErr)
	require.False(t, replay.created)

	require.Nil(t, replay.deleteIfCreated(context.Background()))
	assert.Contains(t, client.objects, "key")
}

// TestConvertBedrockRequestsToJSONL_NoModelIDInModelInput guards against
// regressing the Bedrock batch bug where modelId was injected into each
// record's modelInput. Bedrock requires each JSONL line to be strictly
// {recordId, modelInput} with modelId only at the job level, otherwise it
// rejects records with "modelId: Extra inputs are not permitted".
func TestConvertBedrockRequestsToJSONL_NoModelIDInModelInput(t *testing.T) {
	modelID := "us.anthropic.claude-opus-4-6-v1"
	requests := []schemas.BatchRequestItem{
		{
			CustomID: "item-00043",
			Body: map[string]interface{}{
				"anthropic_version": "bedrock-2023-05-31",
				"max_tokens":        16,
				"messages": []map[string]interface{}{
					{"role": "user", "content": "Reply with the number 43."},
				},
				"model": modelID, // should be stripped, not leaked into modelInput
			},
		},
		{
			CustomID: "item-00044",
			Params: map[string]interface{}{
				"max_tokens": 8,
				"model":      modelID,
			},
		},
	}

	data, err := ConvertBedrockRequestsToJSONL(requests, &modelID)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2)

	for i, line := range lines {
		var record struct {
			RecordID   string                 `json:"recordId"`
			ModelInput map[string]interface{} `json:"modelInput"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &record), "line %d should be valid JSON", i)

		assert.NotEmpty(t, record.RecordID, "recordId should be set")

		// The core regression assertions: neither modelId nor model may appear
		// inside modelInput.
		_, hasModelID := record.ModelInput["modelId"]
		assert.False(t, hasModelID, "modelInput must not contain modelId (line %d)", i)
		_, hasModel := record.ModelInput["model"]
		assert.False(t, hasModel, "modelInput must not contain model (line %d)", i)
	}

	// First record's body should be carried through verbatim (minus model).
	var first struct {
		ModelInput map[string]interface{} `json:"modelInput"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, "bedrock-2023-05-31", first.ModelInput["anthropic_version"])
	assert.Contains(t, first.ModelInput, "messages")
}

// TestConvertBedrockRequestsToJSONL_RequiresModelID confirms the job-level
// model is still mandatory.
func TestConvertBedrockRequestsToJSONL_RequiresModelID(t *testing.T) {
	requests := []schemas.BatchRequestItem{{CustomID: "item-1", Body: map[string]interface{}{"max_tokens": 16}}}

	_, err := ConvertBedrockRequestsToJSONL(requests, nil)
	assert.Error(t, err)

	empty := ""
	_, err = ConvertBedrockRequestsToJSONL(requests, &empty)
	assert.Error(t, err)
}
