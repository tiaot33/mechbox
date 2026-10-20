package main

import (
	"context"

	"github.com/tiaot33/mechbox/internal/library"
)

// 本文件把 library.Library 的方法(Close 除外)逐个开放给前端：同名、同签名，原样转发。
// FindUnmanagedSources 例外：它和自动检查更新都要读来源，轮流进行，不同时跑，做完把空闲内存还给系统。
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

func (s *LibraryService) ListSourceRefs(ctx context.Context, input string) (library.SourceRefs, error) {
	return settle(s.lib.ListSourceRefs(ctx, input))(ctx)
}

func (s *LibraryService) GetCandidateAudit(ctx context.Context, id library.FetchPreviewID, name string) (*library.SkillAudit, error) {
	return settle(s.lib.GetCandidateAudit(ctx, id, name))(ctx)
}

func (s *LibraryService) Fetch(ctx context.Context, id library.FetchPreviewID, names []string) ([]library.Skill, error) {
	return settle(s.lib.Fetch(ctx, id, names))(ctx)
}

// ---------- 库里的技能 ----------

func (s *LibraryService) ListSkills(ctx context.Context) ([]library.Skill, error) {
	return settle(s.lib.ListSkills(ctx))(ctx)
}

func (s *LibraryService) GetSkill(ctx context.Context, id library.SkillID) (library.Skill, error) {
	return settle(s.lib.GetSkill(ctx, id))(ctx)
}

func (s *LibraryService) GetSkillAudit(ctx context.Context, id library.SkillID) (*library.SkillAudit, error) {
	return settle(s.lib.GetSkillAudit(ctx, id))(ctx)
}

func (s *LibraryService) RemoveSkill(ctx context.Context, id library.SkillID, opts library.RemoveSkillOptions) error {
	return settleErr(ctx, s.lib.RemoveSkill(ctx, id, opts))
}

// ---------- 更新 ----------

func (s *LibraryService) CheckUpdates(ctx context.Context, collections []library.CollectionID) ([]library.CollectionUpdateCheck, error) {
	return settle(s.lib.CheckUpdates(ctx, collections))(ctx)
}

func (s *LibraryService) PlanUpdate(ctx context.Context, id library.CollectionID) (library.UpdatePlan, error) {
	return settle(s.lib.PlanUpdate(ctx, id))(ctx)
}

func (s *LibraryService) PlanUpdateFromScope(ctx context.Context, skill library.SkillID, scope library.Scope) (library.UpdatePlan, error) {
	return settle(s.lib.PlanUpdateFromScope(ctx, skill, scope))(ctx)
}

func (s *LibraryService) GetUpdatePlanFile(ctx context.Context, plan library.UpdatePlanID, skill library.SkillID, path string) (library.File, error) {
	return settle(s.lib.GetUpdatePlanFile(ctx, plan, skill, path))(ctx)
}

func (s *LibraryService) SkipUpdate(ctx context.Context, req library.SkipRequest) error {
	return settleErr(ctx, s.lib.SkipUpdate(ctx, req))
}

func (s *LibraryService) RejectRename(ctx context.Context, plan library.UpdatePlanID, skill library.SkillID) error {
	return settleErr(ctx, s.lib.RejectRename(ctx, plan, skill))
}

func (s *LibraryService) KeepDeletedSkill(ctx context.Context, id library.SkillID) (library.Skill, error) {
	return settle(s.lib.KeepDeletedSkill(ctx, id))(ctx)
}

func (s *LibraryService) Update(ctx context.Context, req library.UpdateRequest) (library.UpdateResult, error) {
	return settle(s.lib.Update(ctx, req))(ctx)
}

func (s *LibraryService) RetryUpdateInstalls(ctx context.Context, id library.UpdateRecordID, installs []library.ReinstallRequest) ([]library.ReinstallResult, error) {
	return settle(s.lib.RetryUpdateInstalls(ctx, id, installs))(ctx)
}

