package discovery

import (
	"errors"
	"testing"
)

func TestParseSkillMDNamesTheProblemAndTheLine(t *testing.T) {
	cases := []struct {
		content string
		problem Problem
		line    int
	}{
		{"# no frontmatter\n", NoFrontmatter, 0},
		{"---\nname: x\ndescription: y\n", UnclosedFrontmatter, 0},
		{"---\nname: x\ndescription: Use when: you need it\n---\n", UnquotedColon, 3},
		{"---\nname: x\ndescription: [unclosed\n---\n", InvalidYAML, 0},
		{"---\ndescription: y\n---\n", InvalidName, 0},
		{"---\nname: x\n---\n", NoDescription, 0},
	}
	for _, c := range cases {
		_, err := ParseSkillMD([]byte(c.content), "x")
		problem, ok := errors.AsType[*SkillMDError](err)
		if !ok || problem.Problem != c.problem || c.line != 0 && problem.Line != c.line {
			t.Errorf("ParseSkillMD(%q) = %#v; want %s at line %d", c.content, err, c.problem, c.line)
		}
	}
}
