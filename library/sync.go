package library

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type s3Config struct {
	endpoint, bucket, key, region, accessKey, secretKey, sessionToken string
	pathStyle                                                         bool
}

func (l *sqliteLibrary) SyncPush(ctx context.Context) (err error) {
	defer func() { err = typedError(err) }()
	if l.path == ":memory:" {
		return fmt.Errorf("sync requires a file-backed library")
	}
	snapshot, err := os.CreateTemp(filepath.Dir(l.path), ".library-sync-*.db")
	if err != nil {
		return fmt.Errorf("create sync snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	_ = os.Remove(snapshotPath)
	defer os.Remove(snapshotPath)
	quoted := strings.ReplaceAll(snapshotPath, "'", "''")
	if _, err := l.db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("snapshot library: %w", err)
	}
	// ponytail: full-replace sync holds one database image in memory; stream when libraries become large enough to measure.
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("read sync snapshot: %w", err)
	}
	_, err = s3Request(ctx, http.MethodPut, data)
	return err
}

func (l *sqliteLibrary) SyncPull(ctx context.Context) (err error) {
	defer func() { err = typedError(err) }()
	if l.path == ":memory:" {
		return fmt.Errorf("sync requires a file-backed library")
	}
	data, err := s3Request(ctx, http.MethodGet, nil)
	if err != nil {
		return err
	}
	download, err := os.CreateTemp(filepath.Dir(l.path), ".library-pull-*.db")
	if err != nil {
		return fmt.Errorf("create pull file: %w", err)
	}
	downloadPath := download.Name()
	defer os.Remove(downloadPath)
	if _, err := download.Write(data); err != nil {
		_ = download.Close()
		return fmt.Errorf("write pulled library: %w", err)
	}
	if err := download.Close(); err != nil {
		return fmt.Errorf("close pulled library: %w", err)
	}
	check, err := openExistingLibrary(ctx, downloadPath)
	if err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	var integrity string
	err = check.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity)
	var hasSkillsTable int
	if err == nil {
		err = check.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'skills'`).Scan(&hasSkillsTable)
	}
	_ = check.Close()
	if err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	if integrity != "ok" || hasSkillsTable != 1 {
		return fmt.Errorf("validate pulled library: integrity=%q skills_table=%d", integrity, hasSkillsTable)
	}

	backup, err := os.CreateTemp(filepath.Dir(l.path), ".library-backup-*.db")
	if err != nil {
		return fmt.Errorf("create library backup: %w", err)
	}
	backupPath := backup.Name()
	_ = backup.Close()
	_ = os.Remove(backupPath)
	defer os.Remove(backupPath)
	if err := l.db.Close(); err != nil {
		return fmt.Errorf("close local library: %w", err)
	}
	if err := os.Rename(l.path, backupPath); err != nil {
		l.db, _ = openExistingLibrary(context.Background(), l.path)
		return fmt.Errorf("backup local library: %w", err)
	}
	restore := func(cause error) error {
		_ = os.Remove(l.path)
		_ = os.Rename(backupPath, l.path)
		l.db, _ = openExistingLibrary(context.Background(), l.path)
		return cause
	}
	if err := os.Rename(downloadPath, l.path); err != nil {
		return restore(fmt.Errorf("replace local library: %w", err))
	}
	l.db, err = openExistingLibrary(ctx, l.path)
	if err != nil {
		return restore(fmt.Errorf("open pulled library: %w", err))
	}
	return nil
}

func openExistingLibrary(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func s3Request(ctx context.Context, method string, body []byte) ([]byte, error) {
	cfg, err := loadS3Config()
	if err != nil {
		return nil, err
	}
	resource := "/" + pathEscape(cfg.key)
	if cfg.pathStyle {
		resource = "/" + pathEscape(cfg.bucket) + resource
	}
	requestURL := strings.TrimRight(cfg.endpoint, "/") + resource
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create S3 request: %w", err)
	}
	signS3Request(req, body, cfg, time.Now().UTC())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, &Error{Code: SourceUnreachable, Message: "reach S3: " + err.Error(), Err: err}
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read S3 response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &Error{Code: Permission, Message: "S3 permission denied"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{Code: SourceUnreachable, Message: fmt.Sprintf("S3 returned %s", resp.Status)}
	}
	return response, nil
}

func loadS3Config() (s3Config, error) {
	cfg := s3Config{
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
		return s3Config{}, fmt.Errorf("S3 bucket and AWS credentials are required")
	}
	if cfg.endpoint == "" {
		cfg.endpoint = "https://" + cfg.bucket + ".s3." + cfg.region + ".amazonaws.com"
	} else {
		cfg.pathStyle = true
	}
	if _, err := url.ParseRequestURI(cfg.endpoint); err != nil {
		return s3Config{}, fmt.Errorf("invalid S3 endpoint: %w", err)
	}
	return cfg, nil
}

func pathEscape(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func signS3Request(req *http.Request, payload []byte, cfg s3Config, now time.Time) {
	payloadSum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(payloadSum[:])
	date := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	headers := "host:" + req.URL.Host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	if cfg.sessionToken != "" {
		req.Header.Set("x-amz-security-token", cfg.sessionToken)
		headers += "x-amz-security-token:" + cfg.sessionToken + "\n"
		signedHeaders += ";x-amz-security-token"
	}
	canonical := req.Method + "\n" + req.URL.EscapedPath() + "\n\n" + headers + "\n" + signedHeaders + "\n" + payloadHash
	canonicalSum := sha256.Sum256([]byte(canonical))
	scope := date + "/" + cfg.region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(canonicalSum[:])
	kDate := hmacSHA256([]byte("AWS4"+cfg.secretKey), date)
	kRegion := hmacSHA256(kDate, cfg.region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cfg.accessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
