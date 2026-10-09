package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/s3"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// RemoteIdentity 是同步计划里远端那份库的标识:原样传回执行方法,界面不展示;空表示计划时远端没有库。
// 执行时远端已不是这一份,返回 Conflict,确保推送覆盖的、拉回恢复的就是用户在计划里确认的那一份。
type RemoteIdentity string

// RemoteLibrary 是 S3 上的那份库:界面据此写「S3 上的库:MacBook-Pro 于 3 天前推送」,
// 让用户认出推送会覆盖、拉回会恢复的是哪一份。
type RemoteLibrary struct {
	// Identity 原样传回 [Library.SyncPush] / [Library.SyncPull]。
	Identity RemoteIdentity `json:"identity"`
	// PushedAt 是这份库被推送的时间;S3 没给出时为零值。
	PushedAt time.Time `json:"pushed_at,omitzero"`
	// PushedFrom 是推送它的机器名(推送时那台机器的主机名);没有记录时为空。
	PushedFrom string `json:"pushed_from,omitempty"`
}

// SyncPushPlan 是推送的同步计划:推送会覆盖 S3 上的哪一份库。只读,不上传。
type SyncPushPlan struct {
	// Remote 是 S3 上现在的库;nil 表示 S3 上还没有库(第一次推送)。
	Remote *RemoteLibrary `json:"remote,omitempty"`
}

// CannotPullReason 是拉回的同步计划里不能拉回的原因,界面按它说明下一步该做什么。
type CannotPullReason string

const (
	// CannotPullNoRemote S3 上没有库:还没有哪台机器推送过。
	CannotPullNoRemote CannotPullReason = "no_remote"
	// CannotPullNotALibrary S3 上的文件不是本应用的库,或已损坏。
	CannotPullNotALibrary CannotPullReason = "not_a_library"
	// CannotPullNewerApp S3 上的库来自较新版本的应用:先把这台机器上的应用升级。
	CannotPullNewerApp CannotPullReason = "newer_app"
	// CannotPullOlderApp S3 上的库来自较旧版本的应用:先在推送它的机器上升级应用,再推送一次。
	CannotPullOlderApp CannotPullReason = "older_app"
)

// SyncPullPlan 是拉回的同步计划:会从 S3 上的哪一份库恢复、里面有什么、能不能拉回。
// 只读,不替换本机的库。拉回会替换本机的整个库,Agent 目录里的文件夹不会改动(见 [Library.SyncPull])。
type SyncPullPlan struct {
	// Remote 是 S3 上现在的库;nil 表示 S3 上没有库,此时 CannotPull 为 CannotPullNoRemote。
	Remote *RemoteLibrary `json:"remote,omitempty"`
	// CanPull 为 false 时 CannotPull 给出原因。S3 上的库不能拉回时 [Library.SyncPull] 返回 FailedPrecondition;
	// S3 上没有库(CannotPullNoRemote)时带着空标识拉回返回 NotFound。
	CanPull    bool             `json:"can_pull"`
	CannotPull CannotPullReason `json:"cannot_pull,omitempty"`
	// Skills、Groups 是那份库里的技能数与组数;仅 CanPull 时填写。
	Skills int `json:"skills"`
	Groups int `json:"groups"`
	// Projects 是那份库里的已登记项目,仅 CanPull 时填写。Path 是推送那台机器上的路径,Missing 按这台机器现查:
	// 拉回后项目 ID 不变,Missing 的项目要用 [Library.RelocateProject] 指到它在这台机器上的位置。
	Projects []RegisteredProject `json:"projects,omitempty"`
}

// PlanSyncPush 生成推送的同步计划:S3 上现在是哪一份库,推送会用本机的库覆盖它。只查询,不上传。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
func (l *Library) PlanSyncPush(ctx context.Context) (plan SyncPushPlan, err error) {
	defer func() { err = typedError(err) }()
	object, err := s3.Head(ctx)
	if errors.Is(err, s3.ErrNotFound) {
		return SyncPushPlan{}, nil
	}
	if err != nil {
		return SyncPushPlan{}, err
	}
	return SyncPushPlan{Remote: remoteLibrary(object)}, nil
}

