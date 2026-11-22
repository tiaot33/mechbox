package library

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/ghproxy"
	"github.com/tiaot33/mechbox/internal/library/internal/proxy"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// ProxyMode 是技能库联网时选用网络代理的方式。
type ProxyMode string

const (
	// ProxySystem 跟随这台机器的系统代理设置(默认)。
	ProxySystem ProxyMode = "system"
	// ProxyManual 使用手动填写的代理地址(HTTP、HTTPS 或 SOCKS5)。
	ProxyManual ProxyMode = "manual"
	// ProxyDirect 不用代理,按进程原本的默认行为联网。
	ProxyDirect ProxyMode = "direct"
)

// ProxySettings 是网络代理设置,用 [Library.SetProxySettings] 存进数据库。
// 只属于这台机器:推送出去的库里不带,拉回时保留本机的。
type ProxySettings struct {
	// Mode 是代理方式;传入时留空为 [ProxySystem],读出的总是三种之一。
	Mode ProxyMode `json:"mode,omitempty"`
	// URL 是手动填写的代理地址(http://、https://、socks5://、socks5h://,可带用户名密码);
	// Mode 为 [ProxyManual] 时必填,切到其他模式时保留以便切回。
	URL string `json:"url,omitempty"`
}

// SystemProxy 是这台机器当前启用的静态系统代理,供设置页展示(密码已隐藏)。
// 各项均为空时表示当前未检测到系统代理。
type SystemProxy struct {
	// HTTP 是系统设置的 HTTP 代理地址。
	HTTP string `json:"http,omitempty"`
	// HTTPS 是系统设置的 HTTPS 代理地址。
	HTTPS string `json:"https,omitempty"`
	// SOCKS 是系统设置的 SOCKS5 代理地址。
	SOCKS string `json:"socks,omitempty"`
}

// ProxySettings 返回存在数据库里的网络代理设置(设置页显示它);没设置过时为 [ProxySystem]。
// 拉回后仍是本机自己的设置。
func (l *Library) ProxySettings(ctx context.Context) (settings ProxySettings, err error) {
	defer func() { err = typedError(err) }()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	stored, err := l.store.ProxySettings(ctx)
	if err != nil {
		return ProxySettings{}, err
	}
	return proxySettingsOf(stored), nil
}

