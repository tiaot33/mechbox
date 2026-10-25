package library

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/s3"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// DefaultSyncKeep 是没填保留份数时,S3 上保留这台机器的备份份数。
const DefaultSyncKeep = 10

// SyncInterval 是自动推送的间隔;空字符串为不自动推送。
type SyncInterval string

const (
	// SyncHourly 每小时推送一次。
	SyncHourly SyncInterval = "hourly"
	// SyncDaily 每天推送一次。
	SyncDaily SyncInterval = "daily"
	// SyncWeekly 每周推送一次。
	SyncWeekly SyncInterval = "weekly"
)

// Period 返回间隔的时长;不自动推送(空字符串)或不认识的间隔返回 false。
func (i SyncInterval) Period() (time.Duration, bool) {
	switch i {
	case SyncHourly:
		return time.Hour, true
	case SyncDaily:
		return 24 * time.Hour, true
	case SyncWeekly:
		return 7 * 24 * time.Hour, true
	}
	return 0, false
}

// SyncSettings 是同步用的 S3 设置,用 [Library.SetSyncSettings] 存进数据库。
// 留空的项:文件夹留空时备份放在存储桶根下,区域默认 us-east-1,服务地址留空时按存储桶与区域使用 AWS S3,
// 保留份数为 0 时按 [DefaultSyncKeep],自动推送留空时不自动推送。
// 备份的对象名由库决定,不能设置:<文件夹>/<设备目录>/library-<推送时间>.db,设备目录见 [SyncStatus].Device。
type SyncSettings struct {
	// Endpoint 是服务地址。
	Endpoint string `json:"endpoint,omitempty"`
	// Bucket 是存储桶。
	Bucket string `json:"bucket"`
	// Folder 是存储桶里放备份的文件夹,可以多级(team/mechbox);首尾的 / 与空的一级去掉。
	Folder string `json:"folder,omitempty"`
	// Region 是区域。
	Region string `json:"region,omitempty"`
	// AccessKeyID 是访问密钥的 ID。
	AccessKeyID string `json:"access_key_id"`
	// SecretAccessKey 是访问密钥。
	SecretAccessKey string `json:"secret_access_key"`
	// SessionToken 是临时凭证的会话令牌;用长期密钥时留空。
	SessionToken string `json:"session_token,omitempty"`
	// Keep 是 S3 上保留这台机器的备份份数;每次推送后删掉这台机器超出份数的最旧备份,别的设备的不动。
	Keep int `json:"keep,omitempty"`
	// AutoPush 是自动推送的间隔。库只存取;按间隔调用 [Library.SyncPush] 的是应用,拉回从不自动进行。
	AutoPush SyncInterval `json:"auto_push,omitempty"`
}

// SyncSettings 返回存在数据库里的同步设置(设置页显示它);没填的项为空(保留份数为 0)。
func (l *Library) SyncSettings(ctx context.Context) (settings SyncSettings, err error) {
	defer func() { err = typedError(err) }()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	stored, err := l.store.SyncSettings(ctx)
	if err != nil {
		return SyncSettings{}, err
	}
	return syncSettings(stored), nil
}

// SetSyncSettings 把同步用的 S3 设置(各项去掉首尾空白,文件夹去掉首尾的 /)存进数据库,之后的推送、列出备份与拉回立即使用;
// 留空的项从数据库删掉,全部留空即清掉。只认数据库里的这一组,不读环境变量;缺少存储桶或密钥时同步方法返回 NotConfigured。
// 保留份数小于 0、自动推送的间隔不认识时返回 InvalidArgument(Details.Field 为 keep / auto_push),什么都不存。
// 同步设置只属于这台机器:推送出去的库里不带,拉回时保留本机的(新机器得先有它才能拉回;各台机器也可能用不同的 Access Key)。
func (l *Library) SetSyncSettings(ctx context.Context, settings SyncSettings) (err error) {
	defer func() { err = typedError(err) }()
	for _, field := range []*string{
		&settings.Endpoint, &settings.Bucket, &settings.Folder, &settings.Region,
		&settings.AccessKeyID, &settings.SecretAccessKey, &settings.SessionToken,
	} {
		*field = strings.TrimSpace(*field)
	}
	settings.Folder = syncFolder(settings.Folder)
	if settings.Keep < 0 {
		return argumentError("set sync settings", "keep", "keep must not be negative")
	}
	if _, ok := settings.AutoPush.Period(); !ok && settings.AutoPush != "" {
		return argumentError("set sync settings", "auto_push", "unknown auto push interval "+strconv.Quote(string(settings.AutoPush)))
	}
	stored := store.SyncSettings{
		Endpoint: settings.Endpoint, Bucket: settings.Bucket, Folder: settings.Folder, Region: settings.Region,
		AccessKeyID: settings.AccessKeyID, SecretAccessKey: settings.SecretAccessKey, SessionToken: settings.SessionToken,
		AutoPush: string(settings.AutoPush),
	}
	if settings.Keep > 0 {
		stored.Keep = strconv.Itoa(settings.Keep)
	}
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	return l.store.SetSyncSettings(ctx, stored)
}