func (s *LibraryService) ListUpdateHistory(ctx context.Context, filter library.UpdateHistoryFilter) ([]library.UpdateRecord, error) {
	return settle(s.lib.ListUpdateHistory(ctx, filter))(ctx)
}

func (s *LibraryService) Rollback(ctx context.Context, id library.UpdateRecordID) (library.RollbackResult, error) {
	return settle(s.lib.Rollback(ctx, id))(ctx)
}

// ---------- 版本历史 ----------

func (s *LibraryService) ListVersions(ctx context.Context, id library.SkillID) ([]library.SkillVersion, error) {
	return settle(s.lib.ListVersions(ctx, id))(ctx)
}

func (s *LibraryService) GetVersion(ctx context.Context, id library.VersionID) (library.SkillVersion, error) {
	return settle(s.lib.GetVersion(ctx, id))(ctx)
}

func (s *LibraryService) ListVersionFiles(ctx context.Context, id library.VersionID) ([]library.FileMetadata, error) {
	return settle(s.lib.ListVersionFiles(ctx, id))(ctx)
}

func (s *LibraryService) GetVersionFile(ctx context.Context, id library.VersionID, path string) (library.File, error) {
	return settle(s.lib.GetVersionFile(ctx, id, path))(ctx)
}

func (s *LibraryService) DiffVersions(ctx context.Context, from, to library.VersionID) (library.VersionDiff, error) {
	return settle(s.lib.DiffVersions(ctx, from, to))(ctx)
}

// ---------- 整理:集合与分组 ----------

func (s *LibraryService) MarkNotInterested(ctx context.Context, id library.CollectionID, names []string) error {
	return settleErr(ctx, s.lib.MarkNotInterested(ctx, id, names))
}

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

func (s *LibraryService) DeleteGroup(ctx context.Context, id library.GroupID, opts library.DeleteGroupOptions) error {
	return settleErr(ctx, s.lib.DeleteGroup(ctx, id, opts))
}

func (s *LibraryService) AddSkillToGroup(ctx context.Context, groupID library.GroupID, skillID library.SkillID) ([]library.GroupMemberResult, error) {
	return settle(s.lib.AddSkillToGroup(ctx, groupID, skillID))(ctx)
}

func (s *LibraryService) RemoveSkillFromGroup(ctx context.Context, groupID library.GroupID, skillID library.SkillID, opts library.RemoveFromGroupOptions) error {
	return settleErr(ctx, s.lib.RemoveSkillFromGroup(ctx, groupID, skillID, opts))
}

// ---------- 项目与 Scope ----------

func (s *LibraryService) ListAgents(ctx context.Context) ([]library.Agent, error) {
	return settle(s.lib.ListAgents(ctx))(ctx)
}

func (s *LibraryService) AddAgent(ctx context.Context, agent library.CustomAgent) (library.Agent, error) {
	return settle(s.lib.AddAgent(ctx, agent))(ctx)
}

func (s *LibraryService) EditAgent(ctx context.Context, code library.AgentCode, agent library.CustomAgent, opts library.EditAgentOptions) (library.EditAgentResult, error) {
	return settle(s.lib.EditAgent(ctx, code, agent, opts))(ctx)
}

func (s *LibraryService) DeleteAgent(ctx context.Context, code library.AgentCode, opts library.DeleteAgentOptions) (library.DeleteAgentResult, error) {
	return settle(s.lib.DeleteAgent(ctx, code, opts))(ctx)
}

func (s *LibraryService) RegisterProject(ctx context.Context, path string) (library.RegisteredProject, error) {
	return settle(s.lib.RegisterProject(ctx, path))(ctx)
}

func (s *LibraryService) ListRegisteredProjects(ctx context.Context) ([]library.RegisteredProject, error) {
	return settle(s.lib.ListRegisteredProjects(ctx))(ctx)
}

