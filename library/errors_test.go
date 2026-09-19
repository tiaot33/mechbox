package library_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestFacadeFailuresAlwaysReturnTypedErrors(t *testing.T) {
	tests := []struct {
		name       string
		closeFirst bool
		want       library.ErrorCode
		call       func(library.Library) error
	}{
		{name: "create group", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.CreateGroup(t.Context(), "group")
			return err
		}},
		{name: "install fetch unknown handle", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.InstallFetch(t.Context(), "no-such-handle", nil)
			return err
		}},
		{name: "list versions", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListVersions(t.Context(), 1)
			return err
		}},
		{name: "get version", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.GetVersion(t.Context(), 9999)
			return err
		}},
		{name: "diff versions", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.DiffVersions(t.Context(), 9998, 9999)
			return err
		}},
		{name: "source update", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.CheckUpdate(t.Context(), 9999)
			return err
		}},
		{name: "list registered projects", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListRegisteredProjects(t.Context())
			return err
		}},
		{name: "list collections", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListCollections(t.Context())
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			if test.closeFirst {
				if err := lib.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Cleanup(func() { _ = lib.Close() })
			}
			err = test.call(lib)
			typed, ok := err.(*library.Error)
			if !ok || typed.Code != test.want {
				t.Fatalf("error = %T %v, want *library.Error code %s", err, err, test.want)
			}
		})
	}
}