func syncSettings(stored store.SyncSettings) SyncSettings {
	keep, _ := strconv.Atoi(stored.Keep)
	return SyncSettings{
		Endpoint: stored.Endpoint, Bucket: stored.Bucket, Folder: stored.Folder, Region: stored.Region,
		AccessKeyID: stored.AccessKeyID, SecretAccessKey: stored.SecretAccessKey, SessionToken: stored.SessionToken,
		Keep: max(keep, 0), AutoPush: SyncInterval(stored.AutoPush),
	}
}

func syncFolder(folder string) string {
	var parts []string
	for part := range strings.SplitSeq(folder, "/") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

// SyncStatus 是这台机器的同步状态,设置页据此写「这台机器的备份在 team/mechbox/MacBook-Pro-3f9a2c/,上次推送:3 小时前」。
type SyncStatus struct {
	// Device 是这台机器在 S3 上的设备目录名:主机名加一段随机后缀,第一次用到时生成,之后不变,不能设置;
	// 两台主机名相同的机器也各用各的目录。
	Device string `json:"device"`
	// LastPushedAt 是这台机器上次推送成功的时间(含 S3 上已是同样内容、没有新建备份的那次);没推送过为零值。
	// 应用据此和 [SyncSettings].AutoPush 判断该不该自动推送。
	LastPushedAt time.Time `json:"last_pushed_at,omitzero"`
}

// SyncStatus 返回这台机器的同步状态,不连 S3。设备目录名还没生成时在这里生成并存进数据库。
func (l *Library) SyncStatus(ctx context.Context) (status SyncStatus, err error) {
	defer func() { err = typedError(err) }()
	state, err := l.syncState(ctx)
	if err != nil {
		return SyncStatus{}, err
	}
	status.Device = state.Device
	if at, err := time.Parse(time.RFC3339Nano, state.LastPushedAt); err == nil {
		status.LastPushedAt = at
	}
	return status, nil
}

func (l *Library) syncState(ctx context.Context) (store.SyncState, error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	state, err := l.store.SyncState(ctx)
	if err != nil || state.Device != "" {
		return state, err
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	state.Device = deviceName() + "-" + hex.EncodeToString(suffix)
	return state, l.store.SetSyncState(ctx, state)
}

func (l *Library) recordPush(ctx context.Context, at time.Time) error {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	state, err := l.store.SyncState(ctx)
	if err != nil {
		return err
	}
	state.LastPushedAt = at.UTC().Format(time.RFC3339Nano)
	return l.store.SetSyncState(ctx, state)
}

func deviceName() string {
	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, name)
	name = strings.Trim(name, "-.")
	if name == "" {
		return "device"
	}
	return name
}

type syncTarget struct {
	s3     s3.Settings
	folder string
	keep   int
}

func (l *Library) syncTarget(ctx context.Context) (syncTarget, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	stored, err := l.store.SyncSettings(ctx)
	if err != nil {
		return syncTarget{}, err
	}
	settings := syncSettings(stored)
	target := syncTarget{
		s3: s3.Settings{
			Endpoint: settings.Endpoint, Bucket: settings.Bucket, Region: settings.Region,
			AccessKeyID: settings.AccessKeyID, SecretAccessKey: settings.SecretAccessKey, SessionToken: settings.SessionToken,
		},
		folder: settings.Folder,
		keep:   cmp.Or(settings.Keep, DefaultSyncKeep),
	}
	if target.s3.Bucket == "" || target.s3.AccessKeyID == "" || target.s3.SecretAccessKey == "" {
		return syncTarget{}, fmt.Errorf("S3 bucket and AWS credentials are required: %w", s3.ErrNotConfigured)
	}
	return target, nil
}

func (t syncTarget) prefix() string {
	if t.folder == "" {
		return ""
	}
	return t.folder + "/"
}

const (
	backupPrefix     = "library-"
	backupSuffix     = ".db"
	backupTimeLayout = "20060102T150405.000Z"
)

// SyncBackupID 标识 S3 上的一份备份:从 [SyncBackup].ID 原样传回 [Library.PlanSyncPull] / [Library.SyncPull],界面不展示。
type SyncBackupID string

// SyncBackup 是 S3 上的一份备份,界面据此写「MacBook-Pro-3f9a2c 于 3 天前推送」,让用户认出拉回会恢复的是哪一份。
type SyncBackup struct {
	// ID 原样传回 [Library.PlanSyncPull] / [Library.SyncPull]。
	ID SyncBackupID `json:"id"`
	// Device 是推送它的机器的设备目录名([SyncStatus].Device)。
	Device string `json:"device"`
	// ThisDevice 表示是这台机器推送的。
	ThisDevice bool `json:"this_device"`
	// PushedAt 是推送的时间,按推送那台机器的时钟,精确到毫秒。
	PushedAt time.Time `json:"pushed_at"`
}

// SyncPushResult 是一次推送的结果。
type SyncPushResult struct {
	// Backup 是 S3 上这台机器最新的那份备份:刚推送的,或 Unchanged 时原有的那份。
	Backup SyncBackup `json:"backup"`
	// Unchanged 表示本机的库和这台机器最新的那份备份内容相同,没有新建备份。
	Unchanged bool `json:"unchanged,omitempty"`
}

// SyncPush 把本机的库推送到 S3,作为这台机器的一份新备份:<文件夹>/<设备目录>/library-<推送时间>.db。
// 推送的是调用时本机库的完整内容(只属于这台机器的同步设置与 WSL 发行版除外)。从不覆盖已有的备份,
// 本机的库和这台机器最新的那份备份内容相同时不新建(Unchanged),免得自动推送用同样的副本占满份数。
// 之后删掉这台机器超出保留份数([SyncSettings].Keep)的最旧备份;别的设备的备份不动。
// 手动推送与应用按 [SyncSettings].AutoPush 自动推送都调用它,不需要先生成计划。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable;
// 备份已经传上去、删旧备份失败时也返回这些错误(下一次推送会再删),[SyncStatus].LastPushedAt 照样更新。
func (l *Library) SyncPush(ctx context.Context) (result SyncPushResult, err error) {
	defer func() { err = typedError(err) }()
	target, err := l.syncTarget(ctx)
	if err != nil {
		return SyncPushResult{}, err
	}
	state, err := l.syncState(ctx)
	if err != nil {
		return SyncPushResult{}, err
	}
	devicePrefix := target.prefix() + state.Device + "/"
	objects, err := s3.List(ctx, target.s3, devicePrefix)
	if err != nil {
		return SyncPushResult{}, err
	}
	own := backupObjects(objects, devicePrefix)
	data, digest, err := l.snapshot(ctx)
	if err != nil {
		return SyncPushResult{}, err
	}
	unchanged := false
	if len(own) > 0 {
		newest, err := s3.Head(ctx, target.s3, own[len(own)-1].Key)
		if err != nil && !errors.Is(err, s3.ErrNotFound) {
			return SyncPushResult{}, err
		}
		unchanged = err == nil && newest.SHA256 == digest
	}
	if unchanged {
		result = SyncPushResult{Backup: syncBackup(own[len(own)-1], target.prefix(), state.Device), Unchanged: true}
	} else {
		now := time.Now().UTC()
		key := devicePrefix + backupPrefix + now.Format(backupTimeLayout) + backupSuffix
		object, err := s3.Put(ctx, target.s3, key, data, digest)
		if err != nil {
			return SyncPushResult{}, err
		}
		own = append(own, object)
		slices.SortFunc(own, func(a, b s3.Object) int { return strings.Compare(a.Key, b.Key) })
		result = SyncPushResult{Backup: syncBackup(object, target.prefix(), state.Device)}
	}
	if err := l.recordPush(ctx, time.Now()); err != nil {
		return SyncPushResult{}, err
	}
	for _, old := range own[:max(len(own)-target.keep, 0)] {
		if err := s3.Delete(ctx, target.s3, old.Key); err != nil && !errors.Is(err, s3.ErrNotFound) {
			return result, fmt.Errorf("sync push: remove old backup %s: %w", old.Key, err)
		}
	}
	return result, nil
}

func backupObjects(objects []s3.Object, devicePrefix string) []s3.Object {
	var backups []s3.Object
	for _, object := range objects {
		name, ok := strings.CutPrefix(object.Key, devicePrefix)
		if ok && isBackupName(name) {
			backups = append(backups, object)
		}
	}
	slices.SortFunc(backups, func(a, b s3.Object) int { return strings.Compare(a.Key, b.Key) })
	return backups
}

func isBackupName(name string) bool {
	stamp, ok := strings.CutPrefix(name, backupPrefix)
	if !ok {
		return false
	}
	stamp, ok = strings.CutSuffix(stamp, backupSuffix)
	if !ok {
		return false
	}
	_, err := time.Parse(backupTimeLayout, stamp)
	return err == nil
}

func syncBackup(object s3.Object, prefix, thisDevice string) SyncBackup {
	device, name, _ := strings.Cut(strings.TrimPrefix(object.Key, prefix), "/")
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, backupPrefix), backupSuffix)
	pushed, _ := time.Parse(backupTimeLayout, stamp)
	return SyncBackup{
		ID:         SyncBackupID(object.ETag + " " + object.Key),
		Device:     device,
		ThisDevice: device == thisDevice,
		PushedAt:   pushed,
	}
}

