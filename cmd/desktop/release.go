package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/tiaot33/mechbox/internal/library"
)

// releaseCheckedEvent 在启动时检查完新版本后发出，前端据此重读 Release。
const releaseCheckedEvent = "release-checked"

// releaseFeed 是正式版检查新版本时读的 GitHub Releases 接口;只返回最新的正式版，不含预发布与草稿。
var releaseFeed = "https://api.github.com/repos/tiaot33/mechbox/releases/latest"

// checksumsAsset 是每个版本附带的校验值文件，格式同 sha256sum 的输出。
const checksumsAsset = "SHA256SUMS.txt"

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
	ManualLocation ManualReason = "location" // app 所在的文件夹不能写入：没有权限，或者是直接从 dmg、下载文件夹里打开的
	ManualAsset    ManualReason = "asset"    // 这个版本没有本平台能直接装的安装包或它的校验值
)

// ReleaseStatus 是检查新版本的现状。
type ReleaseStatus struct {
	// Checks 为 false 时这个构建不检查新版本：开发版与 server 模式没设 MECHBOX_RELEASE_FEED 时。
	Checks bool `json:"checks"`
	// Latest 是上一次检查找到的新版本;还没检查过、已是最新时为 nil。
	Latest *Release `json:"latest,omitempty"`
}

type installTarget struct {
	asset  string
	manual ManualReason
}

type releases struct {
	feed    *githubFeed
	updater *updater.Updater
	init    func() error
	apply   func(ctx context.Context, u *updater.Updater, staged string, quit func()) error

	checkMu    sync.Mutex
	mu         sync.Mutex
	latest     *Release
	installing bool
}

func newReleases(feedURL, current string, u *updater.Updater, target func() installTarget) *releases {
	if feedURL == "" || current == "" || u == nil {
		return nil
	}
	feed := &githubFeed{url: feedURL, client: http.DefaultClient, target: target}
	r := &releases{feed: feed, updater: u, apply: installStaged}
	r.init = sync.OnceValue(func() error {
		return u.Init(updater.Config{CurrentVersion: current, Providers: []updater.Provider{feed}, Window: updater.WindowNone})
	})
	return r
}

