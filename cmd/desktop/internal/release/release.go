package release

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
	"net/url"
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

// releaseManifest 是每个版本附带的发布信息，内容是 GitHub releases 接口返回的一部分(tag_name、body、html_url、published_at、assets);
// 由发版工作流在发布后上传。
const releaseManifest = "release.json"

// releaseFeed 是正式版检查新版本时读的地址：最新正式版(不含预发布与草稿)附带的 release.json。
// 它和下载安装包一样走 github.com 的下载地址，不占 GitHub API 每小时 60 次的匿名额度，也不需要令牌。
var releaseFeed = "https://github.com/tiaot33/mechbox/releases/latest/download/" + releaseManifest

// releaseCheckInterval 是运行期间检查新版本的间隔;关掉窗口留在菜单栏时 app 可能很久不退出。没检查成时隔 releaseRetry 再试。
const (
	releaseCheckInterval = 24 * time.Hour
	releaseRetry         = time.Hour
)

// checksumsAsset 是每个版本附带的校验值文件，格式同 sha256sum 的输出。
const checksumsAsset = "SHA256SUMS.txt"

// generatedNotes 是 GitHub 自动生成的改动列表的标题;发布正文里它前面是手动安装的说明，更新说明从这里开始。
const generatedNotes = "## What's Changed"

// pendingFile 记下点了「更新」、app 退出后才装的那个版本，放在数据库所在的目录;下次启动时据此知道装没装上。
const pendingFile = "update-pending.json"

// maxManifestSize 是 release.json 最多读多少字节;正常的只有几 KB,超出时按格式不对处理。
const maxManifestSize = 1 << 20

// checkTimeout 是检查一次新版本最多等多久。
const checkTimeout = 30 * time.Second

type Release struct {
	Version     string
	Notes       string
	PublishedAt time.Time
	PageURL     string
	Manual      ManualReason
}

type ManualReason string

const (
	ManualPackage  ManualReason = "package"
	ManualLocation ManualReason = "location"
	ManualAsset    ManualReason = "asset"
)

type InstallFailure struct {
	Version string
	Reason  string
}

type pendingInstall struct {
	Version string `json:"version"`
	Log     string `json:"log,omitempty"`
}

type installTarget struct {
	asset  string
	manual ManualReason
}

type Releases struct {
	feed     *githubFeed
	updater  *updater.Updater
	stateDir string
	init     func() error
	apply    func(ctx context.Context, u *updater.Updater, staged, log string, quit func()) error

	checkMu    sync.Mutex
	mu         sync.Mutex
	latest     *Release
	failed     *InstallFailure
	installing bool
}

// mirror 为 nil 时检查和下载都直连;否则新版本信息与安装包经它包过的 transport 发出(GitHub 镜像加速),
// 校验值仍直连这个版本在 GitHub 上的固定地址(见 checksumURL)。
func New(current, stateDir string, u *updater.Updater, proxyFunc func(*http.Request) (*url.URL, error), mirror func(http.RoundTripper) http.RoundTripper) *Releases {
	return newReleases(feedURL(), current, stateDir, u, releaseTarget, proxyFunc, mirror)
}