// ListSyncBackups 列出 S3 上同步设置的文件夹里所有设备的备份,最新推送的在前;拉回前让用户选从哪一份恢复,默认选第一份。
// 文件夹里不是本应用备份的对象不列出。S3 上还没有备份时返回空列表。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
func (l *Library) ListSyncBackups(ctx context.Context) (backups []SyncBackup, err error) {
	defer func() { err = typedError(err) }()
	target, err := l.syncTarget(ctx)
	if err != nil {
		return nil, err
	}
	state, err := l.syncState(ctx)
	if err != nil {
		return nil, err
	}
	objects, err := s3.List(ctx, target.s3, target.prefix())
	if err != nil {
		return nil, err
	}
	backups = []SyncBackup{}
	for _, object := range objects {
		device, name, ok := strings.Cut(strings.TrimPrefix(object.Key, target.prefix()), "/")
		if ok && device != "" && isBackupName(name) {
			backups = append(backups, syncBackup(object, target.prefix(), state.Device))
		}
	}
	slices.SortFunc(backups, func(a, b SyncBackup) int {
		return cmp.Or(b.PushedAt.Compare(a.PushedAt), strings.Compare(string(b.ID), string(a.ID)))
	})
	return backups, nil
}

// CannotPullReason 是拉回的同步计划里不能拉回的原因,界面按它说明下一步该做什么。
type CannotPullReason string