// SetProxySettings 保存网络代理设置并立即生效:下一次联网(读来源、skills.sh、S3 同步、AI 服务以及启动的 git、gh 命令;
// 若打开库时传了 [WithHTTPClient],只影响 git、gh 命令与 [ProxyFunc])随即使用新设置。
// 代理方式不认识时返回 InvalidArgument(Details.Field 为 mode);
// 模式为 [ProxyManual] 且代理地址为空,或代理地址格式无效(不是 http://、https://、socks5://、socks5h:// 或缺少主机等)时
// 返回 InvalidArgument(Details.Field 为 url),原来的设置不变。
// 代理设置只属于这台机器:推送出去的库里不带,拉回时保留本机的。
func (l *Library) SetProxySettings(ctx context.Context, settings ProxySettings) (err error) {
	defer func() { err = typedError(err) }()
	parsed, err := proxy.Parse(string(settings.Mode), settings.URL)
	if err != nil {
		if errors.Is(err, proxy.ErrInvalidMode) {
			return argumentError("set proxy settings", "mode", err.Error())
		}
		return argumentError("set proxy settings", "url", err.Error())
	}
	storedMode := string(parsed.Mode)
	if parsed.Mode == proxy.ModeSystem {
		storedMode = ""
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	if err := l.store.SetProxySettings(ctx, store.ProxySettings{Mode: storedMode, URL: parsed.URL}); err != nil {
		return err
	}
	l.proxy.Set(parsed)
	return nil
}

// SystemProxy 返回这台机器当前启用的静态系统代理(macOS 系统设置、Windows 注册表、Linux 环境变量),
// 每次调用现读一次,密码已隐藏,供设置页在「跟随系统」模式下展示。
func (l *Library) SystemProxy(_ context.Context) (sys SystemProxy, err error) {
	defer func() { err = typedError(err) }()
	cfg := proxy.SystemFresh()
	return SystemProxy{
		HTTP:  proxy.RedactURL(cfg.HTTP),
		HTTPS: proxy.RedactURL(cfg.HTTPS),
		SOCKS: proxy.RedactURL(cfg.SOCKS),
	}, nil
}

// ProxyFunc 返回按 lib 当前网络代理设置决定请求走哪个代理的函数,供应用自己发出的 HTTP 请求(如检查和下载应用新版本)
// 共用同一份代理设置;lib 为 nil 时回退到 [http.ProxyFromEnvironment]。
func ProxyFunc(lib *Library) func(*http.Request) (*url.URL, error) {
	if lib == nil || lib.proxy == nil {
		return http.ProxyFromEnvironment
	}
	return lib.proxy.ProxyFor
}

func proxySettingsOf(stored store.ProxySettings) ProxySettings {
	mode, ok := proxy.ParseMode(stored.Mode)
	if !ok {
		mode = proxy.ModeSystem
	}
	return ProxySettings{Mode: ProxyMode(mode), URL: strings.TrimSpace(stored.URL)}
}

func internalProxySettings(stored store.ProxySettings) proxy.Settings {
	if parsed, err := proxy.Parse(stored.Mode, stored.URL); err == nil {
		return parsed
	}
	s := proxySettingsOf(stored)
	return proxy.Settings{Mode: proxy.Mode(s.Mode), URL: s.URL}
}

// GitHubMirror 返回是否经 gh-proxy(https://gh-proxy.org/)加速访问公开的 GitHub 资源(设置页的开关);没设置过时为 false。
// 拉回后仍是本机自己的设置。
func (l *Library) GitHubMirror(ctx context.Context) (on bool, err error) {
	defer func() { err = typedError(err) }()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.GitHubMirror(ctx)
}

// SetGitHubMirror 打开或关闭 GitHub 镜像加速并立即生效,下一次联网就用新设置。
// 打开后,在原始 GitHub 地址前加 https://gh-proxy.org/ 再发出这些请求:GitHub 来源匿名的 API 请求、raw 文件与源码归档包下载、
// 用 HTTPS 地址的 git 命令,以及直接下载来源里指向 GitHub 的链接([GitHubMirrorTransport] 让应用自己的请求也这样走)。
// 下面这些照常直连 GitHub:带令牌或 Cookie 的请求、带查询串的链接(如私有仓库 Raw 链接里的 ?token=,库自己拼的分页与 recursive 参数除外)、
// gh 命令、SSH 地址的 git 命令,以及重定向后的请求;经镜像的 git 命令不带用户 git 配置里的 http.extraHeader 与凭据助手。
// 镜像连不上、返回 5xx、403 或 429,或者卡着不回(HTTP 请求等响应头最多 10 秒,git 命令最多用掉这一步时限的一半)时,这一次请求改为直连;
// 经镜像的 git 命令连不上或被拒时,按原来的顺序改用直连的 HTTPS 与 SSH,镜像已经给出的分支不存在这类结果照样报出。
// 切换开关时丢掉之前读到的 GitHub 内容缓存与匿名请求的限流冷却。
// 数据库里记的来源地址与 [Source] 的 Label、ViewURL 始终是原始的 GitHub 地址,开关怎么切都不影响已入库的技能与同步。
// 与网络代理([Library.SetProxySettings])互不影响:发往镜像的请求照常按代理设置走。
// 设置只属于这台机器:推送出去的库里不带,拉回时保留本机的。
func (l *Library) SetGitHubMirror(ctx context.Context, on bool) (err error) {
	defer func() { err = typedError(err) }()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	l.mirrorMu.Lock()
	defer l.mirrorMu.Unlock()
	if err := l.store.SetGitHubMirror(ctx, on); err != nil {
		return err
	}
	if l.mirror.On() != on {
		l.mirror.Set(on)
		l.sources.ForgetGitHub()
	}
	return nil
}

// GitHubMirrorTransport 返回经 next 发出请求的 [http.RoundTripper]:lib 打开了 GitHub 镜像加速([Library.SetGitHubMirror])时,
// 不带 Authorization、Cookie 头的 GET、HEAD 请求若发往 GitHub(github.com、api.github.com、raw.githubusercontent.com、codeload.github.com、
// gist.github.com、gist.githubusercontent.com)且不带查询串,先改发到 gh-proxy,镜像连不上、返回 5xx、403、429 或 10 秒内没给响应头时再直连原地址;
// 重定向后的请求与其余请求照常经 next 发出。每个请求发出时现读开关。供应用自己发出的请求(如检查和下载应用新版本)共用这个开关;
// lib 或 next 为 nil 时分别当作没打开与 [http.DefaultTransport]。
func GitHubMirrorTransport(lib *Library, next http.RoundTripper) http.RoundTripper {
	if lib == nil {
		return (*ghproxy.Switch)(nil).Transport(next)
	}
	return lib.mirror.Transport(next)
}
