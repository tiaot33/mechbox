package main

import (
	"context"
	"time"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/release"
)

// releaseCheckedEvent 在后台检查完新版本后发出，前端据此重读 Release。
const releaseCheckedEvent = "release-checked"

// releaseCheckRequestedEvent 在点了 macOS 顶部菜单的「检查新版本…」后发出，前端据此检查并显示结果。
const releaseCheckRequestedEvent = "release-check-requested"

// Release 是 GitHub Releases 上比正在运行的版本更新的 Mechbox。
type Release struct {
	Version     string       `json:"version"`
	Notes       string       `json:"notes"` // 更新说明(Markdown)
	PublishedAt time.Time    `json:"published_at,omitzero"`
	PageURL     string       `json:"page_url"` // 这个版本的下载页
	Manual      ManualReason `json:"manual,omitempty"`
}

// ManualReason 说明这次更新为什么不能在 app 里直接装上，只能打开下载页;为空时「更新」下载、校验并装上新版。
type ManualReason string

const (
	ManualPackage  ManualReason = "package"  // 用系统包管理器(deb、rpm、pkg.tar.zst)装的，交给包管理器更新
	ManualLocation ManualReason = "location" // app 不在能直接替换的位置：所在文件夹不能写入(没有权限，或直接从 dmg、下载文件夹里打开),或者和系统临时目录不在同一个磁盘上
	ManualAsset    ManualReason = "asset"    // 这个版本没有本平台能直接装的安装包或它的校验值
)

// ReleaseStatus 是检查新版本的现状。
type ReleaseStatus struct {
	// Checks 为 false 时这个构建不检查新版本：开发版与 server 模式没设 MECHBOX_RELEASE_FEED 时。
	Checks bool `json:"checks"`
	// Latest 是上一次检查找到的新版本;还没检查过、已是最新时为 nil。
	Latest *Release `json:"latest,omitempty"`
	// Failed 是上一次「更新」没装上的版本;上次装上了、或没点过「更新」时为 nil。
	Failed *InstallFailure `json:"failed,omitempty"`
}

// InstallFailure 是上一次「更新」没装上的新版本。新版本在 app 退出后才装上，装没装上要到下次启动才知道:
// 启动时的版本还不是它，就是没装上(Windows 上安装程序被取消或失败，macOS 上替换失败后还原了旧版)。
type InstallFailure struct {
	Version string `json:"version"`
	Reason  string `json:"reason,omitempty"` // 安装程序或替换 app 时记下的最后一行;没有记录时为空
}

// Release 返回检查新版本的现状：这个构建检查不检查新版本，以及上一次检查找到的新版本。
func (a *AppService) Release() ReleaseStatus {
	if a.releases == nil {
		return ReleaseStatus{}
	}
	latest, failed := a.releases.Status()
	return ReleaseStatus{Checks: true, Latest: releaseOf(latest), Failed: failureOf(failed)}
}

// CheckRelease 读 GitHub Releases 上最新的正式版，比正在运行的版本新时返回它，已是最新时返回 nil;结果记下来，Release 随之改变。
// 读的是版本附带的 release.json,不经 GitHub API,不受 API 的限流。连不上或超时时返回 SourceUnreachable,返回的内容读不懂时返回 SourceInvalid;
// 这个构建不检查新版本时返回 FailedPrecondition;正在装新版本时返回 Busy。
func (a *AppService) CheckRelease(ctx context.Context) (*Release, error) {
	latest, err := a.releases.Check(ctx)
	return releaseOf(latest), err
}

// InstallRelease 下载上一次检查找到的新版本的安装包，用 SHA256SUMS.txt 校验，然后装上新版并重启:
// macOS 替换 app,Windows 静默运行安装程序(弹一次 UAC),Linux 替换 AppImage。成功时应用随即退出;
// 下载进度由 Wails 的 wails:updater:download-progress 事件给出。
// 下载失败返回 SourceUnreachable,校验不通过返回 SourceInvalid,当前版本都不变;下载途中取消调用就停下，返回 Canceled。
// app 退出后才装上新版，装没装上下次启动时由 Release 的 Failed 给出;
// version 是用户在对话框里看到、点了「更新」的那个版本;之后的检查找到了别的版本(对话框开着时后台又检查过)时不装，返回 FailedPrecondition。
// 没有新版本或新版本只能从下载页装(Release.Manual 不为空)时也返回 FailedPrecondition。
func (a *AppService) InstallRelease(ctx context.Context, version string) error {
	return a.releases.Install(ctx, version, func() { go a.app.Quit() })
}

func (a *AppService) requestReleaseCheck() {
	a.showWindow()
	go a.app.Event.Emit(releaseCheckRequestedEvent)
}

func releaseOf(r *release.Release) *Release {
	if r == nil {
		return nil
	}
	return &Release{Version: r.Version, Notes: r.Notes, PublishedAt: r.PublishedAt, PageURL: r.PageURL, Manual: manualOf(r.Manual)}
}

func manualOf(reason release.ManualReason) ManualReason {
	switch reason {
	case release.ManualPackage:
		return ManualPackage
	case release.ManualLocation:
		return ManualLocation
	case release.ManualAsset:
		return ManualAsset
	}
	return ""
}

func failureOf(f *release.InstallFailure) *InstallFailure {
	if f == nil {
		return nil
	}
	return &InstallFailure{Version: f.Version, Reason: f.Reason}
}

// releaseChecked 是后台每天检查完一次新版本后做的事：没检查成时记日志，检查成了让前端重读 Release。
func (a *AppService) releaseChecked(err error) {
	if err != nil {
		warn(a.app.Logger, err, "check for new version")
		return
	}
	go a.app.Event.Emit(releaseCheckedEvent)
}
