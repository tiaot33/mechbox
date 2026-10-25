package library_test

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 应用的设置存在数据库里:整组替换,值为空的项不保存,重新打开同一个数据库仍是保存的那一组。
func TestAppSettingsAreReplacedAsAWholeAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	first := openTestLibraryAt(t, path)
	if got, err := first.AppSettings(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("AppSettings of a new library = %v, %v; want none", got, err)
	}
	setAppSettings(t, first, map[string]string{"language": "en", "auto_check": "false"})
	setAppSettings(t, first, map[string]string{"language": "zh", "check_interval": "weekly", "auto_check": ""})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibraryAt(t, path)
	want := map[string]string{"language": "zh", "check_interval": "weekly"}
	if got, err := lib.AppSettings(t.Context()); err != nil || !maps.Equal(got, want) {
		t.Fatalf("AppSettings after reopen = %v, %v; want %v", got, err, want)
	}
	if token, err := lib.GitHubToken(t.Context()); err != nil || token != "" {
		t.Fatalf("GitHubToken = %q, %v; app settings must not touch library settings", token, err)
	}
}

// 设置项名为空或带首尾空白时报 InvalidArgument,原来的设置不变。
func TestAppSettingsRejectInvalidNames(t *testing.T) {
	lib := openTestLibrary(t)
	setAppSettings(t, lib, map[string]string{"language": "en"})
	for _, name := range []string{"", " language", "language\n"} {
		if err := lib.SetAppSettings(t.Context(), map[string]string{name: "x"}); !library.IsCode(err, library.InvalidArgument) {
			t.Fatalf("SetAppSettings(%q) = %v, want InvalidArgument", name, err)
		}
	}
	if got, err := lib.AppSettings(t.Context()); err != nil || !maps.Equal(got, map[string]string{"language": "en"}) {
		t.Fatalf("AppSettings after rejected writes = %v, %v", got, err)
	}
}

// 数据库里明文存着令牌与密钥:新建的数据库文件只有当前用户能读写;已有的、权限更宽的数据库文件连同 -wal 打开时改成 0600。
func TestOpenRestrictsDatabaseFileToOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上文件权限由 ACL 决定")
	}
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openTestLibraryAt(t, path)
	setGitHubToken(t, lib, "secret-token")
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal"} {
		if err := os.WriteFile(name, nil, 0o644); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
		if err := os.Chmod(name, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	openTestLibraryAt(t, path)
	for _, name := range []string{path, path + "-wal"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("%s mode = %v; want 0600", filepath.Base(name), mode)
		}
	}

	fresh := filepath.Join(t.TempDir(), "fresh.db")
	openTestLibraryAt(t, fresh)
	if info, err := os.Stat(fresh); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new database mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}
