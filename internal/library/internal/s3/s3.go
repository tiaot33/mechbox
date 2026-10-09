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

type Object struct {
	ETag         string
	LastModified time.Time
	PushedFrom   string
}

const pushedFromHeader = "x-amz-meta-pushed-from"

func Head(ctx context.Context) (Object, error) {
	_, object, err := request(ctx, http.MethodHead, nil, nil)
	return object, err
}

func Get(ctx context.Context) ([]byte, Object, error) {
	return request(ctx, http.MethodGet, nil, nil)
}

func Put(ctx context.Context, data []byte, expected, pushedFrom string) (Object, error) {
	headers := map[string]string{"If-Match": expected}
	if expected == "" {
		headers = map[string]string{"If-None-Match": "*"}
	}
	if pushedFrom != "" {
		headers[pushedFromHeader] = url.QueryEscape(pushedFrom)
	}
	_, object, err := request(ctx, http.MethodPut, data, headers)
	if err != nil {
		return Object{}, err
	}
	object.PushedFrom = pushedFrom
	return object, nil
}

func request(ctx context.Context, method string, body []byte, headers map[string]string) ([]byte, Object, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, Object{}, err
	}
	resource := "/" + uriEncode(cfg.key, true)
	if cfg.pathStyle {
		resource = "/" + uriEncode(cfg.bucket, false) + resource
	}
	requestURL := strings.TrimRight(cfg.endpoint, "/") + resource
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, Object{}, fmt.Errorf("create S3 request: %w", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	sign(req, body, cfg, time.Now().UTC())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, Object{}, fmt.Errorf("reach S3: %v: %w", err, ErrUnreachable)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Object{}, fmt.Errorf("read S3 response: %v: %w", err, ErrUnreachable)
	}
	var s3Error struct {
		Code string `xml:"Code"`
	}
	_ = xml.Unmarshal(response, &s3Error)
	permissionCode := map[string]bool{"AccessDenied": true, "InvalidAccessKeyId": true, "SignatureDoesNotMatch": true, "ExpiredToken": true, "InvalidToken": true}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || permissionCode[s3Error.Code] {
		return nil, Object{}, ErrPermission
	}
	if resp.StatusCode == http.StatusNotFound || s3Error.Code == "NoSuchKey" {
		return nil, Object{}, ErrNotFound
	}
	if resp.StatusCode == http.StatusPreconditionFailed || s3Error.Code == "PreconditionFailed" {
		return nil, Object{}, ErrPreconditionFailed
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, Object{}, fmt.Errorf("S3 returned %s: %w", resp.Status, ErrUnreachable)
	}
	object := Object{ETag: resp.Header.Get("ETag")}
	if object.ETag == "" && method != http.MethodPut {
		return nil, Object{}, fmt.Errorf("S3 response missing ETag: %w", ErrUnreachable)
	}
	modified := resp.Header.Get("Last-Modified")
	if modified == "" && method == http.MethodPut {
		modified = resp.Header.Get("Date")
	}
	if at, err := http.ParseTime(modified); err == nil {
		object.LastModified = at.UTC()
	}
	if pushedFrom, err := url.QueryUnescape(resp.Header.Get(pushedFromHeader)); err == nil {
		object.PushedFrom = pushedFrom
	}
	return response, object, nil
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