func newReleases(feedURL, current, stateDir string, u *updater.Updater, target func() installTarget, proxyFunc func(*http.Request) (*url.URL, error), mirror func(http.RoundTripper) http.RoundTripper) *Releases {
	if feedURL == "" || current == "" || u == nil {
		return nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = checkTimeout
	if proxyFunc != nil {
		transport.Proxy = proxyFunc
	}
	direct := &http.Client{Transport: transport}
	feed := &githubFeed{url: feedURL, client: direct, direct: direct, target: target}
	if mirror != nil {
		feed.client = &http.Client{Transport: mirror(transport)}
	}
	r := &Releases{feed: feed, updater: u, stateDir: stateDir, apply: installStaged, failed: takePending(stateDir, current)}
	r.init = sync.OnceValue(func() error {
		return u.Init(updater.Config{CurrentVersion: current, Providers: []updater.Provider{feed}, Window: updater.WindowNone})
	})
	return r
}

func (r *Releases) Status() (latest *Release, failed *InstallFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latest, r.failed
}

func takePending(stateDir, current string) *InstallFailure {
	path := filepath.Join(stateDir, pendingFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	_ = os.Remove(path)
	var pending pendingInstall
	if json.Unmarshal(data, &pending) != nil || !newerVersion(pending.Version, current) {
		return nil
	}
	failure := &InstallFailure{Version: pending.Version}
	if pending.Log != "" {
		if log, err := os.ReadFile(pending.Log); err == nil {
			lines := strings.Split(strings.TrimSpace(string(log)), "\n")
			failure.Reason = strings.TrimSpace(lines[len(lines)-1])
		}
	}
	return failure
}

func (r *Releases) Check(ctx context.Context) (*Release, error) {
	if r == nil {
		return nil, &library.Error{Code: library.FailedPrecondition, Message: "this build does not check for new versions"}
	}
	if err := r.init(); err != nil {
		return nil, err
	}
	r.checkMu.Lock()
	defer r.checkMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
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

func (r *Releases) Install(ctx context.Context, version string, quit func()) error {
	if r == nil {
		return &library.Error{Code: library.FailedPrecondition, Message: "this build does not check for new versions"}
	}
	r.mu.Lock()
	latest, installing := r.latest, r.installing
	if latest != nil && latest.Version == version && latest.Manual == "" && !installing {
		r.installing = true
	}
	r.mu.Unlock()
	switch {
	case installing:
		return &library.Error{Code: library.Busy, Message: "a new version is being installed"}
	case latest == nil:
		return &library.Error{Code: library.FailedPrecondition, Message: "no new version to install"}
	case latest.Version != version:
		return &library.Error{Code: library.FailedPrecondition, Message: fmt.Sprintf("the newest version is now %s, not %s", latest.Version, version)}
	case latest.Manual != "":
		return &library.Error{Code: library.FailedPrecondition, Message: fmt.Sprintf("version %s has to be installed from its download page (%s)", latest.Version, latest.Manual)}
	}
	err := r.downloadAndInstall(ctx, latest.Version, quit)
	if err != nil {
		r.mu.Lock()
		r.installing = false
		r.mu.Unlock()
	}
	return err
}

func (r *Releases) downloadAndInstall(ctx context.Context, version string, quit func()) error {
	before := partialDownloads()
	if err := r.updater.DownloadAndInstall(ctx); err != nil {
		// Wails 的 updater 下载失败或取消时没删掉它建的临时目录(返回前把具名返回值 dir 置空了),这里删掉这次多出来的。
		for dir := range partialDownloads() {
			if !before[dir] {
				_ = os.RemoveAll(dir)
			}
		}
		if libraryErr, ok := errors.AsType[*library.Error](err); ok {
			return libraryErr
		}
		if ctx.Err() != nil {
			return unreachable(ctx, err)
		}
		if strings.Contains(err.Error(), "digest mismatch") {
			return &library.Error{Code: library.SourceInvalid, Message: err.Error(), Err: err}
		}
		return &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
	}
	log := installLog(r.stateDir)
	pending := filepath.Join(r.stateDir, pendingFile)
	data, _ := json.Marshal(pendingInstall{Version: version, Log: log})
	if err := os.WriteFile(pending, data, 0o600); err != nil {
		return &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
	}
	if err := r.apply(ctx, r.updater, r.updater.DownloadedPath(), log, quit); err != nil {
		_ = os.Remove(pending)
		return &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
	}
	return nil
}

type githubFeed struct {
	url    string
	client *http.Client
	direct *http.Client
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
	notes := latest.Body
	if i := strings.Index(notes, generatedNotes); i >= 0 {
		notes = notes[i:]
	}
	release := Release{Version: version, Notes: notes, PublishedAt: latest.PublishedAt, PageURL: g.releaseURL(latest.TagName, "tag", "", latest.HTMLURL)}
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
				result.Metadata = map[string]any{"url": g.releaseURL(latest.TagName, "download", asset.Name, asset.URL)}
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
		resp, err := g.get(ctx, g.direct, g.releaseURL(release.TagName, "download", checksumsAsset, asset.URL))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return parseChecksum(resp.Body, name)
	}
	return nil, nil
}

// releaseURL 是这个版本在 GitHub 上的地址:kind 为 "tag" 时是发布页,为 "download" 时是附带的文件 name。
// 读的是 GitHub 上 releases/latest/download/release.json 时,按版本的 tag 拼出 releases/tag/<tag>、releases/download/<tag>/<name>,
// 不用 release.json 里写的地址:新版本信息可能经 GitHub 镜像转发,地址也由镜像决定的话,镜像就能连安装包带校验值一起换掉,
// 或把「打开下载页」指到别的站点。读的不是这种地址(开发时 MECHBOX_RELEASE_FEED 指向的假发布页)时用 release.json 里写的 listed。
func (g *githubFeed) releaseURL(tag, kind, name, listed string) string {
	base, ok := strings.CutSuffix(g.url, "/latest/download/"+releaseManifest)
	if !ok || tag == "" {
		return listed
	}
	location := base + "/" + kind + "/" + url.PathEscape(tag)
	if name != "" {
		location += "/" + url.PathEscape(name)
	}
	return location
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
	resp, err := g.get(ctx, g.client, url)
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
	resp, err := g.get(ctx, g.client, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestSize)).Decode(v); err != nil {
		return &library.Error{Code: library.SourceInvalid, Message: fmt.Sprintf("%s: %v", url, err), Err: err}
	}
	return nil
}

func (g *githubFeed) get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &library.Error{Code: library.Internal, Message: err.Error(), Err: err}
	}
	req.Header.Set("User-Agent", "Mechbox")
	resp, err := client.Do(req)
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
	if errors.Is(ctx.Err(), context.Canceled) {
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

func partialDownloads() map[string]bool {
	dirs := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "wails-update-*"))
	for _, dir := range matches {
		entries, err := os.ReadDir(dir)
		if err == nil && len(entries) == 1 && entries[0].Name() == ".artifact" {
			dirs[dir] = true
		}
	}
	return dirs
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

func installerScript(pid int, setup, staging, dir, exe, log string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return strings.Join([]string{
		"$ErrorActionPreference = 'SilentlyContinue'",
		fmt.Sprintf("Wait-Process -Id %d -Timeout 60", pid),
		fmt.Sprintf("try { $p = Start-Process -FilePath %s -ArgumentList %s -Verb RunAs -Wait -PassThru; $result = \"installer exit code $($p.ExitCode)\" } catch { $result = \"installer did not run: $($_.Exception.Message)\" }", quote(setup), quote("/S /D="+dir)),
		fmt.Sprintf("Set-Content -LiteralPath %s -Value $result -Encoding UTF8", quote(log)),
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

func (r *Releases) Watch(ctx context.Context, checked func(error)) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var last time.Time
	ok := false
	for {
		if now := time.Now().Round(0); releaseDue(now, last, ok) {
			_, err := r.Check(ctx)
			if ctx.Err() == nil {
				checked(err)
			}
			last, ok = now, err == nil
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func releaseDue(now, last time.Time, checked bool) bool {
	if last.IsZero() {
		return true
	}
	wait := releaseRetry
	if checked {
		wait = releaseCheckInterval
	}
	return now.Sub(last) >= wait
}
