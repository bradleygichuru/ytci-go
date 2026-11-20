package r2

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Store interface {
	PresignedPutURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	PutObject(ctx context.Context, key string, data io.Reader, contentType string) error
	GetObject(ctx context.Context, key string) ([]byte, error)
	DeleteObject(ctx context.Context, key string) error
	// HeadObject reports the stored object's size and content type. Callers
	// use it to validate a direct upload before accepting it.
	HeadObject(ctx context.Context, key string) (ObjectInfo, error)
}

// ObjectInfo is the subset of object metadata the upload-validation flow
// needs.
type ObjectInfo struct {
	Size        int64
	ContentType string
}

// ContentTypePresigner is implemented by stores that can bind a presigned PUT
// to a declared content type so R2 rejects a mismatched upload. Callers fall
// back to Store.PresignedPutURL when a store does not implement it.
type ContentTypePresigner interface {
	PresignedPutURLWithContentType(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
}

var (
	_ Store                = (*Client)(nil)
	_ ContentTypePresigner = (*Client)(nil)
)

type Client struct {
	s3Client *s3.Client
	bucket   string
}

func New(accountID, accessKeyID, secretAccessKey, bucket string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("auto"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKeyID, secretAccessKey, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID))
	})

	return &Client{s3Client: client, bucket: bucket}, nil
}

func (c *Client) PresignedPutURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presign := s3.NewPresignClient(c.s3Client)
	req, err := presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

// PresignedPutURLWithContentType signs a PUT that only succeeds when the
// browser sends the declared Content-Type.
func (c *Client) PresignedPutURLWithContentType(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	presign := s3.NewPresignClient(c.s3Client)
	req, err := presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

func (c *Client) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presign := s3.NewPresignClient(c.s3Client)
	req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return req.URL, nil
}

func (c *Client) PutObject(ctx context.Context, key string, data io.Reader, contentType string) error {
	_, err := c.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        data,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

// StreamObject writes the whole object to w. Unlike GetObject it has no size
// cap, so large releases can be served without buffering them in memory.
func (c *Client) StreamObject(ctx context.Context, key string, w io.Writer) (int64, error) {
	out, err := c.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()
	n, err := io.Copy(w, out.Body)
	if err != nil {
		return n, fmt.Errorf("stream object: %w", err)
	}
	return n, nil
}

func (c *Client) GetObject(ctx context.Context, key string) ([]byte, error) {
	out, err := c.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(io.LimitReader(out.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

func (c *Client) DeleteObject(ctx context.Context, key string) error {
	_, err := c.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (c *Client) HeadObject(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := c.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	var contentType string
	if out.ContentType != nil {
		contentType = *out.ContentType
	}
	return ObjectInfo{Size: size, ContentType: contentType}, nil
}
