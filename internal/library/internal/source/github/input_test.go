package github

import (
	"errors"
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
)

func TestParseGitHubInputAcceptsRepositoryForms(t *testing.T) {
	const repository = "https://github.com/owner/repo.git"
	tests := []struct {
		in   Input
		want githubInput
	}{
		{Input{Input: "owner/repo"}, githubInput{repository: repository}},
		{Input{Input: " Owner/Repo.git/ "}, githubInput{repository: repository}},
		{Input{Input: "owner/repo@pdf tools"}, githubInput{repository: repository, name: "pdf tools"}},
		{Input{Input: "owner/repo#v1"}, githubInput{repository: repository, ref: "v1"}},
		{Input{Input: "owner/repo#v1", Ref: "v1", Subpath: "skills"}, githubInput{repository: repository, ref: "v1", subpath: "skills"}},
		{Input{Input: "https://GitHub.com/owner/repo/"}, githubInput{repository: repository}},
		{Input{Input: "https://github.com/owner/repo/tree/main/skills/pdf"}, githubInput{repository: repository, tree: "main/skills/pdf"}},
		{Input{Input: "git@github.com:owner/repo.git"}, githubInput{repository: repository}},
		{Input{Input: "ssh://git@github.com/owner/repo"}, githubInput{repository: repository}},
		{Input{Input: "ssh://github.com/owner/repo"}, githubInput{repository: repository}},
		{Input{Input: "owner/repo.name_1"}, githubInput{repository: "https://github.com/owner/repo.name_1.git"}},
	}
	for _, test := range tests {
		got, err := parseGitHubInput(test.in)
		if err != nil || got != test.want {
			t.Errorf("parseGitHubInput(%+v) = %+v, %v; want %+v", test.in, got, err, test.want)
		}
	}
}

func TestParseGitHubInputRejectsUnsafeOrAmbiguousInputs(t *testing.T) {
	for _, in := range []Input{
		{Input: ""},
		{Input: "owner/re\npo"},
		{Input: "owner/re\x00po"},
		{Input: `owner\repo`},
		{Input: "owner/repo#"},
		{Input: "owner/repo#a#b"},
		{Input: "owner/repo#v1", Ref: "v2"},
		{Input: "https://gitlab.com/owner/repo"},
		{Input: "https://github.com.evil.example/owner/repo"},
		{Input: "http://github.com/owner/repo"},
		{Input: "https://github.com/owner/repo?tab=readme"},
		{Input: "https://token@github.com/owner/repo"},
		{Input: "ssh://alice@github.com/owner/repo"},
		{Input: "owner"},
		{Input: "-owner/repo"},
		{Input: "owner-/repo"},
		{Input: "own_er/repo"},
		{Input: "owner/repo@"},
		{Input: "owner/repo@ pdf"},
		{Input: "owner/re po"},
		{Input: "owner/."},
		{Input: "owner/.."},
		{Input: "owner/.git"},
		{Input: "owner/repo/blob/main/SKILL.md"},
		{Input: "owner/repo/tree"},
		{Input: "owner/repo/tree/main/../../outside"},
		{Input: "owner/repo/tree/main/.git"},
		{Input: "owner/repo", Subpath: "../outside"},
		{Input: "owner/repo", Subpath: "skills/.git/config"},
	} {
		if got, err := parseGitHubInput(in); !isInputError(err) {
			t.Errorf("parseGitHubInput(%+v) = %+v, %v; want InputError", in, got, err)
		}
	}
}

func TestInvalidInputIsRejectedBeforeContactingGitHub(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := Locate(t.Context(), Input{Input: "owner/repo/tree/main/../../outside"}); !isInputError(err) {
		t.Errorf("Locate = %v", err)
	}
	if _, err := InputRefs(t.Context(), "https://token@github.com/owner/repo"); !isInputError(err) {
		t.Errorf("InputRefs = %v", err)
	}
}

func TestPlainRepository(t *testing.T) {
	for input, want := range map[string]string{
		"Owner/Repo":                     "owner/repo",
		"https://github.com/owner/repo/": "owner/repo",
		"git@github.com:owner/repo.git":  "owner/repo",
		"owner/repo#v1":                  "",
		"owner/repo@pdf":                 "",
		"owner/repo/tree/main":           "",
		"owner":                          "",
	} {
		got, ok := PlainRepository(input)
		if got != want || ok != (want != "") {
			t.Errorf("PlainRepository(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
}

func TestMatches(t *testing.T) {
	for input, want := range map[string]bool{
		"owner/repo":                         true,
		" owner/repo#v1 ":                    true,
		"owner/repo@pdf":                     true,
		"git@github.com:owner/repo.git":      true,
		"https://github.com/owner/repo":      true,
		"https://GITHUB.COM/owner/repo":      true,
		"ssh://git@github.com/owner/repo":    true,
		"https://gitlab.com/owner/repo":      false,
		"https://example.com/owner/repo":     false,
		"://github.com/owner/repo":           false,
		"gitlab.com/owner/repo":              false,
		"git@gitlab.com:owner/repo":          false,
		"user@host/repo":                     false,
		"C:/skills/repo":                     false,
		"owner":                              false,
		"https://raw.githubusercontent.com/": false,
	} {
		if got := Matches(input); got != want {
			t.Errorf("Matches(%q) = %v, want %v", input, got, want)
		}
	}
}

func isInputError(err error) bool {
	_, ok := errors.AsType[*source.InputError](err)
	return ok
}