// PlanSyncPull 生成拉回的同步计划:下载 S3 上的库并校验,报告它是哪一份、里面有什么、能不能拉回,
// 不替换本机的库。S3 上没有库或那份库不能拉回时正常返回计划(CanPull 为 false),不返回错误。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
func (l *Library) PlanSyncPull(ctx context.Context) (plan SyncPullPlan, err error) {
	defer func() { err = typedError(err) }()
	data, object, err := s3.Get(ctx)
	if errors.Is(err, s3.ErrNotFound) {
		return SyncPullPlan{CannotPull: CannotPullNoRemote}, nil
	}
	if err != nil {
		return SyncPullPlan{}, err
	}
	plan = SyncPullPlan{Remote: remoteLibrary(object)}
	image, err := store.OpenImage(ctx, data)
	if err != nil {
		reason, ok := cannotPull(err)
		if !ok {
			return SyncPullPlan{}, err
		}
		plan.CannotPull = reason
		return plan, nil
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
	plan.CanPull = true
	plan.Skills, plan.Groups, plan.Projects = summary.Skills, summary.Groups, projects
	return plan, nil
}

// SyncPush 用本机的库覆盖 S3 上的库,推送的是调用时本机库的完整内容;expected 传推送的同步计划里的
// Remote.Identity,S3 上没有库时传空。S3 上的库自计划后被别处创建、改写或删除都返回 Conflict,S3 不被改动,
// 界面重新生成同步计划。成功时返回刚推送的那份库,同步页可直接显示,不必再生成计划。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
func (l *Library) SyncPush(ctx context.Context, expected RemoteIdentity) (remote RemoteLibrary, err error) {
	defer func() { err = typedError(err) }()
	object, err := s3.Head(ctx)
	switch {
	case errors.Is(err, s3.ErrNotFound):
		object = s3.Object{}
	case err != nil:
		return RemoteLibrary{}, err
	}
	if RemoteIdentity(object.ETag) != expected {
		return RemoteLibrary{}, &Error{Code: Conflict, Message: "sync push: remote library changed since sync plan"}
	}
	data, err := l.snapshotBytes(ctx)
	if err != nil {
		return RemoteLibrary{}, err
	}
	object, err = s3.Put(ctx, data, string(expected), machineName())
	if err != nil {
		return RemoteLibrary{}, err
	}
	return *remoteLibrary(object), nil
}

// SyncPull 用 S3 上的库替换本机的整个库;expected 传拉回的同步计划里的 Remote.Identity。
// S3 上的库自计划后被改写或删除返回 Conflict;S3 上有库但不能拉回(计划里 CannotPull 为 CannotPullNotALibrary、
// CannotPullNewerApp 或 CannotPullOlderApp)返回 FailedPrecondition;计划时就没有库(CannotPullNoRemote)、
// 带着空标识拉回而 S3 上仍没有库返回 NotFound。这些情况下本机的库都不变。
// S3 未配置返回 NotConfigured,凭证被拒返回 Permission,连不上返回 SourceUnreachable。
//
// 拉回后:
//   - 技能、版本历史、组、安装记录、已登记项目都换成 S3 上那份;本机原有、那份库里没有的全部消失。
//   - Agent 目录里的文件夹一个都不动,Agent 用的内容在用户重新安装之前不会变。本机原来装出去、
//     那份库里没有记录的文件夹留在原处,库不再管;那份库记录的安装在本机可能还没有文件夹或内容不同,
//     用 [Library.InspectSkillScopes] 查看、[Library.Reinstall] 装回。
//   - 已登记项目的路径是推送那台机器上的;在本机不存在的项目([RegisteredProject].Missing)用
//     [Library.RelocateProject] 指到它在这台机器上的位置。
//   - 内置 Agent 与 [Open] 时一样以本机应用为准;全局作用域按本机的用户主目录。
func (l *Library) SyncPull(ctx context.Context, expected RemoteIdentity) (err error) {
	defer func() { err = typedError(err) }()
	data, object, err := s3.Get(ctx)
	if errors.Is(err, s3.ErrNotFound) && expected != "" {
		return &Error{Code: Conflict, Message: "sync pull: remote library deleted since sync plan"}
	}
	if err != nil {
		return err
	}
	if RemoteIdentity(object.ETag) != expected {
		return &Error{Code: Conflict, Message: "sync pull: remote library changed since sync plan"}
	}
	image, err := store.OpenImage(ctx, data)
	if err != nil {
		if reason, ok := cannotPull(err); ok {
			failure := preconditionError("sync pull", "remote library cannot be pulled: "+string(reason), ErrorDetails{})
			failure.Err = err
			return failure
		}
		return err
	}
	defer image.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("sync pull: find home directory: %w", err)
	}
	l.home = home
	return l.store.Replace(ctx, image)
}

func remoteLibrary(object s3.Object) *RemoteLibrary {
	return &RemoteLibrary{Identity: RemoteIdentity(object.ETag), PushedAt: object.LastModified, PushedFrom: object.PushedFrom}
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

func machineName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(name, ".local")
}

func (l *Library) snapshotBytes(ctx context.Context) ([]byte, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.Snapshot(ctx)
}
