package gitcmd

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

func TestCommandErrorClassifiesGitOutput(t *testing.T) {
	const (
		permission  = "permission"
		missingRef  = "missing ref"
		unreachable = "unreachable"
		other       = "other"
	)
	tests := []struct {
		output string
		want   string
	}{
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", permission},
		{"fatal: could not read Password for 'https://token@git.example.com': terminal prompts disabled", permission},
		{"remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/owner/repo.git/'", permission},
		{"remote: HTTP Basic: Access denied. If a password was provided for Git authentication, the password was incorrect.\nfatal: Authentication failed for 'https://gitlab.com/group/repo.git/'", permission},
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.", permission},
		{"fatal: unable to access 'https://git.example.com/repo.git/': The requested URL returned error: 403", permission},
		{"fatal: unable to access 'https://git.example.com/repo.git/': The requested URL returned error: 401", permission},
		{"error: RPC failed; HTTP 403 curl 22 The requested URL returned error: 403", permission},
		{"warning: Could not find remote branch nope to clone.\nfatal: Remote branch nope not found in upstream origin", missingRef},
		{"fatal: couldn't find remote ref refs/heads/nope", missingRef},
		{"fatal: Couldn't find remote ref nope", missingRef},
		{"fatal: remote error: upload-pack: not our ref 0123456789abcdef0123456789abcdef01234567", missingRef},
		{"fatal: unable to access 'https://nope.invalid/repo.git/': Could not resolve host: nope.invalid", unreachable},
		{"ssh: Could not resolve hostname nope.invalid: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.", unreachable},
		{"ssh: Could not resolve hostname nope.invalid: Name or service not known\nfatal: Could not read from remote repository.", unreachable},
		{"ssh: connect to host git.example.com port 22: Connection refused\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.", unreachable},
		{"ssh: connect to host git.example.com port 22: Network is unreachable", unreachable},
		{"ssh: connect to host git.example.com port 22: No route to host", unreachable},
		{"fatal: unable to access 'https://127.0.0.1:1/repo.git/': Failed to connect to 127.0.0.1 port 1 after 0 ms: Couldn't connect to server", unreachable},
		{"remote: Repository not found.\nfatal: repository 'https://github.com/owner/missing.git/' not found", other},
		{"fatal: 'origin' does not appear to be a git repository", other},
		{"", other},
	}
	for _, test := range tests {
		err := commandError([]byte(test.output))
		var got string
		_, missing := errors.AsType[*source.NotFoundError](err)
		switch {
		case errors.Is(err, source.ErrPermission):
			got = permission
		case missing:
			got = missingRef
		// 连不上和其他失败都用 ErrUnreachable,只靠提示文字区分。
		case errors.Is(err, source.ErrUnreachable) && strings.HasPrefix(err.Error(), "git: remote could not be reached"):
			got = unreachable
		case errors.Is(err, source.ErrUnreachable) && strings.HasPrefix(err.Error(), "git: operation failed"):
			got = other
		}
		if got != test.want {
			t.Errorf("commandError(%q) = %v (%s), want %s", test.output, err, got, test.want)
		}
	}
}

func TestValidSubpath(t *testing.T) {
	for value, want := range map[string]bool{
		"":                   true,
		"skills":             true,
		"skills/pdf":         true,
		"skills/.github":     true,
		"skills/.gitignore":  true,
		"/skills":            false,
		"skills/":            false,
		"skills//pdf":        false,
		"../outside":         false,
		"skills/../../x":     false,
		"./skills":           false,
		`skills\pdf`:         false,
		"C:/skills":          false,
		"skills\x00":         false,
		".git":               false,
		"skills/.git":        false,
		"skills/.git/config": false,
	} {
		if got := ValidSubpath(value); got != want {
			t.Errorf("ValidSubpath(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestTreeMatches(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	refs := []string{"main", "release", "release/v1", "release", "rel"}
	tests := []struct {
		tree string
		want []string
	}{
		{"main", []string{"main"}},
		{"main/skills/pdf", []string{"main"}},
		{"release/v1/skills", []string{"release", "release/v1"}},
		{"releases/skills", nil},
		{commit + "/skills", []string{commit}},
		{commit, []string{commit}},
		{"0123456/skills", nil},
	}
	for _, test := range tests {
		if got := treeMatches(test.tree, refs); !slices.Equal(got, test.want) {
			t.Errorf("treeMatches(%q) = %q, want %q", test.tree, got, test.want)
		}
	}
}
