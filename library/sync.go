package library

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

type s3Config struct {
	endpoint, bucket, key, region, accessKey, secretKey, sessionToken string
	pathStyle                                                         bool
}

func (l *sqliteLibrary) SyncPush(ctx context.Context) (err error) {
	defer func() { err = typedError(err) }()
	snapshot, err := os.CreateTemp("", ".library-sync-*.db")
	if err != nil {
		return fmt.Errorf("create sync snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	defer os.Remove(snapshotPath)
	snapshotDB, err := openExistingLibrary(ctx, snapshotPath)
	if err != nil {
		return fmt.Errorf("open sync snapshot: %w", err)
	}
	if err := backupDatabase(ctx, snapshotDB, l.db); err != nil {
		_ = snapshotDB.Close()
		return fmt.Errorf("snapshot library: %w", err)
	}
	if err := snapshotDB.Close(); err != nil {
		return fmt.Errorf("close sync snapshot: %w", err)
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
	data, err := s3Request(ctx, http.MethodGet, nil)
	if err != nil {
		return err
	}
	download, err := os.CreateTemp("", ".library-pull-*.db")
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
	defer check.Close()
	var integrity string
	err = check.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity)
	var hasSkillsTable int
	if err == nil {
		err = check.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'skills'`).Scan(&hasSkillsTable)
	}
	if err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	if integrity != "ok" || hasSkillsTable != 1 {
		return fmt.Errorf("validate pulled library: integrity=%q skills_table=%d", integrity, hasSkillsTable)
	}
	if err := remapProjectPaths(ctx, check, os.Getenv("SKILLS_PROJECT_PATH_MAP")); err != nil {
		return fmt.Errorf("remap pulled projects: %w", err)
	}
	return backupDatabase(ctx, l.db, check)
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

func backupDatabase(ctx context.Context, destination, source *sql.DB) error {
	destinationConn, err := destination.Conn(ctx)
	if err != nil {
		return err
	}
	defer destinationConn.Close()
	sourceConn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer sourceConn.Close()
	return destinationConn.Raw(func(destinationDriver any) error {
		destinationSQLite, ok := destinationDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("unexpected destination SQLite driver %T", destinationDriver)
		}
		return sourceConn.Raw(func(sourceDriver any) error {
			sourceSQLite, ok := sourceDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("unexpected source SQLite driver %T", sourceDriver)
			}
			backup, err := destinationSQLite.Backup("main", sourceSQLite, "main")
			if err != nil {
				return err
			}
			for {
				done, err := backup.Step(128)
				if err != nil {
					_ = backup.Close()
					return err
				}
				if done {
					return backup.Close()
				}
				select {
				case <-ctx.Done():
					_ = backup.Close()
					return ctx.Err()
				case <-time.After(time.Millisecond):
				}
			}
		})
	})
}

func remapProjectPaths(ctx context.Context, db *sql.DB, raw string) error {
	paths := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			return fmt.Errorf("decode SKILLS_PROJECT_PATH_MAP: %w", err)
		}
	}
	projectRows, err := db.QueryContext(ctx, `SELECT path FROM projects`)
	if err != nil {
		return err
	}
	var remoteProjects []string
	for projectRows.Next() {
		var path string
		if err := projectRows.Scan(&path); err != nil {
			_ = projectRows.Close()
			return err
		}
		remoteProjects = append(remoteProjects, path)
	}
	if err := projectRows.Err(); err != nil {
		_ = projectRows.Close()
		return err
	}
	if err := projectRows.Close(); err != nil {
		return err
	}
	oldPaths := make([]string, 0, len(paths))
	for old, newPath := range paths {
		if old == "" || newPath == "" {
			return fmt.Errorf("project path mapping cannot be empty")
		}
		absolute, err := filepath.Abs(newPath)
		if err != nil {
			return err
		}
		paths[old] = filepath.Clean(absolute)
		oldPaths = append(oldPaths, old)
	}
	sort.Slice(oldPaths, func(i, j int) bool { return len(oldPaths[i]) > len(oldPaths[j]) })
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	for _, old := range oldPaths {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET path = ? WHERE path = ?`, paths[old], old); err != nil {
			return err
		}
	}
	var hasActivationGroups int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'activation_groups'`).Scan(&hasActivationGroups); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT skill_id, agent, scope_path FROM activations`)
	if err != nil {
		return err
	}
	type activationPath struct {
		skillID SkillID
		agent   Agent
		path    string
	}
	var activations []activationPath
	for rows.Next() {
		var activation activationPath
		if err := rows.Scan(&activation.skillID, &activation.agent, &activation.path); err != nil {
			_ = rows.Close()
			return err
		}
		activations = append(activations, activation)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, activation := range activations {
		mapped := activation.path
		for _, old := range oldPaths {
			if candidate, ok := remapPath(activation.path, old, paths[old]); ok {
				mapped = candidate
				break
			}
		}
		if mapped == activation.path && !isProjectScope(activation.path, remoteProjects) {
			switch activation.agent {
			case AgentClaudeCode:
				mapped = filepath.Join(home, ".claude", "skills")
			case AgentCodex:
				mapped = filepath.Join(home, ".codex", "skills")
			}
		}
		if mapped != activation.path {
			if hasActivationGroups == 1 {
				if _, err := tx.ExecContext(ctx, `UPDATE activation_groups SET scope_path = ? WHERE skill_id = ? AND agent = ? AND scope_path = ?`, mapped, activation.skillID, activation.agent, activation.path); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE activations SET scope_path = ? WHERE skill_id = ? AND agent = ? AND scope_path = ?`, mapped, activation.skillID, activation.agent, activation.path); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func isProjectScope(path string, projects []string) bool {
	for _, project := range projects {
		if _, ok := remapPath(path, project, project); ok {
			return true
		}
	}
	return false
}

func remapPath(path, old, newPath string) (string, bool) {
	if path == old {
		return newPath, true
	}
	for _, separator := range []string{"/", `\`} {
		if strings.HasPrefix(path, old+separator) {
			relative := strings.ReplaceAll(path[len(old)+1:], `\`, "/")
			return filepath.Join(newPath, filepath.FromSlash(relative)), true
		}
	}
	return path, false
}

func s3Request(ctx context.Context, method string, body []byte) ([]byte, error) {
	cfg, err := loadS3Config()
	if err != nil {
		return nil, err
	}
	resource := "/" + awsURIEncode(cfg.key, true)
	if cfg.pathStyle {
		resource = "/" + awsURIEncode(cfg.bucket, false) + resource
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

func awsURIEncode(value string, preserveSlash bool) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for i := 0; i < len(value); i++ {
		character := value[i]
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) || preserveSlash && character == '/' {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&15])
	}
	return encoded.String()
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
