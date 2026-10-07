package main

import (
	"context"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 本文件把 library.Library 的方法(Close 除外)逐个开放给前端：同名、同签名，原样转发。
// TestLibraryServiceExposesEveryLibraryMethod 保证 Library 新增方法时这里不会漏。

// settle 处理前端已取消的调用：前端 promise.cancel() 后 ctx 被取消，此时方法若返回错误,
// Wails 运行时会把这个"取消之后的拒绝"报成未处理的 rejection;返回正常结果则会被运行时丢弃。
// 所以调用已取消时吞掉错误、返回零值——前端早已不要这次结果。用法：return settle(s.lib.X(ctx))(ctx)。
func settle[T any](v T, err error) func(context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		if err != nil && ctx.Err() != nil {
			var zero T
			return zero, nil
		}
		return v, err
	}
}

// settleErr 是只返回 error 的方法对应的 settle。
func settleErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// ---------- 进库 ----------

func (s *LibraryService) SearchSkills(ctx context.Context, query string) ([]library.SearchSkill, error) {
	return settle(s.lib.SearchSkills(ctx, query))(ctx)
}

func (s *LibraryService) PreviewFetch(ctx context.Context, req library.PreviewFetchRequest) (library.FetchPreview, error) {
	return settle(s.lib.PreviewFetch(ctx, req))(ctx)
}

func (s *LibraryService) GetSkillAudit(ctx context.Context, id library.FetchPreviewID, name string) (*library.SkillAudit, error) {
	return settle(s.lib.GetSkillAudit(ctx, id, name))(ctx)
}

func (s *LibraryService) Fetch(ctx context.Context, id library.FetchPreviewID, names []string) ([]library.SkillSummary, error) {
	return settle(s.lib.Fetch(ctx, id, names))(ctx)
}

// ---------- 库里的技能 ----------

func (s *LibraryService) ListSkills(ctx context.Context) ([]library.SkillSummary, error) {
	return settle(s.lib.ListSkills(ctx))(ctx)
}

func (s *LibraryService) GetSkill(ctx context.Context, id library.SkillID) (library.Skill, error) {
	return settle(s.lib.GetSkill(ctx, id))(ctx)
}

func (s *LibraryService) ListSkillFiles(ctx context.Context, id library.SkillID) ([]library.FileMetadata, error) {
	return settle(s.lib.ListSkillFiles(ctx, id))(ctx)
}

func (s *LibraryService) GetSkillFile(ctx context.Context, id library.SkillID, path string) (library.File, error) {
	return settle(s.lib.GetSkillFile(ctx, id, path))(ctx)
}

func (s *LibraryService) RemoveSkill(ctx context.Context, id library.SkillID, opts library.RemoveSkillOptions) error {
	return settleErr(ctx, s.lib.RemoveSkill(ctx, id, opts))
}

// ---------- 更新 ----------

func (s *LibraryService) CheckUpdates(ctx context.Context, ids []library.SkillID) ([]library.UpdateCheck, error) {
	return settle(s.lib.CheckUpdates(ctx, ids))(ctx)
}

func (s *LibraryService) Update(ctx context.Context, id library.SkillID) (library.UpdateResult, error) {
	return settle(s.lib.Update(ctx, id))(ctx)
}

// ---------- 版本历史 ----------

func (s *LibraryService) ListVersions(ctx context.Context, id library.SkillID) ([]library.SkillVersion, error) {
	return settle(s.lib.ListVersions(ctx, id))(ctx)
}

func (s *LibraryService) GetVersion(ctx context.Context, id library.VersionID) (library.SkillVersion, error) {
	return settle(s.lib.GetVersion(ctx, id))(ctx)
}

func (s *LibraryService) DiffVersions(ctx context.Context, oldID, newID library.VersionID) (library.VersionDiff, error) {
	return settle(s.lib.DiffVersions(ctx, oldID, newID))(ctx)
}

// ---------- 整理:集合与分组 ----------

func (s *LibraryService) ListCollections(ctx context.Context) ([]library.Collection, error) {
	return settle(s.lib.ListCollections(ctx))(ctx)
}

func (s *LibraryService) CreateGroup(ctx context.Context, name string) (library.Group, error) {
	return settle(s.lib.CreateGroup(ctx, name))(ctx)
}

func (s *LibraryService) ListGroups(ctx context.Context) ([]library.Group, error) {
	return settle(s.lib.ListGroups(ctx))(ctx)
}

func (s *LibraryService) RenameGroup(ctx context.Context, id library.GroupID, name string) (library.Group, error) {
	return settle(s.lib.RenameGroup(ctx, id, name))(ctx)
}

func (s *LibraryService) DeleteGroup(ctx context.Context, id library.GroupID) error {
	return settleErr(ctx, s.lib.DeleteGroup(ctx, id))
}

func (s *LibraryService) AddSkillToGroup(ctx context.Context, groupID library.GroupID, skillID library.SkillID) ([]library.GroupMemberResult, error) {
	return settle(s.lib.AddSkillToGroup(ctx, groupID, skillID))(ctx)
}

