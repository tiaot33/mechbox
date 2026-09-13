package library

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

type s3Config struct {
	endpoint, bucket, key, region, accessKey, secretKey, sessionToken string
	pathStyle                                                         bool
}

// pullProjectionWarning 提示 pull 后 agent 目录投影未随之重建,可能与新库不一致。
const pullProjectionWarning = "agent 目录投影未随本次 pull 重建,可能与新库不一致"

func (l *sqliteLibrary) PreviewSyncPush(ctx context.Context) (preview SyncPreview, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	identity, exists, err := remoteIdentity(ctx)
	if err != nil {
		return SyncPreview{}, err
	}
	return SyncPreview{Remote: identity, RemoteExists: exists}, nil
}

func (l *sqliteLibrary) PreviewSyncPull(ctx context.Context) (preview SyncPreview, err error) {
	defer func() { err = typedError(err) }()
	data, etag, err := s3Request(ctx, http.MethodGet, nil, nil)
	if err != nil {
		return SyncPreview{}, err
	}
	_, cleanup, err := openPulledLibrary(ctx, data)
	if err == nil {
		defer cleanup()
	} else if !errors.Is(err, errUnacceptableSchema) {
		sqliteErr, ok := errors.AsType[sqlite3.Error](err)
		if !ok || (sqliteErr.Code != sqlite3.ErrCorrupt && sqliteErr.Code != sqlite3.ErrNotADB) {
			return SyncPreview{}, err
		}
	}
	schemaOK := err == nil
	preview = SyncPreview{Remote: RemoteIdentity(etag), RemoteExists: true, SchemaOK: schemaOK}
	if !schemaOK {
		preview.Warnings = append(preview.Warnings, "远端库的 schema 不可接受,pull 将被拒绝")
	}
	preview.Warnings = append(preview.Warnings, pullProjectionWarning)
	return preview, nil
}

