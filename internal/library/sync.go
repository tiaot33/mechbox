package library

import (
	"bytes"
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
	"maps"
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

// pullInstallationWarning 提示 pull 后 agent 目录安装项未随之重建,可能与新库不一致。
const pullInstallationWarning = "agent 目录里的安装项未随本次 pull 重建,可能与新库不一致"

func (l *sqliteLibrary) PlanSyncPush(ctx context.Context) (plan SyncPlan, err error) {
	// 纯网络探测,不碰库,不加锁。
	defer func() { err = typedError(err) }()
	identity, exists, err := remoteIdentity(ctx)
	if err != nil {
		return SyncPlan{}, err
	}
	return SyncPlan{Remote: identity, RemoteExists: exists}, nil
}

func (l *sqliteLibrary) PlanSyncPull(ctx context.Context) (plan SyncPlan, err error) {
	defer func() { err = typedError(err) }()
	data, etag, err := s3Request(ctx, http.MethodGet, nil, nil)
	if err != nil {
		return SyncPlan{}, err
	}
	_, cleanup, err := openPulledLibrary(ctx, data)
	if err == nil {
		defer cleanup()
	} else if !unacceptableRemote(err) {
		return SyncPlan{}, err
	}
	schemaOK := err == nil
	plan = SyncPlan{Remote: RemoteIdentity(etag), RemoteExists: true, SchemaOK: schemaOK}
	if !schemaOK {
		plan.Warnings = append(plan.Warnings, "远端库的 schema 不可接受,pull 将被拒绝")
	}
	plan.Warnings = append(plan.Warnings, pullInstallationWarning)
	return plan, nil
}

func (l *sqliteLibrary) SyncPush(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
	// 三段式:HEAD(无锁)→ 快照(共享锁,只为与 SyncPull 的整库替换互斥)→ PUT(无锁,快照已在内存)。
	defer func() { err = typedError(err) }()
	identity, _, err := remoteIdentity(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	if identity != expected {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync push: remote identity changed since sync plan"}
	}
	data, err := l.snapshotBytes(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	// 条件写让 check-then-act 变原子:HEAD 查询只是快速失败,真正的守卫是这里的前置条件——
	// 同步计划时无对象则要求写入时仍无对象(If-None-Match: *),否则要求远端仍是同步计划里的身份
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
		return SyncResult{}, &Error{Code: Conflict, Message: "sync pull: remote object deleted since sync plan"}
	}
	if err != nil {
		return SyncResult{}, err
	}
	if RemoteIdentity(etag) != expected {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync pull: remote identity changed since sync plan"}
	}
	check, cleanup, err := openPulledLibrary(ctx, data)
	if err != nil {
		if unacceptableRemote(err) {
			failure := preconditionError("sync pull", "remote library is not acceptable", ErrorDetails{})
			failure.Err = err
			return SyncResult{}, failure
		}
		return SyncResult{}, err
	}
	defer cleanup()
	if err := remapProjectPaths(ctx, check, os.Getenv("SKILLS_PROJECT_PATH_MAP")); err != nil {
		return SyncResult{}, fmt.Errorf("remap pulled projects: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// 拉库后按本机 $HOME 解释全局 Scope,与「换机器再 Open」一致。
	home, err := os.UserHomeDir()
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync pull: find home directory: %w", err)
	}
	l.home = home
	if err := backupDatabase(ctx, l.db, check); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Remote: RemoteIdentity(etag), Warnings: []string{pullInstallationWarning}}, nil
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

// snapshotBytes 在共享锁内生成当前库的事务性快照并读入内存;锁只覆盖快照本身,不覆盖上传。
func (l *sqliteLibrary) snapshotBytes(ctx context.Context) ([]byte, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	snapshot, err := os.CreateTemp("", ".library-sync-*.db")
	if err != nil {
		return nil, fmt.Errorf("create sync snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	defer os.Remove(snapshotPath)
	snapshotDB, err := openLibraryDB(ctx, snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("open sync snapshot: %w", err)
	}
	if err := backupDatabase(ctx, snapshotDB, l.db); err != nil {
		_ = snapshotDB.Close()
		return nil, fmt.Errorf("snapshot library: %w", err)
	}
	// backup 会把本库文件头里的 WAL 标记一并复制;推送的镜像要保持 rollback journal 才能与老版本互通。
	var mode string
	err = snapshotDB.QueryRowContext(ctx, `PRAGMA journal_mode = DELETE`).Scan(&mode)
	if err == nil && mode != "delete" {
		err = fmt.Errorf("journal_mode stayed %q", mode)
	}
	if err != nil {
		_ = snapshotDB.Close()
		return nil, fmt.Errorf("reset sync snapshot journal mode: %w", err)
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
	check, err = openLibraryDB(ctx, downloadPath)
	if err == nil {
		err = validatePulledDatabase(ctx, check)
	}
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("validate pulled library: %w", err)
	}
	return check, cleanup, nil
}

// unacceptableRemote 判断拉取内容是否为不可接受的远端库(schema / 损坏 / 不是数据库)。
func unacceptableRemote(err error) bool {
	if errors.Is(err, errUnacceptableSchema) {
		return true
	}
	sqliteErr, ok := errors.AsType[sqlite3.Error](err)
	return ok && (sqliteErr.Code == sqlite3.ErrCorrupt || sqliteErr.Code == sqlite3.ErrNotADB)
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

// remapProjectPaths 按 SKILLS_PROJECT_PATH_MAP 把拉回库里 Registered project 的路径改成本机路径。
// 只改 projects 表:安装项记录只记 Scope 身份,拉库后目录一律按本机 $HOME / 映射后的项目根 Resolve,
// 不再改写任何绝对路径(见 ADR 0011)。
//
// 每个项目只按它在拉回库里记录的原路径查一次映射、按 id 改写,已改写的结果不再参与匹配,
// 因此 {A→B, B→C} 确定地得到 A 的项目落在 B、B 的项目落在 C。改写顺序先在内存里按
// 「目标路径尚未被占用」推演好再落盘;目标被不会迁走的项目占着、两个项目指向同一路径或
// 映射成环(A↔B 互换)都是配置错误,返回 InvalidArgument 并回滚,不留下一半改写。
func remapProjectPaths(ctx context.Context, db *sql.DB, raw string) error {
	paths := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			return &Error{Code: InvalidArgument, Message: "decode SKILLS_PROJECT_PATH_MAP: " + err.Error(), Err: err}
		}
	}
	for old, newPath := range paths {
		if old == "" || newPath == "" {
			return &Error{Code: InvalidArgument, Message: "SKILLS_PROJECT_PATH_MAP: project path mapping cannot be empty"}
		}
		absolute, err := filepath.Abs(newPath)
		if err != nil {
			return err
		}
		paths[old] = filepath.Clean(absolute)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// current 是拉回库里每个项目的当前路径;pending 是尚待改写的项目及其目标路径。
	current := map[ProjectID]string{}
	pending := map[ProjectID]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id, path FROM projects`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id ProjectID
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			_ = rows.Close()
			return err
		}
		current[id] = path
		if newPath, ok := paths[path]; ok && newPath != path {
			pending[id] = newPath
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	occupied := map[string]bool{}
	for _, path := range current {
		occupied[path] = true
	}
	var order []ProjectID
	for len(pending) > 0 {
		progressed := false
		for _, id := range slices.Sorted(maps.Keys(pending)) {
			if occupied[pending[id]] {
				continue
			}
			delete(occupied, current[id])
			occupied[pending[id]] = true
			order = append(order, id)
			delete(pending, id)
			progressed = true
		}
		if !progressed {
			var stuck []string
			for _, id := range slices.Sorted(maps.Keys(pending)) {
				stuck = append(stuck, current[id]+" -> "+pending[id])
			}
			return &Error{Code: InvalidArgument, Message: "SKILLS_PROJECT_PATH_MAP: target path still held by another registered project (occupied target or cyclic mapping): " + strings.Join(stuck, ", ")}
		}
	}
	for _, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET path = ? WHERE id = ?`, paths[current[id]], id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// s3Request 发起一次已签名的 S3 请求,返回响应体与对象的 ETag(远端身份)。
// 对象不存在(404)映射为 NotFound,便于同步计划区分"远端尚无对象"与真正的故障。
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
		return nil, "", &Error{Code: Conflict, Message: "remote object changed since sync plan"}
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
