package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
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

type Settings struct {
	Endpoint, Bucket, Region, AccessKeyID, SecretAccessKey, SessionToken string
}

type config struct {
	endpoint, bucket, region, accessKey, secretKey, sessionToken string
	pathStyle                                                    bool
}

type Object struct {
	Key, ETag, SHA256 string
}

const sha256Header = "x-amz-meta-sha256"

func Head(ctx context.Context, settings Settings, key string) (Object, error) {
	_, object, err := request(ctx, settings, http.MethodHead, key, nil, nil, nil)
	return object, err
}

func Get(ctx context.Context, settings Settings, key, expected string) ([]byte, Object, error) {
	var headers map[string]string
	if expected != "" {
		headers = map[string]string{"If-Match": expected}
	}
	return request(ctx, settings, http.MethodGet, key, nil, nil, headers)
}

func Put(ctx context.Context, settings Settings, key string, data []byte, sha256 string) (Object, error) {
	headers := map[string]string{"If-None-Match": "*", sha256Header: sha256}
	_, object, err := request(ctx, settings, http.MethodPut, key, nil, data, headers)
	if err != nil {
		return Object{}, err
	}
	object.SHA256 = sha256
	return object, nil
}

func Delete(ctx context.Context, settings Settings, key string) error {
	_, _, err := request(ctx, settings, http.MethodDelete, key, nil, nil, nil)
	return err
}

func List(ctx context.Context, settings Settings, prefix string) ([]Object, error) {
	var objects []Object
	token := ""
	for {
		query := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		response, _, err := request(ctx, settings, http.MethodGet, "", query, nil, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
			Contents              []struct {
				Key  string `xml:"Key"`
				ETag string `xml:"ETag"`
			} `xml:"Contents"`
		}
		if err := xml.Unmarshal(response, &page); err != nil {
			return nil, fmt.Errorf("parse S3 object list: %v: %w", err, ErrUnreachable)
		}
		for _, content := range page.Contents {
			objects = append(objects, Object{Key: content.Key, ETag: content.ETag})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			return objects, nil
		}
		token = page.NextContinuationToken
	}
}

func request(ctx context.Context, settings Settings, method, key string, query url.Values, body []byte, headers map[string]string) ([]byte, Object, error) {
	cfg, err := loadConfig(settings)
	if err != nil {
		return nil, Object{}, err
	}
	resource := "/" + uriEncode(key, true)
	if cfg.pathStyle {
		resource = "/" + uriEncode(cfg.bucket, false)
		if key != "" {
			resource += "/" + uriEncode(key, true)
		}
	}
	requestURL := strings.TrimRight(cfg.endpoint, "/") + resource
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, Object{}, fmt.Errorf("create S3 request: %w", err)
	}
	req.URL.RawQuery = canonicalQuery(query)
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
	if key == "" || method == http.MethodDelete {
		return response, Object{}, nil
	}
	object := Object{Key: key, ETag: resp.Header.Get("ETag"), SHA256: resp.Header.Get(sha256Header)}
	if object.ETag == "" && method != http.MethodPut {
		return nil, Object{}, fmt.Errorf("S3 response missing ETag: %w", ErrUnreachable)
	}
	return response, object, nil
}

func canonicalQuery(query url.Values) string {
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(query)) {
		for _, value := range query[name] {
			parts = append(parts, uriEncode(name, false)+"="+uriEncode(value, false))
		}
	}
	return strings.Join(parts, "&")
}

func loadConfig(settings Settings) (config, error) {
	cfg := config{
		endpoint:     settings.Endpoint,
		bucket:       settings.Bucket,
		region:       settings.Region,
		accessKey:    settings.AccessKeyID,
		secretKey:    settings.SecretAccessKey,
		sessionToken: settings.SessionToken,
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