const (
	// CannotPullNotALibrary 这份备份不是本应用的库,或已损坏。
	CannotPullNotALibrary CannotPullReason = "not_a_library"
	// CannotPullNewerApp 这份备份来自较新版本的应用:先把这台机器上的应用升级。
	CannotPullNewerApp CannotPullReason = "newer_app"
	// CannotPullOlderApp 这份备份来自较旧版本的应用:先在推送它的机器上升级应用,再推送一次。
	CannotPullOlderApp CannotPullReason = "older_app"
)

// SyncPullPlan 是拉回的同步计划:从选中的那份备份恢复会得到什么、能不能拉回。
// 只读,不替换本机的库。拉回会替换本机的整个库,Agent 目录里的文件夹不会改动(见 [Library.SyncPull])。
type SyncPullPlan struct {
	// CanPull 为 false 时 CannotPull 给出原因,此时 [Library.SyncPull] 返回 FailedPrecondition。
	CanPull    bool             `json:"can_pull"`
	CannotPull CannotPullReason `json:"cannot_pull,omitempty"`
	// Skills、Groups 是那份备份里的技能数与组数;仅 CanPull 时填写。
	Skills int `json:"skills"`
	Groups int `json:"groups"`
	// Projects 是那份备份里的已登记项目,仅 CanPull 时填写。Path 是推送那台机器上的路径,Missing 按这台机器现查:
	// 拉回后项目 ID 不变,Missing 的项目要用 [Library.RelocateProject] 指到它在这台机器上的位置。
	Projects []RegisteredProject `json:"projects,omitempty"`
}

// PlanSyncPull 生成拉回的同步计划:下载 [Library.ListSyncBackups] 列出的一份备份并校验,报告里面有什么、能不能拉回,
// 不替换本机的库。那份备份不能拉回时正常返回计划(CanPull 为 false),不返回错误。
// 那份备份列出之后被删掉(推送它的机器按保留份数删了旧的)或改写返回 Conflict,界面重新列出备份;
// backup 不是列出的标识返回 InvalidArgument。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
func (l *Library) PlanSyncPull(ctx context.Context, backup SyncBackupID) (plan SyncPullPlan, err error) {
	defer func() { err = typedError(err) }()
	data, err := l.downloadBackup(ctx, "plan sync pull", backup)
	if err != nil {
		return SyncPullPlan{}, err
	}
	image, err := store.OpenImage(ctx, data)
	if err != nil {
		reason, ok := cannotPull(err)
		if !ok {
			return SyncPullPlan{}, err
		}
		return SyncPullPlan{CannotPull: reason}, nil
	}
	defer image.Close()
	summary, err := image.Summary(ctx)
	if err != nil {
		return SyncPullPlan{}, err
	}
	projects, err := registeredProjects(summary.Projects)
	if err != nil {
		return SyncPullPlan{}, err
	}
	return SyncPullPlan{CanPull: true, Skills: summary.Skills, Groups: summary.Groups, Projects: projects}, nil
}

