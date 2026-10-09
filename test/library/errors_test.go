package library_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestFacadeFailuresAlwaysReturnTypedErrors(t *testing.T) {
	tests := []struct {
		name       string
		closeFirst bool
		want       library.ErrorCode
		call       func(*library.Library) error
	}{
		{name: "create group", closeFirst: true, want: library.Internal, call: func(lib *library.Library) error {
			_, err := lib.CreateGroup(t.Context(), "group")
			return err
		}},
		{name: "fetch unknown handle", want: library.NotFound, call: func(lib *library.Library) error {
			_, err := lib.Fetch(t.Context(), "no-such-handle", []string{"demo"})
			return err
		}},
		{name: "fetch without names", want: library.InvalidArgument, call: func(lib *library.Library) error {
			_, err := lib.Fetch(t.Context(), "no-such-handle", nil)
			return err
		}},
		{name: "list versions", closeFirst: true, want: library.Internal, call: func(lib *library.Library) error {
			_, err := lib.ListVersions(t.Context(), 1)
			return err
		}},
		{name: "list versions of missing skill", want: library.NotFound, call: func(lib *library.Library) error {
			_, err := lib.ListVersions(t.Context(), 9999)
			return err
		}},
		{name: "get version", want: library.NotFound, call: func(lib *library.Library) error {
			_, err := lib.GetVersion(t.Context(), 9999)
			return err
		}},
		{name: "diff versions", want: library.NotFound, call: func(lib *library.Library) error {
			_, err := lib.DiffVersions(t.Context(), 9998, 9999)
			return err
		}},
		{name: "source update", want: library.NotFound, call: func(lib *library.Library) error {
			_, err := lib.Update(t.Context(), library.UpdateRequest{CollectionID: 1, Skills: []library.SkillID{9999}})
			return err
		}},
		{name: "list registered projects", closeFirst: true, want: library.Internal, call: func(lib *library.Library) error {
			_, err := lib.ListRegisteredProjects(t.Context())
			return err
		}},
		{name: "list collections", closeFirst: true, want: library.Internal, call: func(lib *library.Library) error {
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
		Message: "fetch skill: skill already fetched",
		Details: library.ErrorDetails{SkillID: 3, Name: "demo", Field: "", Path: "", ProjectID: 0},
		Err:     errors.New("sqlite"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(detailed) != `{"code":"already_exists","message":"fetch skill: skill already fetched","details":{"name":"demo","skill_id":3}}` {
		t.Fatalf("detailed JSON = %s", detailed)
	}
	if strings.Contains(string(detailed), "sqlite") || strings.Contains(string(detailed), "field") || strings.Contains(string(detailed), "path") {
		t.Fatalf("detailed JSON leaked empty fields or Err: %s", detailed)
	}
}

// ErrorOf 是桌面端错误编码的唯一入口:每个码的 code / details 原样进入 JSON,Err 不外泄但仍可 errors.Is。
func TestErrorOfEncodesEveryCodeWithDetails(t *testing.T) {
	codes := []library.ErrorCode{
		library.InvalidArgument, library.NotFound, library.AlreadyExists, library.Conflict,
		library.FailedPrecondition, library.Canceled, library.SourceUnreachable, library.SourceInvalid,
		library.Busy, library.RateLimited, library.Permission, library.NotConfigured, library.Internal,
	}
	cause := errors.New("hidden cause")
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			original := &library.Error{
				Code:    code,
				Message: "operation: reason",
				Details: library.ErrorDetails{Field: "name", Path: "/p", Name: "demo", SkillID: 3, ProjectID: 4, RetryAt: time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)},
				Err:     cause,
			}
			got := library.ErrorOf(fmt.Errorf("wrapped: %w", original))
			if got != original {
				t.Fatalf("ErrorOf(wrapped) = %#v, want the wrapped *Error %#v", got, original)
			}
			if !errors.Is(got, cause) {
				t.Fatal("ErrorOf dropped Err: errors.Is no longer matches the cause")
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"code":"` + string(code) + `","message":"operation: reason","details":{"field":"name","path":"/p","name":"demo","skill_id":3,"project_id":4,"retry_at":"2026-10-09T13:00:00Z"}}`
			if string(encoded) != want {
				t.Fatalf("JSON = %s, want %s", encoded, want)
			}
		})
	}
}

// 非 *Error 的 error 按技能库规则归类,无可识别原因的记为 internal;消息保留、原错误仍可 errors.Is;nil 仍是 nil。
func TestErrorOfPlainErrorIsInternal(t *testing.T) {
	plain := errors.New("boom")
	got := library.ErrorOf(fmt.Errorf("op: %w", plain))
	if got == nil || got.Code != library.Internal || got.Message != "op: boom" || !errors.Is(got, plain) {
		t.Fatalf("ErrorOf(plain) = %#v, want internal wrapping the original", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"code":"internal","message":"op: boom"}` {
		t.Fatalf("JSON = %s", encoded)
	}
	if got := library.ErrorOf(nil); got != nil {
		t.Fatalf("ErrorOf(nil) = %#v, want nil", got)
	}
	// 与技能库同一套归类:接入层自己产生的 ctx 取消仍是 canceled,不当 bug 报。
	if got := library.ErrorOf(fmt.Errorf("call: %w", context.Canceled)); got.Code != library.Canceled || !errors.Is(got, context.Canceled) {
		t.Fatalf("ErrorOf(canceled) = %#v, want canceled", got)
	}
}

// 补充各行为规格尚未覆盖的错误分类;已有业务用例覆盖的场景不在这里重复搭建。
func TestFacadeFailureCodes(t *testing.T) {
	tests := []struct {
		name string
		want library.ErrorCode
		call func(*testing.T, *library.Library) error
	}{
		{name: "remove skill invalid folders", want: library.InvalidArgument, call: func(t *testing.T, lib *library.Library) error {
			return lib.RemoveSkill(t.Context(), 1, library.RemoveSkillOptions{Folders: "nope"})
		}},
		{name: "unregister project invalid folders", want: library.InvalidArgument, call: func(t *testing.T, lib *library.Library) error {
			return lib.UnregisterProject(t.Context(), 1, library.UnregisterProjectOptions{Folders: "nope"})
		}},
		{name: "remove skill not in group", want: library.NotFound, call: func(t *testing.T, lib *library.Library) error {
			skill := fetchNamedSkill(t, lib, "outsider")
			group, err := lib.CreateGroup(t.Context(), "g")
			if err != nil {
				t.Fatal(err)
			}
			return lib.RemoveSkillFromGroup(t.Context(), group.ID, skill.ID, library.RemoveFromGroupOptions{})
		}},
		{name: "remove skill from group invalid if_no_other_install", want: library.InvalidArgument, call: func(t *testing.T, lib *library.Library) error {
			return lib.RemoveSkillFromGroup(t.Context(), 1, 1, library.RemoveFromGroupOptions{IfNoOtherInstall: "nope"})
		}},
		{name: "preview unreadable archive", want: library.SourceInvalid, call: func(t *testing.T, lib *library.Library) error {
			_, err := lib.PreviewFetch(t.Context(), serveArchive(t, []byte("not-a-zip")))
			return err
		}},
		{name: "preview archive server error", want: library.SourceUnreachable, call: func(t *testing.T, lib *library.Library) error {
			stubHTTPResponse(t, 500, strings.NewReader(""))
			_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: "https://source.example/skills.zip"})
			return err
		}},
		{name: "search response over size limit", want: library.SourceInvalid, call: func(t *testing.T, lib *library.Library) error {
			stubHTTPResponse(t, 200, io.LimitReader(infiniteZeros{}, 1<<20+1))
			_, err := lib.SearchSkills(t.Context(), "q")
			return err
		}},
		{name: "preview local unsupported file type", want: library.SourceInvalid, call: func(t *testing.T, lib *library.Library) error {
			root := filepath.Join(t.TempDir(), "linked")
			writeLocalSkill(t, root, "# X\n")
			if err := os.Symlink("/does-not-exist", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			_, err := fetchLocalDir(t.Context(), lib, root)
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
	skill := fetchNamedSkill(t, lib, "cancel")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name string
		call func() error
	}{
		{"update", func() error { _, err := lib.Update(ctx, singleUpdate(skill)); return err }},
		{"check updates", func() error { _, err := lib.CheckUpdates(ctx, []library.CollectionID{skill.CollectionID}); return err }},
		{"preview", func() error {
			_, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{URL: "https://source.example/skills.zip"})
			return err
		}},
		{"search", func() error { _, err := lib.SearchSkills(ctx, "q"); return err }},
		{"fetch from preview", func() error {
			preview, err := lib.PreviewFetch(t.Context(), serveArchive(t, zipArchive(t, map[string]string{"s/SKILL.md": skillDocument("s", "body")})))
			if err != nil {
				t.Fatal(err)
			}
			_, err = lib.Fetch(ctx, preview.ID, candidateNames(preview))
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
