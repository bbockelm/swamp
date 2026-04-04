package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
)

// Store wraps S3-compatible object storage operations.
type Store struct {
	client *s3.Client
	bucket string
}

// NewS3Client creates an S3 client for the given endpoint and credentials.
// Works with any S3-compatible service (AWS S3, MinIO, etc).
func NewS3Client(endpoint, accessKey, secretKey string, useSSL, usePathStyle bool) *s3.Client {
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	ep := endpoint
	ep = strings.TrimPrefix(ep, "http://")
	ep = strings.TrimPrefix(ep, "https://")
	resolvedEndpoint := fmt.Sprintf("%s://%s", scheme, ep)

	return s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(resolvedEndpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle: usePathStyle,
	})
}

// NewStoreFromClient creates a Store from an existing S3 client and bucket name.
func NewStoreFromClient(client *s3.Client, bucket string) *Store {
	return &Store{client: client, bucket: bucket}
}

// New creates a new S3 store from config, ensures the bucket exists.
func New(cfg *config.Config) (*Store, error) {
	client := NewS3Client(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL, cfg.S3UsePathStyle)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(cfg.S3Bucket),
	})
	if err != nil {
		log.Warn().Err(err).Msg("Bucket does not exist or is not accessible, attempting to create")
		_, createErr := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(cfg.S3Bucket),
		})
		if createErr != nil {
			log.Warn().Err(createErr).Msg("Failed to create S3 bucket (may already exist)")
		}
	}

	return &Store{client: client, bucket: cfg.S3Bucket}, nil
}

// Upload stores a file in S3.
func (s *Store) Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        reader,
		ContentType: aws.String(contentType),
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("uploading to S3: %w", err)
	}
	return nil
}

// Download retrieves a file from S3.
func (s *Store) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("downloading from S3: %w", err)
	}
	return out.Body, nil
}

// Delete removes a file from S3.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("deleting from S3: %w", err)
	}
	return nil
}

// GenerateKey creates a unique S3 key for an analysis artifact.
func (s *Store) GenerateKey(analysisID, filename string) string {
	return fmt.Sprintf("analyses/%s/%s", analysisID, filename)
}

// ListKeys returns all object keys with the given prefix.
func (s *Store) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing S3 objects: %w", err)
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}
	return keys, nil
}

// Bucket returns the bucket name.
func (s *Store) Bucket() string {
	return s.bucket
}

// Client returns the underlying S3 client.
func (s *Store) Client() *s3.Client {
	return s.client
}
