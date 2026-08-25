package bedrock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

const batchInputContentDigestMetadataKey = "bifrost-content-sha256"

const maxBatchInputConditionalWriteAttempts = 3

type batchInputS3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type batchInputS3Upload struct {
	client  batchInputS3Client
	bucket  string
	key     string
	etag    string
	created bool
}

// uploadToS3 uploads content to an S3 bucket using the provided credentials.
func uploadToS3(
	ctx context.Context,
	accessKey, secretKey string,
	sessionToken *string,
	region string,
	bucket, key string,
	content []byte,
	replayProtected bool,
) (*batchInputS3Upload, *schemas.BifrostError) {
	// Create AWS config with credentials
	var cfg aws.Config
	var err error

	if accessKey != "" && secretKey != "" {
		// Use provided credentials
		var creds aws.CredentialsProvider
		if sessionToken != nil && *sessionToken != "" {
			creds = credentials.NewStaticCredentialsProvider(accessKey, secretKey, *sessionToken)
		} else {
			creds = credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")
		}

		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(creds),
		)
	} else {
		// Use default credentials chain (IAM role, env vars, etc.)
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
	}

	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to load aws config for s3", err)
	}

	// Create S3 client
	client := s3.NewFromConfig(cfg)

	return putBatchInputS3Object(ctx, client, bucket, key, content, replayProtected)
}

func putBatchInputS3Object(
	ctx context.Context,
	client batchInputS3Client,
	bucket, key string,
	content []byte,
	replayProtected bool,
) (*batchInputS3Upload, *schemas.BifrostError) {
	contentDigest := fmt.Sprintf("%x", sha256.Sum256(content))
	for attempt := 0; attempt < maxBatchInputConditionalWriteAttempts; attempt++ {
		// PutObject consumes Body, so build a fresh input for every 409 retry.
		input := &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(content),
			ContentType: aws.String("application/jsonl"),
			Metadata: map[string]string{
				batchInputContentDigestMetadataKey: contentDigest,
			},
		}
		if replayProtected {
			// A stable token-derived key is safe only if retries cannot overwrite the
			// object that AWS associated with the first accepted request.
			input.IfNoneMatch = aws.String("*")
		}

		output, err := client.PutObject(ctx, input)
		if err == nil {
			if output == nil {
				return nil, providerUtils.NewBifrostOperationError(fmt.Sprintf("s3 returned no upload result for batch input: %s/%s", bucket, key), nil)
			}
			return &batchInputS3Upload{
				client:  client,
				bucket:  bucket,
				key:     key,
				etag:    aws.ToString(output.ETag),
				created: true,
			}, nil
		}
		if replayProtected && isS3ConditionalRequestConflict(err) && attempt+1 < maxBatchInputConditionalWriteAttempts {
			continue
		}
		if replayProtected && isS3PreconditionFailed(err) {
			existing, headErr := client.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
			})
			if headErr != nil {
				return nil, providerUtils.NewBifrostOperationError(fmt.Sprintf("failed to verify existing s3 batch input: %s/%s", bucket, key), headErr)
			}
			if existing.Metadata[batchInputContentDigestMetadataKey] == contentDigest {
				return &batchInputS3Upload{
					client: client,
					bucket: bucket,
					key:    key,
					etag:   aws.ToString(existing.ETag),
				}, nil
			}
			return nil, newBatchClientRequestTokenReuseError()
		}

		return nil, providerUtils.NewBifrostOperationError(fmt.Sprintf("failed to upload to s3: %s/%s", bucket, key), err)
	}

	return nil, providerUtils.NewBifrostOperationError(fmt.Sprintf("failed to upload to s3 after %d conditional-write attempts: %s/%s", maxBatchInputConditionalWriteAttempts, bucket, key), nil)
}

func isS3PreconditionFailed(err error) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && apiError.ErrorCode() == "PreconditionFailed"
}

func isS3ConditionalRequestConflict(err error) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && apiError.ErrorCode() == "ConditionalRequestConflict"
}

func newBatchClientRequestTokenReuseError() *schemas.BifrostError {
	statusCode := 409
	errorType := "invalid_request_error"
	errorCode := "idempotency_key_reused"
	allowFallbacks := false
	return &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     &statusCode,
		AllowFallbacks: &allowFallbacks,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Code:    &errorCode,
			Message: "bedrock batch client_request_token was reused with different inline request content",
		},
	}
}

// deleteIfCreated removes only the exact object version written by this request.
// The ETag precondition prevents cleanup from deleting a later replacement.
func (upload *batchInputS3Upload) deleteIfCreated(ctx context.Context) *schemas.BifrostError {
	if upload == nil || !upload.created {
		return nil
	}
	if upload.etag == "" {
		return providerUtils.NewBifrostOperationError("cannot safely delete rejected bedrock batch input because s3 did not return an etag", nil)
	}
	_, err := upload.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(upload.bucket),
		Key:     aws.String(upload.key),
		IfMatch: aws.String(upload.etag),
	})
	if err != nil {
		return providerUtils.NewBifrostOperationError(fmt.Sprintf("failed to delete rejected s3 batch input: %s/%s", upload.bucket, upload.key), err)
	}
	upload.created = false
	return nil
}

// generateBatchInputS3Key generates a stable key for replayable batch creates
// and a unique key for calls that do not carry an idempotency token. Tokenized
// calls must address the same object on every replay; conditional upload plus
// digest metadata rejects accidental token reuse with different JSONL.
func generateBatchInputS3Key(jobName, clientRequestToken string, _ []byte) string {
	if clientRequestToken != "" {
		digest := sha256.Sum256([]byte(clientRequestToken))
		return fmt.Sprintf("bifrost-batch-input/token-%x.jsonl", digest)
	}
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("bifrost-batch-input/%s-%d.jsonl", jobName, timestamp)
}

// deriveInputS3URIFromOutput derives an input S3 URI from the output S3 URI.
// It uses the same bucket but with a different path for input files.
func deriveInputS3URIFromOutput(outputS3URI, inputKey string) string {
	bucket, _ := parseS3URI(outputS3URI)
	return fmt.Sprintf("s3://%s/%s", bucket, inputKey)
}

// ConvertBedrockRequestsToJSONL converts batch request items to JSONL format for Bedrock.
// Bedrock uses a specific format for batch inference requests.
func ConvertBedrockRequestsToJSONL(requests []schemas.BatchRequestItem, modelID *string) ([]byte, error) {
	// Model ID is required for Bedrock batch JSONL conversion
	if modelID == nil || *modelID == "" {
		return nil, fmt.Errorf("modelID is required for Bedrock batch JSONL conversion")
	}
	// Initialize the buffer
	var buf bytes.Buffer

	// Iterate over the requests
	for _, req := range requests {
		// Build the Bedrock batch request format. modelId belongs at the job
		// level, not inside modelInput, so the record only carries recordId and
		// the raw model invocation body.
		modelInput := map[string]interface{}{}
		bedrockReq := map[string]interface{}{
			"recordId":   req.CustomID,
			"modelInput": modelInput,
		}

		// If the request has a body, use it as the model input parameters
		if req.Body != nil {
			for k, v := range req.Body {
				if k != "model" { // model is set at the job level, not per-record
					modelInput[k] = v
				}
			}
		} else if req.Params != nil {
			for k, v := range req.Params {
				if k != "model" {
					modelInput[k] = v
				}
			}
		}

		// Marshal the request as a JSON line
		line, err := providerUtils.MarshalSorted(bedrockReq)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal batch request item %s: %w", req.CustomID, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	return buf.Bytes(), nil
}
