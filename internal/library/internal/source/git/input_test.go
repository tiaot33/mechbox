package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
)

func TestParseAcceptsRepositoryForms(t *testing.T) {
	tests := []struct {
		in   Input
		want parsedInput
	}{
		{Input{Input: "gitlab:group/repo"}, parsedInput{repository: "https://gitlab.com/group/repo.git", gitlab: true}},
		{Input{Input: "gitlab.com/group/sub/repo"}, parsedInput{repository: "https://gitlab.com/group/sub/repo.git", gitlab: true}},
		{Input{Input: "https://GitLab.com/group/repo.git/"}, parsedInput{repository: "https://GitLab.com/group/repo.git", gitlab: true}},
		{Input{Input: "https://gitlab.com/group/repo/-/tree/main/skills"}, parsedInput{repository: "https://gitlab.com/group/repo.git", tree: "main/skills", gitlab: true}},
		{Input{Input: "https://git.example.com/team/repo/-/tree/dev"}, parsedInput{repository: "https://git.example.com/team/repo.git", tree: "dev", gitlab: true}},
		{Input{Input: "http://git.example.com/repo.git"}, parsedInput{repository: "http://git.example.com/repo.git"}},
		{Input{Input: "https://git.example.com/team/repo.git#v1"}, parsedInput{repository: "https://git.example.com/team/repo.git", ref: "v1"}},
		{Input{Input: "https://git.example.com/repo.git#v1", Ref: "v1", Subpath: "skills/pdf"}, parsedInput{repository: "https://git.example.com/repo.git", ref: "v1", subpath: "skills/pdf"}},
		{Input{Input: "git@git.example.com:team/repo.git"}, parsedInput{repository: "git@git.example.com:team/repo.git"}},
		{Input{Input: "git@gitlab.com:group/repo/"}, parsedInput{repository: "git@gitlab.com:group/repo", gitlab: true}},
		{Input{Input: "ssh://git@git.example.com:2222/team/repo.git"}, parsedInput{repository: "ssh://git@git.example.com:2222/team/repo.git"}},
		{Input{Input: "ssh://git@gitlab.com/group/repo"}, parsedInput{repository: "ssh://git@gitlab.com/group/repo", gitlab: true}},
	}
	for _, test := range tests {
		got, err := parse(test.in)
		if err != nil || got != test.want {
			t.Errorf("parse(%+v) = %+v, %v; want %+v", test.in, got, err, test.want)
		}
	}
}

func TestParseRejectsUnsafeOrAmbiguousInputs(t *testing.T) {
	for _, in := range []Input{
		{Input: ""},
		{Input: "   "},
		{Input: "https://git.example.com/a b.git"},
		{Input: "https://git.example.com/re\npo.git"},
		{Input: "https://git.example.com/re\rpo.git"},
		{Input: "https://git.example.com/repo.git\x00"},
		{Input: `https://git.example.com\repo.git`},
		{Input: "https://git.example.com/repo.git#"},
		{Input: "https://git.example.com/repo.git#a#b"},
		{Input: "https://git.example.com/repo.git#v1", Ref: "v2"},
		{Input: "git@git.example.com"},
		{Input: "git@:team/repo.git"},
		{Input: "git@user@git.example.com:team/repo.git"},
		{Input: "git@git.example.com/x:team/repo.git"},
		{Input: "git@git.example.com:-oProxyCommand=evil"},
		{Input: "git@git.example.com:team/../repo.git"},
		{Input: "git@git.example.com:team//repo.git"},
		{Input: "git@git.example.com:team/.git"},
		{Input: "git@git.example.com:team/-"},
		{Input: "https://user:secret@git.example.com/repo.git"},
		{Input: "https://token@git.example.com/repo.git"},
		{Input: "ssh://git:secret@git.example.com/team/repo.git"},
		{Input: "https://git.example.com/repo.git?private_token=x"},
		{Input: "https:///repo.git"},
		{Input: "ftp://git.example.com/repo.git"},
		{Input: "file:///tmp/repo.git"},
		{Input: "ext::sh"},
		{Input: "https://git.example.com/-repo.git"},
		{Input: "https://git.example.com/"},
		{Input: "https://gitlab.com/repo"},
		{Input: "gitlab:repo"},
		{Input: "https://git.example.com/team/repo/-/tree/"},
		{Input: "https://git.example.com/team/repo/-/tree/../outside"},
		{Input: "https://git.example.com/team/repo/-/tree/main/.git/hooks"},
		{Input: "https://git.example.com/repo.git", Subpath: "../outside"},
		{Input: "https://git.example.com/repo.git", Subpath: "/skills"},
		{Input: "https://git.example.com/repo.git", Subpath: ".git"},
		{Input: "https://git.example.com/repo.git", Subpath: "skills/.git/config"},
		{Input: "https://git.example.com/repo.git", Subpath: "C:/skills"},
	} {
		if got, err := parse(in); !isInputError(err) {
			t.Errorf("parse(%+v) = %+v, %v; want InputError", in, got, err)
		}
	}
}

func TestInvalidInputIsRejectedBeforeContactingRemote(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := Locate(t.Context(), Input{Input: "git@git.example.com:-oProxyCommand=evil"}); !isInputError(err) {
		t.Errorf("Locate = %v", err)
	}
	if _, err := InputRefs(t.Context(), "https://user:secret@git.example.com/repo.git"); !isInputError(err) {
		t.Errorf("InputRefs = %v", err)
	}
	if _, err := Locate(t.Context(), Input{Input: "ssh://git:secret@git.example.com/team/repo.git"}); !isInputError(err) || strings.Contains(err.Error(), "secret") {
		t.Errorf("Locate with ssh password = %v", err)
	}
	if _, ok, err := Probe(t.Context(), "https://git.example.com/repo", "", "../outside"); ok || !isInputError(err) {
		t.Errorf("Probe = %v, %v", ok, err)
	}
}

func TestMatches(t *testing.T) {
	for input, want := range map[string]bool{
		"gitlab:group/repo":                   true,
		"GitLab.com/group/repo":               true,
		"ssh://git@git.example.com/repo":      true,
		"git@git.example.com:team/repo":       true,
		"https://gitlab.com/group/repo":       true,
		"https://git.example.com/g/r/-/tree/": true,
		"https://git.example.com/repo.git":    true,
		"https://git.example.com/repo.git/":   true,
		"https://git.example.com/repo.git#v1": true,
		" https://git.example.com/repo.git ":  true,
		"git@git.example.com":                 false,
		"https://git.example.com/repo":        false,
		"https://example.com/skills.zip":      false,
		"ftp://git.example.com/repo.git":      false,
		"owner/repo":                          false,
		"/tmp/repo.git":                       false,
		"https:///repo.git":                   false,
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