func (l *Library) downloadBackup(ctx context.Context, operation string, backup SyncBackupID) ([]byte, error) {
	etag, key, ok := strings.Cut(string(backup), " ")
	if !ok || etag == "" || key == "" {
		return nil, argumentError(operation, "backup", "not a sync backup id")
	}
	target, err := l.syncTarget(ctx)
	if err != nil {
		return nil, err
	}
	data, _, err := s3.Get(ctx, target.s3, key, etag)
	switch {
	case errors.Is(err, s3.ErrNotFound):
		return nil, &Error{Code: Conflict, Message: operation + ": backup was removed from S3"}
	case errors.Is(err, s3.ErrPreconditionFailed):
		return nil, &Error{Code: Conflict, Message: operation + ": backup changed on S3", Err: err}
	}
	return data, err
}

// SyncPull 用 S3 上的一份备份替换本机的整个库;backup 传 [Library.ListSyncBackups] 列出、用户在拉回计划里确认的那一份。
// 那份备份之后被删掉或改写返回 Conflict;不能拉回(计划里 CanPull 为 false)返回 FailedPrecondition;
// backup 不是列出的标识返回 InvalidArgument。这些情况下本机的库都不变。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
// 拉回从不自动进行,只在用户确认后调用。
//
// 拉回后:
//   - 技能、版本历史、组、安装记录、已登记项目都换成那份备份里的;本机原有、那份备份里没有的全部消失。
//   - Agent 目录里的文件夹一个都不动,Agent 用的内容在用户重新安装之前不会变。本机原来装出去、
//     那份备份里没有记录的文件夹留在原处,库不再管;那份备份记录的安装在本机可能还没有文件夹或内容不同,
//     用 [Library.InspectSkillScopes] 查看、[Library.Reinstall] 装回。
//   - 已登记项目的路径是推送那台机器上的;在本机不存在的项目([RegisteredProject].Missing)用
//     [Library.RelocateProject] 指到它在这台机器上的位置。
//   - 内置 Agent 与 [Open] 时一样以本机应用为准;全局作用域按本机的用户主目录。
//   - 设置换成那份备份里的:GitHub 令牌([Library.SetGitHubToken],那份备份里没有时变成没设置)、AI 设置([Library.SetAISettings])
//     与应用的设置([Library.SetAppSettings])。
//     只属于这台机器的同步设置([Library.SetSyncSettings])、同步状态([Library.SyncStatus])与 WSL 发行版([Library.SetWSLDistro])保留本机的。
func (l *Library) SyncPull(ctx context.Context, backup SyncBackupID) (err error) {
	defer func() { err = typedError(err) }()
	data, err := l.downloadBackup(ctx, "sync pull", backup)
	if err != nil {
		return err
	}
	image, err := store.OpenImage(ctx, data)
	if err != nil {
		if reason, ok := cannotPull(err); ok {
			failure := preconditionError("sync pull", "backup cannot be pulled: "+string(reason), ErrorDetails{})
			failure.Err = err
			return failure
		}
		return err
	}
	defer image.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	home, err := resolveHome(ctx, l.distro)
	if err != nil {
		return fmt.Errorf("sync pull: %w", err)
	}
	l.home = home
	if err := l.store.Replace(ctx, image); err != nil {
		return err
	}
	token, err := l.store.GitHubToken(ctx)
	if err != nil {
		return fmt.Errorf("sync pull: %w", err)
	}
	l.github.SetToken(token)
	return nil
}

func cannotPull(err error) (CannotPullReason, bool) {
	switch {
	case errors.Is(err, store.ErrNewerSchema):
		return CannotPullNewerApp, true
	case errors.Is(err, store.ErrOlderSchema):
		return CannotPullOlderApp, true
	case errors.Is(err, store.ErrUnacceptable):
		return CannotPullNotALibrary, true
	}
	return "", false
}

func (l *Library) snapshot(ctx context.Context) ([]byte, string, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.Snapshot(ctx)
}