func TestErrorJSONOmitsEmptyDetailsAndInternalErr(t *testing.T) {
	plain, err := json.Marshal(&library.Error{Code: library.Internal, Message: "boom", Err: errors.New("hidden")})
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"code":"internal","message":"boom"}` {
		t.Fatalf("plain JSON = %s", plain)
	}
	if strings.Contains(string(plain), "hidden") || strings.Contains(string(plain), "details") {
		t.Fatalf("plain JSON leaked internals: %s", plain)
	}

	detailed, err := json.Marshal(&library.Error{
		Code:    library.AlreadyExists,
		Message: "install skill: skill already installed",
		Details: library.ErrorDetails{SkillID: 3, Name: "demo", Field: "", Path: "", ProjectID: 0},
		Err:     errors.New("sqlite"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(detailed) != `{"code":"already_exists","message":"install skill: skill already installed","details":{"name":"demo","skill_id":3}}` {
		t.Fatalf("detailed JSON = %s", detailed)
	}
	if strings.Contains(string(detailed), "sqlite") || strings.Contains(string(detailed), "field") || strings.Contains(string(detailed), "path") {
		t.Fatalf("detailed JSON leaked empty fields or Err: %s", detailed)
	}
}

// 补充各行为规格尚未覆盖的错误分类;已有业务用例覆盖的场景不在这里重复搭建。
func TestFacadeFailureCodes(t *testing.T) {
	tests := []struct {
		name string
		want library.ErrorCode
		call func(*testing.T, library.Library) error
	}{
		{name: "install local file path", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			file := filepath.Join(t.TempDir(), "not-dir")
			writeFile(t, file, []byte("x"), 0o644)
			_, err := lib.Install(t.Context(), library.LocalSource{Path: file})
			return err
		}},
		{name: "remove skill invalid projected copies", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			return lib.RemoveSkill(t.Context(), 1, library.RemoveSkillOptions{ProjectedCopies: "nope"})
		}},
		{name: "unregister project invalid projected copies", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			return lib.UnregisterProject(t.Context(), 1, library.UnregisterProjectOptions{ProjectedCopies: "nope"})
		}},
		{name: "activate invalid on conflict", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			return lib.Activate(t.Context(), 1, library.GlobalScope{Agent: library.AgentCodex}, library.ActivateOptions{OnConflict: "nope"})
		}},
		{name: "activate group invalid on conflict", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			return lib.ActivateGroup(t.Context(), 1, library.GlobalScope{Agent: library.AgentCodex}, library.ActivateOptions{OnConflict: "nope"})
		}},
		{name: "activate group colliding names", want: library.FailedPrecondition, call: func(t *testing.T, lib library.Library) error {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "one", "dup", "SKILL.md"), []byte("# One\n"), 0o644)
			writeFile(t, filepath.Join(root, "two", "dup", "SKILL.md"), []byte("# Two\n"), 0o644)
			first, err := lib.Install(t.Context(), library.LocalSource{Path: filepath.Join(root, "one", "dup")})
			if err != nil {
				t.Fatal(err)
			}
			second, err := lib.Install(t.Context(), library.LocalSource{Path: filepath.Join(root, "two", "dup")})
			if err != nil {
				t.Fatal(err)
			}
			group, err := lib.CreateGroup(t.Context(), "dups")
			if err != nil {
				t.Fatal(err)
			}
			for _, skill := range []library.Skill{first, second} {
				if err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
					t.Fatal(err)
				}
			}
			return lib.ActivateGroup(t.Context(), group.ID, library.GlobalScope{Agent: library.AgentCodex}, library.ActivateOptions{})
		}},
		{name: "remove skill not in group", want: library.NotFound, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "outsider")
			group, err := lib.CreateGroup(t.Context(), "g")
			if err != nil {
				t.Fatal(err)
			}
			return lib.RemoveSkillFromGroup(t.Context(), group.ID, skill.ID)
		}},
		{name: "fetch unreadable archive", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.Fetch(t.Context(), serveArchive(t, []byte("not-a-zip")))
			return err
		}},
		{name: "fetch archive server error", want: library.SourceUnreachable, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 500, strings.NewReader(""))
			_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}})
			return err
		}},
		{name: "search response over size limit", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 200, io.LimitReader(infiniteZeros{}, 1<<20+1))
			_, err := lib.SearchSkills(t.Context(), "q")
			return err
		}},
		{name: "install local missing path", want: library.SourceUnreachable, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.Install(t.Context(), library.LocalSource{Path: filepath.Join(t.TempDir(), "missing")})
			return err
		}},
		{name: "install local unsupported file type", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "SKILL.md"), []byte("# X\n"), 0o644)
			if err := os.Symlink("/does-not-exist", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			_, err := lib.Install(t.Context(), library.LocalSource{Path: root})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			err := test.call(t, lib)
			requireLibraryError(t, err, test.want)
		})
	}
}

func TestCanceledOperationsStillMatchContext(t *testing.T) {
	lib := openTestLibrary(t)
	skill := installNamedSkill(t, lib, "cancel")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name string
		call func() error
	}{
		{"update", func() error { _, err := lib.Update(ctx, skill.ID); return err }},
		{"check updates", func() error { _, err := lib.CheckUpdates(ctx, []library.SkillID{skill.ID}); return err }},
		{"fetch", func() error {
			_, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}})
			return err
		}},
		{"search", func() error { _, err := lib.SearchSkills(ctx, "q"); return err }},
		{"install fetch", func() error {
			fetch, err := lib.Fetch(t.Context(), serveArchive(t, zipArchive(t, map[string]string{"s/SKILL.md": skillDocument("s", "body")})))
			if err != nil {
				t.Fatal(err)
			}
			_, err = lib.InstallFetch(ctx, fetch.ID, nil)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want Canceled wrapping context.Canceled", err)
			}
		})
	}
}

func TestSQLiteUniqueFallbackOmitsDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	if _, err := lib.CreateGroup(t.Context(), "dup"); err != nil {
		t.Fatal(err)
	}
	// 加一个门面不知道的 UNIQUE,绕过显式检查,只走 typedError 兜底。
	editLibraryFixture(t, path, `CREATE UNIQUE INDEX groups_singleton ON groups((1))`)
	_, err = lib.CreateGroup(t.Context(), "other")
	typed := requireLibraryError(t, err, library.AlreadyExists)
	if typed.Details != (library.ErrorDetails{}) {
		t.Fatalf("fallback details = %#v, want zero", typed.Details)
	}
}