func (s *LibraryService) RemoveSkillFromGroup(ctx context.Context, groupID library.GroupID, skillID library.SkillID, opts library.RemoveFromGroupOptions) error {
	return settleErr(ctx, s.lib.RemoveSkillFromGroup(ctx, groupID, skillID, opts))
}

func (s *LibraryService) ListGroupSkills(ctx context.Context, groupID library.GroupID) ([]library.SkillSummary, error) {
	return settle(s.lib.ListGroupSkills(ctx, groupID))(ctx)
}

func (s *LibraryService) ListGroupsWithMembers(ctx context.Context) ([]library.GroupWithMembers, error) {
	return settle(s.lib.ListGroupsWithMembers(ctx))(ctx)
}

// ---------- 项目与 Scope ----------

func (s *LibraryService) ListAgents(ctx context.Context) ([]library.Agent, error) {
	return settle(s.lib.ListAgents(ctx))(ctx)
}

func (s *LibraryService) RegisterProject(ctx context.Context, path string) (library.RegisteredProject, error) {
	return settle(s.lib.RegisterProject(ctx, path))(ctx)
}

func (s *LibraryService) ListRegisteredProjects(ctx context.Context) ([]library.RegisteredProject, error) {
	return settle(s.lib.ListRegisteredProjects(ctx))(ctx)
}

func (s *LibraryService) UnregisterProject(ctx context.Context, id library.ProjectID, opts library.UnregisterProjectOptions) error {
	return settleErr(ctx, s.lib.UnregisterProject(ctx, id, opts))
}

func (s *LibraryService) Resolve(ctx context.Context, scope library.Scope) (string, error) {
	return settle(s.lib.Resolve(ctx, scope))(ctx)
}

// ---------- 安装、卸载与重新安装 ----------

func (s *LibraryService) PlanInstall(ctx context.Context, id library.SkillID, scope library.Scope) (library.InstallPlan, error) {
	return settle(s.lib.PlanInstall(ctx, id, scope))(ctx)
}

func (s *LibraryService) Install(ctx context.Context, id library.SkillID, scope library.Scope, opts library.InstallOptions) error {
	return settleErr(ctx, s.lib.Install(ctx, id, scope, opts))
}

func (s *LibraryService) Uninstall(ctx context.Context, id library.SkillID, scope library.Scope) error {
	return settleErr(ctx, s.lib.Uninstall(ctx, id, scope))
}

func (s *LibraryService) PlanGroupInstall(ctx context.Context, groupID library.GroupID, scope library.Scope) (library.GroupInstallPlan, error) {
	return settle(s.lib.PlanGroupInstall(ctx, groupID, scope))(ctx)
}

func (s *LibraryService) InstallGroup(ctx context.Context, groupID library.GroupID, scope library.Scope, opts library.InstallOptions) ([]library.GroupMemberResult, error) {
	return settle(s.lib.InstallGroup(ctx, groupID, scope, opts))(ctx)
}

func (s *LibraryService) UninstallGroup(ctx context.Context, groupID library.GroupID, scope library.Scope) error {
	return settleErr(ctx, s.lib.UninstallGroup(ctx, groupID, scope))
}

func (s *LibraryService) ListGroupInstalls(ctx context.Context) ([]library.GroupInstall, error) {
	return settle(s.lib.ListGroupInstalls(ctx))(ctx)
}

func (s *LibraryService) ListSkillScopes(ctx context.Context) ([]library.SkillScope, error) {
	return settle(s.lib.ListSkillScopes(ctx))(ctx)
}

func (s *LibraryService) InspectSkillScopes(ctx context.Context, filter library.SkillScopeFilter) ([]library.SkillScopeInspection, error) {
	return settle(s.lib.InspectSkillScopes(ctx, filter))(ctx)
}

func (s *LibraryService) Reinstall(ctx context.Context, filter library.SkillScopeFilter, opts library.ReinstallOptions) ([]library.ReinstallResult, error) {
	return settle(s.lib.Reinstall(ctx, filter, opts))(ctx)
}

// ---------- 同步 ----------

func (s *LibraryService) PlanSyncPush(ctx context.Context) (library.SyncPlan, error) {
	return settle(s.lib.PlanSyncPush(ctx))(ctx)
}

func (s *LibraryService) PlanSyncPull(ctx context.Context) (library.SyncPlan, error) {
	return settle(s.lib.PlanSyncPull(ctx))(ctx)
}

func (s *LibraryService) SyncPush(ctx context.Context, expected library.RemoteIdentity) (library.SyncResult, error) {
	return settle(s.lib.SyncPush(ctx, expected))(ctx)
}

func (s *LibraryService) SyncPull(ctx context.Context, expected library.RemoteIdentity) (library.SyncResult, error) {
	return settle(s.lib.SyncPull(ctx, expected))(ctx)
}