func (s *LibraryService) RelocateProject(ctx context.Context, id library.ProjectID, path string) (library.RegisteredProject, error) {
	return settle(s.lib.RelocateProject(ctx, id, path))(ctx)
}

func (s *LibraryService) UnregisterProject(ctx context.Context, id library.ProjectID, opts library.UnregisterProjectOptions) error {
	return settleErr(ctx, s.lib.UnregisterProject(ctx, id, opts))
}

func (s *LibraryService) ScopeDirectory(ctx context.Context, scope library.Scope) (string, error) {
	return settle(s.lib.ScopeDirectory(ctx, scope))(ctx)
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

func (s *LibraryService) ListSkillScopes(ctx context.Context, filter library.SkillScopeFilter) ([]library.SkillScope, error) {
	return settle(s.lib.ListSkillScopes(ctx, filter))(ctx)
}

func (s *LibraryService) InspectSkillScopes(ctx context.Context, filter library.SkillScopeFilter) ([]library.SkillScopeInspection, error) {
	return settle(s.lib.InspectSkillScopes(ctx, filter))(ctx)
}

func (s *LibraryService) ListUnmanagedSkills(ctx context.Context) ([]library.UnmanagedSkill, error) {
	return settle(s.lib.ListUnmanagedSkills(ctx))(ctx)
}

func (s *LibraryService) FetchUnmanaged(ctx context.Context, req library.FetchUnmanagedRequest) (library.FetchUnmanagedResult, error) {
	return settle(s.lib.FetchUnmanaged(ctx, req))(ctx)
}

func (s *LibraryService) PlanFetchUnmanaged(ctx context.Context, req library.PlanFetchUnmanagedRequest) ([]library.PlannedUnmanagedSkill, error) {
	return settle(s.lib.PlanFetchUnmanaged(ctx, req))(ctx)
}

func (s *LibraryService) FindUnmanagedSources(ctx context.Context, req library.FindUnmanagedSourcesRequest) (found []library.FoundUnmanagedSource, err error) {
	s.inBackground(ctx, func() { found, err = s.lib.FindUnmanagedSources(ctx, req) })
	return settle(found, err)(ctx)
}

func (s *LibraryService) FetchUnmanagedSkills(ctx context.Context, req library.FetchUnmanagedSkillsRequest) ([]library.FetchedUnmanagedSkill, error) {
	return settle(s.lib.FetchUnmanagedSkills(ctx, req))(ctx)
}

func (s *LibraryService) ListSameNameSkills(ctx context.Context) ([]library.SameNameSkill, error) {
	return settle(s.lib.ListSameNameSkills(ctx))(ctx)
}

func (s *LibraryService) Reinstall(ctx context.Context, requests []library.ReinstallRequest) ([]library.ReinstallResult, error) {
	return settle(s.lib.Reinstall(ctx, requests))(ctx)
}

// ---------- GitHub 与同步 ----------

func (s *LibraryService) SetGitHubToken(token string) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.token = token != ""
	s.lib.SetGitHubToken(token)
}

func (s *LibraryService) SetSyncSettings(settings *library.SyncSettings) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.sync = nil
	if settings != nil {
		given := *settings
		s.sync = &given
	}
	s.lib.SetSyncSettings(settings)
}

func (s *LibraryService) PlanSyncPush(ctx context.Context) (library.SyncPushPlan, error) {
	return settle(s.lib.PlanSyncPush(ctx))(ctx)
}

func (s *LibraryService) PlanSyncPull(ctx context.Context) (library.SyncPullPlan, error) {
	return settle(s.lib.PlanSyncPull(ctx))(ctx)
}

func (s *LibraryService) SyncPush(ctx context.Context, expected library.RemoteIdentity) (library.RemoteLibrary, error) {
	return settle(s.lib.SyncPush(ctx, expected))(ctx)
}

func (s *LibraryService) SyncPull(ctx context.Context, expected library.RemoteIdentity) error {
	return settleErr(ctx, s.lib.SyncPull(ctx, expected))
}
