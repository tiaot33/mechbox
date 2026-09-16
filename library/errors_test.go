package library_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// TestFacadeFailureCodes 是 ADR 0004 最小码集的可执行"情况 → 码"总表:spec 列出的每种失败各占一行。
// 细节字段的断言放在各行为规格文件旁,这里只固定归类。
func TestFacadeFailureCodes(t *testing.T) {
	tests := []struct {
		name string
		want library.ErrorCode
		call func(*testing.T, library.Library) error
	}{
		{name: "create group blank name", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.CreateGroup(t.Context(), "  ")
			return err
		}},
		{name: "rename group blank name", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			group, err := lib.CreateGroup(t.Context(), "named")
			if err != nil {
				t.Fatal(err)
			}
			return lib.RenameGroup(t.Context(), group.ID, "")
		}},
		{name: "register project empty path", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.RegisterProject(t.Context(), "")
			return err
		}},
		{name: "register project file path", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			file := filepath.Join(t.TempDir(), "file")
			writeFile(t, file, []byte("x"), 0o644)
			_, err := lib.RegisterProject(t.Context(), file)
			return err
		}},
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
		{name: "diff versions across skills", want: library.InvalidArgument, call: func(t *testing.T, lib library.Library) error {
			first := installNamedSkill(t, lib, "one")
			second := installNamedSkill(t, lib, "two")
			versionsFirst, err := lib.ListVersions(t.Context(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			versionsSecond, err := lib.ListVersions(t.Context(), second.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = lib.DiffVersions(t.Context(), versionsFirst[0].ID, versionsSecond[0].ID)
			return err
		}},
		{name: "install fetch already installed", want: library.AlreadyExists, call: func(t *testing.T, lib library.Library) error {
			spec := serveArchive(t, zipArchive(t, map[string]string{"s/SKILL.md": skillDocument("s", "body")}))
			first, err := lib.Fetch(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lib.InstallFetch(t.Context(), first.ID, nil); err != nil {
				t.Fatal(err)
			}
			second, err := lib.Fetch(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			_, err = lib.InstallFetch(t.Context(), second.ID, nil)
			return err
		}},
		{name: "install duplicate local skill", want: library.AlreadyExists, call: func(t *testing.T, lib library.Library) error {
			source := filepath.Join(t.TempDir(), "dup")
			writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Dup\n"), 0o644)
			if _, err := lib.Install(t.Context(), library.LocalSource{Path: source}); err != nil {
				t.Fatal(err)
			}
			_, err := lib.Install(t.Context(), library.LocalSource{Path: source})
			return err
		}},
		{name: "add skill already in group", want: library.AlreadyExists, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "member")
			group, err := lib.CreateGroup(t.Context(), "g")
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
				t.Fatal(err)
			}
			return lib.AddSkillToGroup(t.Context(), group.ID, skill.ID)
		}},
		{name: "register project duplicate path", want: library.AlreadyExists, call: func(t *testing.T, lib library.Library) error {
			root := t.TempDir()
			if _, err := lib.RegisterProject(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			_, err := lib.RegisterProject(t.Context(), root)
			return err
		}},
		{name: "activate existing target", want: library.AlreadyExists, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "target")
			scope := library.GlobalScope{Agent: library.AgentCodex}
			directory, err := lib.Resolve(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(directory, skill.Name, "old.txt"), []byte("old"), 0o644)
			return lib.Activate(t.Context(), skill.ID, scope, library.ActivateOptions{})
		}},
		{name: "deactivate group-backed skill", want: library.FailedPrecondition, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "grouped")
			group, err := lib.CreateGroup(t.Context(), "only")
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
				t.Fatal(err)
			}
			scope := library.GlobalScope{Agent: library.AgentCodex}
			if err := lib.ActivateGroup(t.Context(), group.ID, scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
			return lib.Deactivate(t.Context(), skill.ID, scope)
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
		{name: "activate group existing target", want: library.AlreadyExists, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "member")
			group, err := lib.CreateGroup(t.Context(), "g")
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
				t.Fatal(err)
			}
			scope := library.GlobalScope{Agent: library.AgentCodex}
			directory, err := lib.Resolve(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(directory, skill.Name, "old.txt"), []byte("old"), 0o644)
			return lib.ActivateGroup(t.Context(), group.ID, scope, library.ActivateOptions{})
		}},
		{name: "activate directory shared by another scope", want: library.FailedPrecondition, call: func(t *testing.T, lib library.Library) error {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			skill := installNamedSkill(t, lib, "shared")
			project, err := lib.RegisterProject(t.Context(), home)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.Activate(t.Context(), skill.ID, library.GlobalScope{Agent: library.AgentCodex}, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
			return lib.Activate(t.Context(), skill.ID, library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}, library.ActivateOptions{})
		}},
		{name: "activate name claimed by another skill", want: library.FailedPrecondition, call: func(t *testing.T, lib library.Library) error {
			holder := installNamedSkill(t, lib, "shared")
			challenger := installNamedSkill(t, lib, "shared")
			scope := library.GlobalScope{Agent: library.AgentCodex}
			if err := lib.Activate(t.Context(), holder.ID, scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
			return lib.Activate(t.Context(), challenger.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
		}},
		{name: "deactivate inactive skill", want: library.NotFound, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "idle")
			return lib.Deactivate(t.Context(), skill.ID, library.GlobalScope{Agent: library.AgentCodex})
		}},
		{name: "deactivate inactive group", want: library.NotFound, call: func(t *testing.T, lib library.Library) error {
			group, err := lib.CreateGroup(t.Context(), "idle")
			if err != nil {
				t.Fatal(err)
			}
			return lib.DeactivateGroup(t.Context(), group.ID, library.GlobalScope{Agent: library.AgentCodex})
		}},
		{name: "remove skill not in group", want: library.NotFound, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "outsider")
			group, err := lib.CreateGroup(t.Context(), "g")
			if err != nil {
				t.Fatal(err)
			}
			return lib.RemoveSkillFromGroup(t.Context(), group.ID, skill.ID)
		}},
		{name: "sync pull unacceptable remote", want: library.FailedPrecondition, call: func(t *testing.T, lib library.Library) error {
			stub := newS3Stub(t)
			if _, err := lib.SyncPush(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			stub.set([]byte("not a database"))
			preview, err := lib.PreviewSyncPull(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, err = lib.SyncPull(t.Context(), preview.Remote)
			return err
		}},
		{name: "sync pull changed remote", want: library.Conflict, call: func(t *testing.T, lib library.Library) error {
			stub := newS3Stub(t)
			if _, err := lib.SyncPush(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			preview, err := lib.PreviewSyncPull(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			stub.set([]byte("changed after preview"))
			_, err = lib.SyncPull(t.Context(), preview.Remote)
			return err
		}},
		{name: "sync unconfigured", want: library.NotConfigured, call: func(t *testing.T, lib library.Library) error {
			t.Setenv("SKILLS_S3_BUCKET", "")
			_, err := lib.PreviewSyncPull(t.Context())
			return err
		}},
		{name: "fetch missing git ref", want: library.NotFound, call: func(t *testing.T, lib library.Library) error {
			githubRemote(t)
			spec, err := lib.ResolveSource(t.Context(), "owner/repo#does-not-exist", library.ResolveSourceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = lib.Fetch(t.Context(), spec)
			return err
		}},
		{name: "fetch unconfigured store", want: library.NotConfigured, call: func(t *testing.T, lib library.Library) error {
			t.Setenv("SKILLS_STORE_URL", "")
			_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceStore, Locator: library.SourceLocator{PackageID: "pkg", Version: "1"}})
			return err
		}},
		{name: "fetch no valid candidates", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.Fetch(t.Context(), serveArchive(t, zipArchive(t, map[string]string{"bad/SKILL.md": "---\nname: [broken\n---\n"})))
			return err
		}},
		{name: "fetch unreadable archive", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.Fetch(t.Context(), serveArchive(t, []byte("not-a-zip")))
			return err
		}},
		{name: "fetch archive with unsafe path", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			_, err := lib.Fetch(t.Context(), serveArchive(t, zipArchive(t, map[string]string{"s/SKILL.md": skillDocument("s", "body"), "../escape": "x"})))
			return err
		}},
		{name: "fetch archive over file count limit", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			files := map[string]string{"s/SKILL.md": skillDocument("s", "body")}
			for i := range 1000 {
				files[fmt.Sprintf("extra/%d.txt", i)] = "x"
			}
			_, err := lib.Fetch(t.Context(), serveArchive(t, zipArchive(t, files)))
			return err
		}},
		{name: "fetch archive over expanded size limit", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			if _, err := writer.CreateRaw(&zip.FileHeader{Name: "ignored/huge", UncompressedSize64: 65 << 20}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := lib.Fetch(t.Context(), serveArchive(t, buffer.Bytes()))
			return err
		}},
		{name: "fetch archive over download size limit", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 200, io.LimitReader(infiniteZeros{}, 64<<20+1))
			_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}})
			return err
		}},
		{name: "fetch archive server error", want: library.SourceUnreachable, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 500, strings.NewReader(""))
			_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}})
			return err
		}},
		{name: "fetch archive forbidden", want: library.Permission, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 403, strings.NewReader(""))
			_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}})
			return err
		}},
		{name: "search malformed response", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 200, strings.NewReader("broken"))
			_, err := lib.SearchSkills(t.Context(), "q")
			return err
		}},
		{name: "search response over size limit", want: library.SourceInvalid, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 200, io.LimitReader(infiniteZeros{}, 1<<20+1))
			_, err := lib.SearchSkills(t.Context(), "q")
			return err
		}},
		{name: "search server error", want: library.SourceUnreachable, call: func(t *testing.T, lib library.Library) error {
			stubHTTPResponse(t, 500, strings.NewReader(""))
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
		{name: "canceled update", want: library.Canceled, call: func(t *testing.T, lib library.Library) error {
			skill := installNamedSkill(t, lib, "cancel")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := lib.Update(ctx, skill.ID)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// HOME 取规范路径,使 RegisterProject(home) 与全局 Scope 解析到同一目录。
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			err = test.call(t, lib)
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
	editLibraryFixture(t, path, `CREATE UNIQUE INDEX groups_name ON groups(name)`)
	_, err = lib.CreateGroup(t.Context(), "dup")
	typed := requireLibraryError(t, err, library.AlreadyExists)
	if typed.Details != (library.ErrorDetails{}) {
		t.Fatalf("fallback details = %#v, want zero", typed.Details)
	}
}

func TestSQLiteForeignKeyFallbackOmitsDetails(t *testing.T) {
	var first atomic.Bool
	started := make(chan struct{})
	release := make(chan struct{})
	old := zipArchive(t, map[string]string{"skill/SKILL.md": skillDocument("skill", "old")})
	updated := zipArchive(t, map[string]string{"skill/SKILL.md": skillDocument("skill", "new")})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if first.CompareAndSwap(false, true) {
			_, _ = w.Write(old)
			return
		}
		close(started)
		<-release
		_, _ = w.Write(updated)
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "library.db")
	lib1, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib1.Close() })
	lib2, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib2.Close() })

	spec := library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL + "/skills.zip"}}
	fetch, err := lib1.Fetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib1.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("install = %#v, %v", installed, err)
	}

	go func() {
		<-started
		_ = lib2.RemoveSkill(t.Context(), installed[0].ID, library.RemoveSkillOptions{})
		close(release)
	}()
	_, err = lib1.Update(t.Context(), installed[0].ID)
	typed := requireLibraryError(t, err, library.NotFound)
	if typed.Details != (library.ErrorDetails{}) {
		t.Fatalf("fallback details = %#v, want zero", typed.Details)
	}
}
