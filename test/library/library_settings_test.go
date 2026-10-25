package library_test

import (
	"maps"
	"path/filepath"
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