func (r *releases) status() ReleaseStatus {
	if r == nil {
		return ReleaseStatus{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return ReleaseStatus{Checks: true, Latest: r.latest}
}

func (r *releases) check(ctx context.Context) (*Release, error) {
	if r == nil {
		return nil, &library.Error{Code: library.FailedPrecondition, Message: "this build does not check for new versions"}
	}
	if err := r.init(); err != nil {
		return nil, err
	}
	r.checkMu.Lock()
	defer r.checkMu.Unlock()
	r.mu.Lock()
	installing := r.installing
	r.mu.Unlock()
	if installing {
		return nil, &library.Error{Code: library.Busy, Message: "a new version is being installed"}
	}
	found, err := r.updater.Check(ctx)
	if err != nil {
		if cause := r.feed.takeError(); cause != nil {
			err = cause
		}
		return nil, err
	}
	var latest *Release
	if found != nil {
		release, ok := found.Metadata["release"].(Release)
		if !ok {
			return nil, &library.Error{Code: library.Internal, Message: "release metadata missing"}
		}
		latest = &release
	}
	r.mu.Lock()
	r.latest = latest
	r.mu.Unlock()
	return latest, nil
}

func (r *releases) install(ctx context.Context, quit func()) error {
	if r == nil {
		return &library.Error{Code: library.FailedPrecondition, Message: "this build does not check for new versions"}
	}
	r.mu.Lock()
	latest, installing := r.latest, r.installing
	if latest != nil && latest.Manual == "" && !installing {
		r.installing = true
	}
	r.mu.Unlock()
	switch {
	case installing:
		return &library.Error{Code: library.Busy, Message: "a new version is being installed"}
	case latest == nil:
		return &library.Error{Code: library.FailedPrecondition, Message: "no new version to install"}
	case latest.Manual != "":
		return &library.Error{Code: library.FailedPrecondition, Message: fmt.Sprintf("version %s has to be installed from its download page (%s)", latest.Version, latest.Manual)}
	}
	err := r.downloadAndInstall(ctx, quit)
	if err != nil {
		r.mu.Lock()
		r.installing = false
		r.mu.Unlock()
	}
	return err
}

func (r *releases) downloadAndInstall(ctx context.Context, quit func()) error {
	if err := r.updater.DownloadAndInstall(ctx); err != nil {
		if ctx.Err() != nil {
			return &library.Error{Code: library.Canceled, Message: ctx.Err().Error(), Err: ctx.Err()}
		}
		if libraryErr, ok := errors.AsType[*library.Error](err); ok {
			return libraryErr
		}
		return &library.Error{Code: library.SourceInvalid, Message: err.Error(), Err: err}
	}
	if err := r.apply(ctx, r.updater, r.updater.DownloadedPath(), quit); err != nil {
		return &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
	}
	return nil
}

type githubFeed struct {
	url    string
	client *http.Client
	target func() installTarget

	mu      sync.Mutex
	lastErr error
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (g *githubFeed) Name() string { return "github" }

func (g *githubFeed) takeError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := g.lastErr
	g.lastErr = nil
	return err
}

func (g *githubFeed) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	release, err := g.check(ctx, req.CurrentVersion)
	g.mu.Lock()
	g.lastErr = err
	g.mu.Unlock()
	return release, err
}

func (g *githubFeed) check(ctx context.Context, current string) (*updater.Release, error) {
	var latest githubRelease
	if err := g.getJSON(ctx, g.url, &latest); err != nil {
		return nil, err
	}
	version := strings.TrimPrefix(latest.TagName, "v")
	if !newerVersion(version, current) {
		return nil, nil
	}
	release := Release{Version: version, Notes: latest.Body, PublishedAt: latest.PublishedAt, PageURL: latest.HTMLURL}
	result := &updater.Release{Version: version, Notes: latest.Body, PublishedAt: latest.PublishedAt}
	target := g.target()
	release.Manual = target.manual
	if release.Manual == "" {
		release.Manual = ManualAsset
		for _, asset := range latest.Assets {
			if !strings.HasSuffix(asset.Name, target.asset) {
				continue
			}
			digest, err := g.checksum(ctx, latest, asset.Name)
			if err != nil {
				return nil, err
			}
			if digest != nil {
				release.Manual = ""
				result.Artifact = updater.Artifact{Filename: asset.Name, Size: asset.Size}
				result.Verification = &updater.Verification{DigestAlgo: "sha256", Digest: digest}
				result.Metadata = map[string]any{"url": asset.URL}
			}
			break
		}
	}
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	result.Metadata["release"] = release
	return result, nil
}

func (g *githubFeed) checksum(ctx context.Context, release githubRelease, name string) ([]byte, error) {
	for _, asset := range release.Assets {
		if asset.Name != checksumsAsset {
			continue
		}
		resp, err := g.get(ctx, asset.URL, "")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return parseChecksum(resp.Body, name)
	}
	return nil, nil
}

func parseChecksum(r io.Reader, name string) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		sum, file, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !ok || strings.TrimLeft(strings.TrimSpace(file), "*") != name {
			continue
		}
		digest, err := hex.DecodeString(sum)
		if err != nil || len(digest) != 32 {
			return nil, &library.Error{Code: library.SourceInvalid, Message: fmt.Sprintf("%s: malformed checksum for %s", checksumsAsset, name)}
		}
		return digest, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, &library.Error{Code: library.SourceUnreachable, Message: err.Error(), Err: err}
	}
	return nil, nil
}

func (g *githubFeed) Download(ctx context.Context, release *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	url, _ := release.Metadata["url"].(string)
	if url == "" {
		return &library.Error{Code: library.FailedPrecondition, Message: "no installer to download"}
	}
	resp, err := g.get(ctx, url, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	total := max(resp.ContentLength, release.Artifact.Size)
	var written int64
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
			}
			written += int64(n)
			onProgress(written, total)
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return unreachable(ctx, readErr)
		}
	}
}

