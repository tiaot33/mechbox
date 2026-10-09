package library

import (
	"context"
	"path/filepath"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
)

// GitHubSettings 是设置页「GitHub」分区里用户填的内容。技能库只在内存里用它,不保存;
// 桌面端在启动时和用户每次修改后用 [Library.SetGitHubSettings] 交进来。零值表示不用令牌、不允许 gh,只能匿名访问。
type GitHubSettings struct {
	// Token 是用户粘贴的个人访问令牌;空表示没填。
	Token string `json:"token,omitempty"`
	// AllowGH 对应「允许使用 gh 命令登录」开关。关掉时技能库任何情况下都不调用 gh。
	AllowGH bool `json:"allow_gh,omitzero"`
	// GHPath 是用户指定的 gh 位置,须是绝对路径;空表示由技能库在 PATH 与 gh 的常见安装位置(含 Homebrew)里找。
	GHPath string `json:"gh_path,omitempty"`
}

// SetGitHubSettings 换上用户在设置页保存的 GitHub 设置,立即生效:之前因令牌或 gh 的登录被 GitHub 拒绝而记下的判断全部作废,
// 下一次读 GitHub 按新设置重新选身份,已打开的入库预览之后的读取也一样。
//
// 读 GitHub 时按下面的顺序选身份:有令牌就用令牌;没有令牌或令牌被 GitHub 拒绝时,允许 gh 才用 gh 登录的账号;
// 否则匿名,只能读公开仓库。GitHub 不可用时退回的 git 浅克隆不带这些身份,只用用户自己的 git 凭证。
//
// GHPath 不是绝对路径时返回 InvalidArgument(Details.Field 为 "gh_path"),设置不变。
// 给出的位置上没有可执行的 gh 不算错误,[Library.CheckGitHubAccess] 会报告没找到 gh。
func (l *Library) SetGitHubSettings(settings GitHubSettings) error {
	if settings.GHPath != "" && !filepath.IsAbs(settings.GHPath) {
		return argumentError("set github settings", "gh_path", "gh path must be absolute")
	}
	l.github.SetSettings(github.Settings{Token: settings.Token, AllowGH: settings.AllowGH, GHPath: settings.GHPath})
	return nil
}

// GitHubIdentity 是读 GitHub 时用的身份,设置页的「当前身份」显示它。
type GitHubIdentity string

const (
	// GitHubAnonymous 匿名访问:只能读公开仓库,每小时请求次数最少。
	GitHubAnonymous GitHubIdentity = "anonymous"
	// GitHubPersonalToken 用设置里的个人访问令牌。
	GitHubPersonalToken GitHubIdentity = "token"
	// GitHubGHLogin 用 gh 命令登录的账号。
	GitHubGHLogin GitHubIdentity = "gh"
)

// GitHubTokenStatus 是设置里的个人访问令牌是否能用,设置页「测试」按钮的结果。
type GitHubTokenStatus string

const (
	// GitHubTokenNone 没填令牌。
	GitHubTokenNone GitHubTokenStatus = "none"
	// GitHubTokenValid GitHub 认这个令牌。
	GitHubTokenValid GitHubTokenStatus = "valid"
	// GitHubTokenRejected GitHub 拒绝了这个令牌(无效、已过期或已撤销);读 GitHub 时会跳过它。
	GitHubTokenRejected GitHubTokenStatus = "rejected"
)

// GHStatus 是「允许使用 gh 命令登录」开关下面显示的 gh 状态。
type GHStatus string

const (
	// GHDisabled 开关关着,没有调用 gh。
	GHDisabled GHStatus = "disabled"
	// GHNotFound 指定的位置、PATH 与 gh 的常见安装位置都没有 gh;界面让用户指定 gh 的位置。
	GHNotFound GHStatus = "not_found"
	// GHNotLoggedIn 找到了 gh,但它没有登录 github.com,或者它的登录被 GitHub 拒绝。
	GHNotLoggedIn GHStatus = "not_logged_in"
	// GHLoggedIn 找到了 gh,且已登录;账号在 GHAccess.Login。
	GHLoggedIn GHStatus = "logged_in"
)

// GitHubRateLimit 是当前身份本小时的请求次数。
type GitHubRateLimit struct {
	// Limit 是本小时的总次数。
	Limit int `json:"limit"`
	// Remaining 是本小时还剩的次数。
	Remaining int `json:"remaining"`
	// ResetAt 是次数恢复的时间;GitHub 没给出时为零值并省略。
	ResetAt time.Time `json:"reset_at,omitzero"`
}

// GHAccess 是 gh 命令的状态。
type GHAccess struct {
	// Status 是 gh 是否找到、是否登录。
	Status GHStatus `json:"status"`
	// Path 是找到的 gh 位置;没找到或开关关着时为空。
	Path string `json:"path,omitempty"`
	// Login 是 gh 登录的 GitHub 账号;Status 为 logged_in 时才有。
	Login string `json:"login,omitempty"`
}

// GitHubAccess 是设置页「GitHub」分区显示的访问状态,由 [Library.CheckGitHubAccess] 给出。
type GitHubAccess struct {
	// Identity 是现在读 GitHub 会用的身份。
	Identity GitHubIdentity `json:"identity"`
	// Login 是当前身份的 GitHub 账号;匿名时为空,请求次数已用完时 GitHub 不给出账号,也为空。
	Login string `json:"login,omitempty"`
	// RateLimit 是当前身份本小时的请求次数与恢复时间。
	RateLimit GitHubRateLimit `json:"rate_limit"`
	// Token 是设置里的个人访问令牌是否有效。
	Token GitHubTokenStatus `json:"token"`
	// GH 是 gh 是否找到、登录的是哪个账号。
	GH GHAccess `json:"gh"`
}

// CheckGitHubAccess 按当前的 GitHub 设置向 GitHub 确认访问状态:令牌是否有效、gh 是否找到并登录、
// 现在读 GitHub 会用哪个身份、它的账号与本小时还剩多少次请求。设置页打开、用户点「测试」或改了设置之后调用。
//
// 每次调用都重新确认:令牌被拒的判断以这次结果为准,gh 重新查找并读取登录,用户刚在终端里登录 gh 也能看到。
// 开关关着时不调用 gh。查询本身不消耗匿名身份的请求次数;令牌与 gh 各消耗一次。
// 连不上 GitHub 时返回 SourceUnreachable。
func (l *Library) CheckGitHubAccess(ctx context.Context) (access GitHubAccess, err error) {
	defer func() { err = typedError(err) }()
	got, err := l.github.CheckAccess(ctx)
	if err != nil {
		return GitHubAccess{}, err
	}
	return GitHubAccess{
		Identity:  [...]GitHubIdentity{github.Anonymous: GitHubAnonymous, github.PersonalToken: GitHubPersonalToken, github.GHLogin: GitHubGHLogin}[got.Identity],
		Login:     got.Login,
		RateLimit: GitHubRateLimit{Limit: got.RateLimit.Limit, Remaining: got.RateLimit.Remaining, ResetAt: got.RateLimit.ResetAt},
		Token:     [...]GitHubTokenStatus{github.NoToken: GitHubTokenNone, github.TokenValid: GitHubTokenValid, github.TokenRejected: GitHubTokenRejected}[got.Token],
		GH: GHAccess{
			Status: [...]GHStatus{github.GHDisabled: GHDisabled, github.GHNotFound: GHNotFound, github.GHNotLoggedIn: GHNotLoggedIn, github.GHLoggedIn: GHLoggedIn}[got.GH],
			Path:   got.GHPath,
			Login:  got.GHLogin,
		},
	}, nil
}
