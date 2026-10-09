package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

type Settings struct {
	Token   string
	AllowGH bool
	GHPath  string
}

type Identity int

const (
	Anonymous Identity = iota
	PersonalToken
	GHLogin
)

type TokenStatus int

const (
	NoToken TokenStatus = iota
	TokenValid
	TokenRejected
)

type GHStatus int

const (
	GHDisabled GHStatus = iota
	GHNotFound
	GHNotLoggedIn
	GHLoggedIn
)

type RateLimit struct {
	Limit     int
	Remaining int
	ResetAt   time.Time
}

type Access struct {
	Identity  Identity
	Login     string
	RateLimit RateLimit
	Token     TokenStatus
	GH        GHStatus
	GHPath    string
	GHLogin   string
}

var ghLocations = []string{
	"/opt/homebrew/bin/gh",
	"/usr/local/bin/gh",
	"/opt/local/bin/gh",
	"/home/linuxbrew/.linuxbrew/bin/gh",
	"/usr/bin/gh",
	"/snap/bin/gh",
	`C:\Program Files\GitHub CLI\gh.exe`,
}

type authState struct {
	settings      Settings
	tokenRejected atomic.Bool
	ghRejected    atomic.Bool
	ghMu          sync.Mutex
	ghProbed      bool
	gh            ghAccount
}

type ghAccount struct {
	path  string
	token string
}

type credential struct {
	identity Identity
	token    string
}

func newAuthState(settings Settings) *authState {
	return &authState{settings: settings}
}

func (c *Client) SetSettings(settings Settings) {
	c.auth.Store(newAuthState(settings))
}

func (s *authState) credential(ctx context.Context) (credential, error) {
	if s.settings.Token != "" && !s.tokenRejected.Load() {
		return credential{identity: PersonalToken, token: s.settings.Token}, nil
	}
	if s.settings.AllowGH && !s.ghRejected.Load() {
		gh, err := s.probeGH(ctx, false)
		if err != nil {
			return credential{}, err
		}
		if gh.token != "" {
			return credential{identity: GHLogin, token: gh.token}, nil
		}
	}
	return credential{identity: Anonymous}, nil
}

func (s *authState) reject(identity Identity) {
	switch identity {
	case PersonalToken:
		s.tokenRejected.Store(true)
	case GHLogin:
		s.ghRejected.Store(true)
	}
}

func (s *authState) probeGH(ctx context.Context, refresh bool) (ghAccount, error) {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	if s.ghProbed && !refresh {
		return s.gh, nil
	}
	gh := ghAccount{path: findGH(s.settings.GHPath, ghLocations)}
	if gh.path != "" {
		var output bytes.Buffer
		err := githubCLIOutput(ctx, gh.path, &output, "auth", "token", "--hostname", "github.com")
		if ctx.Err() != nil {
			return ghAccount{}, ctx.Err()
		}
		if token := strings.TrimSpace(output.String()); err == nil && token != "" && !strings.ContainsAny(token, " \t\r\n") {
			gh.token = token
		}
	}
	s.gh, s.ghProbed = gh, true
	s.ghRejected.Store(false)
	return gh, nil
}

func findGH(configured string, locations []string) string {
	if configured != "" {
		if _, err := exec.LookPath(configured); err != nil {
			return ""
		}
		return configured
	}
	if path, err := exec.LookPath("gh"); err == nil {
		return path
	}
	for _, location := range locations {
		if _, err := exec.LookPath(location); err == nil {
			return location
		}
	}
	return ""
}

func ghEnvironment() []string {
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.HasSuffix(name, "_TOKEN") && (strings.HasPrefix(name, "GH_") || strings.HasPrefix(name, "GITHUB_"))
	})
	return append(env, "GH_PROMPT_DISABLED=1")
}

func githubTokenRejected(err error) bool {
	httpErr, ok := errors.AsType[*githubHTTPError](err)
	return ok && !httpErr.rateLimited && httpErr.status == http.StatusUnauthorized
}

func (c *Client) CheckAccess(ctx context.Context) (Access, error) {
	state := c.auth.Load()
	var access Access
	current := -1
	accounts := make([]githubAccount, 0, 2)
	if state.settings.Token != "" {
		account, err := fetchAccount(ctx, state.settings.Token)
		switch {
		case err == nil:
			access.Token = TokenValid
			state.tokenRejected.Store(false)
			accounts = append(accounts, account)
			current, access.Identity = 0, PersonalToken
		case githubTokenRejected(err):
			access.Token = TokenRejected
			state.tokenRejected.Store(true)
		default:
			return Access{}, err
		}
	}
	if state.settings.AllowGH {
		gh, err := state.probeGH(ctx, true)
		if err != nil {
			return Access{}, err
		}
		access.GH, access.GHPath = GHNotFound, gh.path
		if gh.path != "" {
			access.GH = GHNotLoggedIn
		}
		if gh.token != "" {
			account, err := fetchAccount(ctx, gh.token)
			switch {
			case err == nil:
				access.GH, access.GHLogin = GHLoggedIn, account.login
				accounts = append(accounts, account)
				if current < 0 {
					current, access.Identity = len(accounts)-1, GHLogin
				}
			case githubTokenRejected(err):
				state.ghRejected.Store(true)
			default:
				return Access{}, err
			}
		}
	}
	if current < 0 {
		rate, err := fetchAnonymousRateLimit(ctx)
		if err != nil {
			return Access{}, err
		}
		access.Identity, access.RateLimit = Anonymous, rate
		return access, nil
	}
	access.Login, access.RateLimit = accounts[current].login, accounts[current].rate
	return access, nil
}

type githubAccount struct {
	login string
	rate  RateLimit
}

func fetchAccount(ctx context.Context, token string) (githubAccount, error) {
	var payload struct {
		Login string `json:"login"`
	}
	header, err := githubAPIGet(ctx, "/user", token, &payload)
	if httpErr, ok := errors.AsType[*githubHTTPError](err); ok && httpErr.rateLimited {
		return githubAccount{rate: rateLimitFromHeader(header)}, nil
	}
	if err != nil {
		return githubAccount{}, err
	}
	return githubAccount{login: payload.Login, rate: rateLimitFromHeader(header)}, nil
}

func fetchAnonymousRateLimit(ctx context.Context) (RateLimit, error) {
	var payload struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if _, err := githubAPIGet(ctx, "/rate_limit", "", &payload); err != nil {
		return RateLimit{}, err
	}
	core := payload.Resources.Core
	return RateLimit{Limit: core.Limit, Remaining: core.Remaining, ResetAt: unixTime(core.Reset)}, nil
}

func githubAPIGet(ctx context.Context, endpoint, token string, dest any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+githubAPIHost+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "skills-tui")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("check github access: %w", source.ErrUnreachable)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.Header, fmt.Errorf("check github access: %w: %w", newGitHubHTTPError(response), source.ErrUnreachable)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("check github access: %w", source.ErrUnreachable)
	}
	if json.Unmarshal(body, dest) != nil {
		return nil, fmt.Errorf("check github access: invalid response: %w", source.ErrUnreachable)
	}
	return response.Header, nil
}

func rateLimitFromHeader(header http.Header) RateLimit {
	limit, _ := strconv.Atoi(header.Get("X-RateLimit-Limit"))
	remaining, _ := strconv.Atoi(header.Get("X-RateLimit-Remaining"))
	reset, _ := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64)
	return RateLimit{Limit: limit, Remaining: remaining, ResetAt: unixTime(reset)}
}

func unixTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