func (g *githubFeed) getJSON(ctx context.Context, url string, v any) error {
	resp, err := g.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return &library.Error{Code: library.SourceInvalid, Message: fmt.Sprintf("%s: %v", url, err), Err: err}
	}
	return nil
}

func (g *githubFeed) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
	}
	req.Header.Set("User-Agent", "Mechbox")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, unreachable(ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &library.Error{Code: library.SourceUnreachable, Message: fmt.Sprintf("GET %s: %s", url, resp.Status)}
	}
	return resp, nil
}

func unreachable(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &library.Error{Code: library.Canceled, Message: ctx.Err().Error(), Err: ctx.Err()}
	}
	return &library.Error{Code: library.SourceUnreachable, Message: err.Error(), Err: err}
}

func newerVersion(latest, current string) bool {
	l, lok := parseVersion(latest)
	c, cok := parseVersion(current)
	if !lok || !cok {
		return false
	}
	for i := range 3 {
		if l.numbers[i] != c.numbers[i] {
			return l.numbers[i] > c.numbers[i]
		}
	}
	switch {
	case l.pre == c.pre:
		return false
	case l.pre == "":
		return true
	case c.pre == "":
		return false
	}
	return l.pre > c.pre
}

type version struct {
	numbers [3]int
	pre     string
}

func parseVersion(s string) (version, bool) {
	var v version
	core, pre, _ := strings.Cut(strings.TrimPrefix(s, "v"), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return v, false
		}
		v.numbers[i] = n
	}
	v.pre = pre
	return v, true
}

func writableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".mechbox-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func replaceFile(staged, target string) error {
	src, err := os.Open(staged)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".mechbox-update-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, src)
	err = errors.Join(err, tmp.Chmod(0o755), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func installerScript(pid int, setup, staging, dir, exe string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return strings.Join([]string{
		"$ErrorActionPreference = 'SilentlyContinue'",
		fmt.Sprintf("Wait-Process -Id %d -Timeout 60", pid),
		fmt.Sprintf("try { Start-Process -FilePath %s -ArgumentList %s -Verb RunAs -Wait } catch {}", quote(setup), quote("/S /D="+dir)),
		fmt.Sprintf("Remove-Item -LiteralPath %s -Recurse -Force", quote(staging)),
		fmt.Sprintf("Start-Process -FilePath %s", quote(exe)),
	}, "\n")
}

func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		raw[2*i], raw[2*i+1] = byte(u), byte(u>>8)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// Release 返回检查新版本的现状：这个构建检查不检查新版本，以及上一次检查找到的新版本。
func (a *AppService) Release() ReleaseStatus {
	return a.releases.status()
}

// CheckRelease 读 GitHub Releases 上最新的正式版，比正在运行的版本新时返回它，已是最新时返回 nil;结果记下来，Release 随之改变。
// 连不上或被限流时返回 SourceUnreachable;这个构建不检查新版本时返回 FailedPrecondition;正在装新版本时返回 Busy。
func (a *AppService) CheckRelease(ctx context.Context) (*Release, error) {
	return a.releases.check(ctx)
}

// InstallRelease 下载上一次检查找到的新版本的安装包，用 SHA256SUMS.txt 校验，然后装上新版并重启:
// macOS 替换 app,Windows 静默运行安装程序(弹一次 UAC),Linux 替换 AppImage。成功时应用随即退出;
// 下载进度由 Wails 的 wails:updater:download-progress 事件给出。
// 下载失败返回 SourceUnreachable,校验不通过返回 SourceInvalid,当前版本都不变;
// 没有新版本或新版本只能从下载页装(Release.Manual 不为空)时返回 FailedPrecondition。
func (a *AppService) InstallRelease(ctx context.Context) error {
	return a.releases.install(ctx, func() { go a.app.Quit() })
}

func (a *AppService) checkReleaseOnStartup(ctx context.Context) {
	if _, err := a.releases.check(ctx); err != nil {
		if ctx.Err() == nil {
			a.warn(err, "check for new version")
		}
		return
	}
	if ctx.Err() == nil && a.app != nil {
		a.app.Event.Emit(releaseCheckedEvent)
	}
}
