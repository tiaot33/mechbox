package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	ErrNotConfigured      = errors.New("S3 sync is not configured")
	ErrUnreachable        = errors.New("S3 could not be reached")
	ErrPermission         = errors.New("S3 permission denied")
	ErrNotFound           = errors.New("S3 object not found")
	ErrPreconditionFailed = errors.New("remote object changed since sync plan")
)

type config struct {
	endpoint, bucket, key, region, accessKey, secretKey, sessionToken string
	pathStyle                                                         bool
}

func Head(ctx context.Context) (string, error) {
	_, etag, err := request(ctx, http.MethodHead, nil, nil)
	return etag, err
}

func Get(ctx context.Context) ([]byte, string, error) {
	return request(ctx, http.MethodGet, nil, nil)
}

func Put(ctx context.Context, data []byte, expected string) (string, error) {
	conditions := map[string]string{"If-Match": expected}
	if expected == "" {
		conditions = map[string]string{"If-None-Match": "*"}
	}
	_, etag, err := request(ctx, http.MethodPut, data, conditions)
	return etag, err
}

func request(ctx context.Context, method string, body []byte, conditions map[string]string) ([]byte, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", err
	}
	resource := "/" + uriEncode(cfg.key, true)
	if cfg.pathStyle {
		resource = "/" + uriEncode(cfg.bucket, false) + resource
	}
	requestURL := strings.TrimRight(cfg.endpoint, "/") + resource
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create S3 request: %w", err)
	}
	sign(req, body, cfg, time.Now().UTC())
	for name, value := range conditions {
		req.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("reach S3: %v: %w", err, ErrUnreachable)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read S3 response: %v: %w", err, ErrUnreachable)
	}
	var s3Error struct {
		Code string `xml:"Code"`
	}
	_ = xml.Unmarshal(response, &s3Error)
	permissionCode := map[string]bool{"AccessDenied": true, "InvalidAccessKeyId": true, "SignatureDoesNotMatch": true, "ExpiredToken": true, "InvalidToken": true}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || permissionCode[s3Error.Code] {
		return nil, "", ErrPermission
	}
	if resp.StatusCode == http.StatusNotFound || s3Error.Code == "NoSuchKey" {
		return nil, "", ErrNotFound
	}
	if resp.StatusCode == http.StatusPreconditionFailed || s3Error.Code == "PreconditionFailed" {
		return nil, "", ErrPreconditionFailed
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("S3 returned %s: %w", resp.Status, ErrUnreachable)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" && method != http.MethodPut {
		return nil, "", fmt.Errorf("S3 response missing ETag: %w", ErrUnreachable)
	}
	return response, etag, nil
}

func loadConfig() (config, error) {
	cfg := config{
		endpoint:     os.Getenv("SKILLS_S3_ENDPOINT"),
		bucket:       os.Getenv("SKILLS_S3_BUCKET"),
		key:          os.Getenv("SKILLS_S3_KEY"),
		region:       os.Getenv("AWS_REGION"),
		accessKey:    os.Getenv("AWS_ACCESS_KEY_ID"),
		secretKey:    os.Getenv("AWS_SECRET_ACCESS_KEY"),
		sessionToken: os.Getenv("AWS_SESSION_TOKEN"),
	}
	if cfg.key == "" {
		cfg.key = "library.db"
	}
	if cfg.region == "" {
		cfg.region = "us-east-1"
	}
	if cfg.bucket == "" || cfg.accessKey == "" || cfg.secretKey == "" {
		return config{}, fmt.Errorf("S3 bucket and AWS credentials are required: %w", ErrNotConfigured)
	}
	if cfg.endpoint == "" {
		if strings.Contains(cfg.bucket, ".") {
			cfg.endpoint = "https://s3." + cfg.region + ".amazonaws.com"
			cfg.pathStyle = true
		} else {
			cfg.endpoint = "https://" + cfg.bucket + ".s3." + cfg.region + ".amazonaws.com"
		}
	} else {
		cfg.pathStyle = true
	}
	endpoint, err := url.ParseRequestURI(cfg.endpoint)
	if err != nil || endpoint.Host == "" || endpoint.Scheme != "http" && endpoint.Scheme != "https" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.User != nil {
		if err == nil {
			err = errors.New("endpoint must be an http(s) origin without credentials, query, or fragment")
		}
		return config{}, fmt.Errorf("invalid S3 endpoint: %v: %w", err, ErrNotConfigured)
	}
	return cfg, nil
}
