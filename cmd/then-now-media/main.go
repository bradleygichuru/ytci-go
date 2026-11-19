// Command then-now-media inventories and uploads Then & Now media to R2
// while keeping the bucket inside its free-tier storage allowance.
//
// Usage:
//
//	then-now-media ls
//	then-now-media upload <dir> <key-prefix> [--dry-run]
//	then-now-media sign <key> [ttl]
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Budget is the storage allowance we hold ourselves to. Cloudflare R2 includes
// 10 GiB-month of standard storage; we stop short of it so a stray upload
// cannot start billing or exhaust the allowance.
const (
	budgetBytes int64 = 8 << 30 // 8 GiB
	freeTier    int64 = 10 << 30
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	accountID, accessKey, secretKey, bucket := env()
	client := newClient(accountID, accessKey, secretKey)
	ctx := context.Background()

	switch os.Args[1] {
	case "ls":
		if err := list(ctx, client, bucket); err != nil {
			fail(err)
		}
	case "sign":
		if len(os.Args) < 3 {
			usage()
		}
		ttl := 15 * time.Minute
		if len(os.Args) > 3 {
			d, err := time.ParseDuration(os.Args[3])
			if err != nil {
				fail(fmt.Errorf("ttl: %w", err))
			}
			ttl = d
		}
		u, err := presignGet(ctx, client, bucket, os.Args[2], ttl)
		if err != nil {
			fail(err)
		}
		fmt.Println(u)
	case "upload":
		if len(os.Args) < 4 {
			usage()
		}
		dryRun := false
		for _, a := range os.Args[4:] {
			if a == "--dry-run" {
				dryRun = true
			}
		}
		if err := upload(ctx, client, bucket, os.Args[2], os.Args[3], dryRun); err != nil {
			fail(err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  then-now-media ls
  then-now-media upload <dir> <key-prefix> [--dry-run]
  then-now-media sign <key> [ttl]
`)
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func env() (accountID, accessKey, secretKey, bucket string) {
	accountID = os.Getenv("R2_ACCOUNT_ID")
	accessKey = os.Getenv("R2_ACCESS_KEY_ID")
	secretKey = os.Getenv("R2_SECRET_ACCESS_KEY")
	bucket = os.Getenv("R2_BUCKET")
	if accountID == "" || accessKey == "" || secretKey == "" || bucket == "" {
		fail(fmt.Errorf("R2_ACCOUNT_ID, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY and R2_BUCKET are required"))
	}
	return
}

func newClient(accountID, accessKey, secretKey string) *s3.Client {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("auto"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		fail(fmt.Errorf("load aws config: %w", err))
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID))
	})
}

type object struct {
	Key  string
	Size int64
}

func objects(ctx context.Context, client *s3.Client, bucket string) ([]object, error) {
	var out []object
	var token *string
	for {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list: %w", err)
		}
		for _, o := range page.Contents {
			out = append(out, object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size)})
		}
		if page.IsTruncated == nil || !*page.IsTruncated {
			break
		}
		token = page.NextContinuationToken
	}
	return out, nil
}

func list(ctx context.Context, client *s3.Client, bucket string) error {
	objs, err := objects(ctx, client, bucket)
	if err != nil {
		return err
	}
	var total int64
	for _, o := range objs {
		total += o.Size
		fmt.Printf("%10s  %s\n", human(o.Size), o.Key)
	}
	fmt.Printf("\n%d objects, %s used\n", len(objs), human(total))
	fmt.Printf("free tier %s | budget %s | headroom to budget %s\n",
		human(freeTier), human(budgetBytes), human(budgetBytes-total))
	if total > budgetBytes {
		fmt.Println("WARNING: bucket is over budget")
	}
	return nil
}

func upload(ctx context.Context, client *s3.Client, bucket, dir, prefix string, dryRun bool) error {
	objs, err := objects(ctx, client, bucket)
	if err != nil {
		return err
	}
	var used int64
	existing := map[string]int64{}
	for _, o := range objs {
		used += o.Size
		existing[o.Key] = o.Size
	}

	type file struct {
		path string
		key  string
		size int64
	}
	var files []file
	var incoming int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		key := strings.TrimSuffix(prefix, "/") + "/" + e.Name()
		files = append(files, file{
			path: filepath.Join(dir, e.Name()),
			key:  key,
			size: info.Size(),
		})
		// Replacements free their old bytes, so only count growth.
		if prev, ok := existing[key]; ok {
			incoming += info.Size() - prev
		} else {
			incoming += info.Size()
		}
	}

	projected := used + incoming
	fmt.Printf("bucket: %s used | this upload: %s | projected: %s (budget %s)\n",
		human(used), human(incoming), human(projected), human(budgetBytes))
	if projected > budgetBytes {
		return fmt.Errorf("refusing upload: projected %s exceeds budget %s; free space or shrink renders first",
			human(projected), human(budgetBytes))
	}

	var written int64
	for _, f := range files {
		ct := contentType(f.path)
		if dryRun {
			fmt.Printf("would upload %10s  %s  (%s)\n", human(f.size), f.key, ct)
			continue
		}
		if err := put(ctx, client, bucket, f.path, f.key, ct); err != nil {
			return err
		}
		written += f.size
		fmt.Printf("uploaded %10s  %s  (%s)\n", human(f.size), f.key, ct)
	}
	fmt.Printf("done: %s in %d objects | bucket now %s of %s budget\n",
		human(written), len(files), human(projected), human(budgetBytes))
	return nil
}

func put(ctx context.Context, client *s3.Client, bucket, path, key, ct string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         f,
		ContentType:  aws.String(ct),
		CacheControl: aws.String("private, max-age=3600"),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func presignGet(ctx context.Context, client *s3.Client, bucket, key string, ttl time.Duration) (string, error) {
	presign := s3.NewPresignClient(client)
	req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign: %w", err)
	}
	return req.URL, nil
}

func contentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".vtt":
		return "text/vtt"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