func (l *sqliteLibrary) SyncPush(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	identity, _, err := remoteIdentity(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	if identity != expected {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync push: remote identity changed since preview"}
	}
	data, err := l.snapshotBytes(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	// 条件写让 check-then-act 变原子:HEAD 预检只是快速失败,真正的守卫是这里的前置条件——
	// 预览时无对象则要求写入时仍无对象(If-None-Match: *),否则要求远端仍是预览时的身份
	// (If-Match: expected)。窗口内远端若被改动,服务端拒绝(412),不会覆盖。
	conditions := map[string]string{"If-Match": string(expected)}
	if expected == "" {
		conditions = map[string]string{"If-None-Match": "*"}
	}
	_, etag, err := s3Request(ctx, http.MethodPut, data, conditions)
	if err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Remote: RemoteIdentity(etag)}, nil
}

func (l *sqliteLibrary) SyncPull(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
	defer func() { err = typedError(err) }()
	data, etag, err := s3Request(ctx, http.MethodGet, nil, nil)
	if IsCode(err, NotFound) && expected != "" {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync pull: remote object deleted since preview"}
	}
	if err != nil {
		return SyncResult{}, err
	}
	if RemoteIdentity(etag) != expected {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync pull: remote identity changed since preview"}
	}
	check, cleanup, err := openPulledLibrary(ctx, data)
	if err != nil {
		return SyncResult{}, err
	}
	defer cleanup()
	if err := remapProjectPaths(ctx, check, os.Getenv("SKILLS_PROJECT_PATH_MAP")); err != nil {
		return SyncResult{}, fmt.Errorf("remap pulled projects: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := backupDatabase(ctx, l.db, check); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Remote: RemoteIdentity(etag), Warnings: []string{pullProjectionWarning}}, nil
}

// remoteIdentity 通过 HEAD 读取远端对象的身份;对象不存在时返回 ("", false, nil)。
func remoteIdentity(ctx context.Context) (RemoteIdentity, bool, error) {
	_, etag, err := s3Request(ctx, http.MethodHead, nil, nil)
	if err != nil {
		if IsCode(err, NotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return RemoteIdentity(etag), true, nil
}

// snapshotBytes 生成当前库的事务性快照并读入内存。
func (l *sqliteLibrary) snapshotBytes(ctx context.Context) ([]byte, error) {
	snapshot, err := os.CreateTemp("", ".library-sync-*.db")
	if err != nil {
		return nil, fmt.Errorf("create sync snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	defer os.Remove(snapshotPath)
	snapshotDB, err := openExistingLibrary(ctx, snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("open sync snapshot: %w", err)
	}
	if err := backupDatabase(ctx, snapshotDB, l.db); err != nil {
		_ = snapshotDB.Close()
		return nil, fmt.Errorf("snapshot library: %w", err)
	}
	if err := snapshotDB.Close(); err != nil {
		return nil, fmt.Errorf("close sync snapshot: %w", err)
	}
	// ponytail: full-replace sync holds one database image in memory; stream when libraries become large enough to measure.
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("read sync snapshot: %w", err)
	}
	return data, nil
}

// errUnacceptableSchema 只标记已确认的远端库校验失败,不包含本地 I/O 或取消。
var errUnacceptableSchema = errors.New("unacceptable library schema")

// openPulledLibrary 写入并校验临时库;失败时自行清理,成功后由调用方 defer cleanup()。
func openPulledLibrary(ctx context.Context, data []byte) (*sql.DB, func(), error) {
	download, err := os.CreateTemp("", ".library-pull-*.db")
	if err != nil {
		return nil, nil, fmt.Errorf("create pull file: %w", err)
	}
	downloadPath := download.Name()
	var check *sql.DB
	cleanup := func() {
		if check != nil {
			_ = check.Close()
		}
		_ = os.Remove(downloadPath)
	}
	if _, err := download.Write(data); err != nil {
		_ = download.Close()
		cleanup()
		return nil, nil, fmt.Errorf("write pulled library: %w", err)
	}
	if err := download.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("close pulled library: %w", err)
	}
	check, err = openExistingLibrary(ctx, downloadPath)
	if err == nil {
		err = validatePulledDatabase(ctx, check)
	}
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("validate pulled library: %w", err)
	}
	return check, cleanup, nil
}

// validatePulledDatabase 校验一个已打开的拉取库:完整性 + schema 身份。
func validatePulledDatabase(ctx context.Context, check *sql.DB) error {
	var integrity string
	if err := check.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("%w: integrity=%q", errUnacceptableSchema, integrity)
	}
	if err := validateLibrarySchema(ctx, check); err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
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

func validateLibrarySchema(ctx context.Context, db *sql.DB) error {
	var applicationID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if applicationID != libraryApplicationID || userVersion != librarySchemaVersion {
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
	actual, err := schemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	expectedDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer expectedDB.Close()
	if _, err := expectedDB.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	expected, err := schemaFingerprint(ctx, expectedDB)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: schema fingerprint mismatch", errUnacceptableSchema)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("%w: foreign key check failed", errUnacceptableSchema)
	}
	return rows.Err()
}

func schemaFingerprint(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\n%s\n", name, strings.Join(strings.Fields(statement), " "))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
	slices.SortFunc(oldPaths, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
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
			if _, err := tx.ExecContext(ctx, `UPDATE activation_groups SET scope_path = ? WHERE skill_id = ? AND agent = ? AND scope_path = ?`, mapped, activation.skillID, activation.agent, activation.path); err != nil {
				return err
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

// s3Request 发起一次已签名的 S3 请求,返回响应体与对象的 ETag(远端身份)。
// 对象不存在(404)映射为 NotFound,便于预览区分"远端尚无对象"与真正的故障。
// conditions 里的条件头(If-Match / If-None-Match)不参与签名(SigV4 只签指定头);
// 服务端据其判定前置条件,不满足时返回 412,映射为 Conflict。
func s3Request(ctx context.Context, method string, body []byte, conditions map[string]string) ([]byte, string, error) {
	cfg, err := loadS3Config()
	if err != nil {
		return nil, "", err
	}
	resource := "/" + awsURIEncode(cfg.key, true)
	if cfg.pathStyle {
		resource = "/" + awsURIEncode(cfg.bucket, false) + resource
	}
	requestURL := strings.TrimRight(cfg.endpoint, "/") + resource
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create S3 request: %w", err)
	}
	signS3Request(req, body, cfg, time.Now().UTC())
	for name, value := range conditions {
		req.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", &Error{Code: SourceUnreachable, Message: "reach S3: " + err.Error(), Err: err}
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", &Error{Code: SourceUnreachable, Message: "read S3 response: " + err.Error(), Err: err}
	}
	var s3Error struct {
		Code string `xml:"Code"`
	}
	_ = xml.Unmarshal(response, &s3Error)
	permissionCode := map[string]bool{"AccessDenied": true, "InvalidAccessKeyId": true, "SignatureDoesNotMatch": true, "ExpiredToken": true, "InvalidToken": true}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || permissionCode[s3Error.Code] {
		return nil, "", &Error{Code: Permission, Message: "S3 permission denied"}
	}
	if resp.StatusCode == http.StatusNotFound || s3Error.Code == "NoSuchKey" {
		return nil, "", &Error{Code: NotFound, Message: "S3 object not found"}
	}
	if resp.StatusCode == http.StatusPreconditionFailed || s3Error.Code == "PreconditionFailed" {
		return nil, "", &Error{Code: Conflict, Message: "remote object changed since preview"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &Error{Code: SourceUnreachable, Message: fmt.Sprintf("S3 returned %s", resp.Status)}
	}
	etag := resp.Header.Get("ETag")
	// 已存在的对象必须提供身份,不能把缺失 ETag 当作远端无对象。
	if etag == "" && method != http.MethodPut {
		return nil, "", &Error{Code: SourceUnreachable, Message: "S3 response missing ETag"}
	}
	return response, etag, nil
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
		return s3Config{}, &Error{Code: NotConfigured, Message: "sync: S3 bucket and AWS credentials are required"}
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
			err = fmt.Errorf("endpoint must be an http(s) origin without credentials, query, or fragment")
		}
		return s3Config{}, &Error{Code: NotConfigured, Message: "invalid S3 endpoint: " + err.Error(), Err: err}
	}
	return cfg, nil
}

func awsURIEncode(value string, preserveSlash bool) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for i := range len(value) {
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
