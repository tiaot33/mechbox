package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// ListAgents 以库内 agents 表为准,而不是内置常量:旧库靠 Open 补种子,额外行也会被列出。
func TestListAgentsReadsAgentsTableNotConstants(t *testing.T) {
	ctx := t.Context()
	want := seededAgents()

	t.Run("legacy library without agents table is seeded on Open", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "library.db")
		writeV1LibraryFile(t, path)
		lib, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lib.Close() })
		got, err := lib.ListAgents(ctx)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("ListAgents() after migrating legacy library = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("extra agent row is listed without a code constant", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "library.db")
		lib, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.Close(); err != nil {
			t.Fatal(err)
		}
		// 只用 SQL 构造多一行 Agent 的输入文件;门面须按库内容列出,而不是按内置常量。
		editLibraryFixture(t, path, `INSERT INTO agents(code, name, global_path, project_path) VALUES ('another', 'Another', '.another/skills', '.another/skills')`)
		reopened, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.ListAgents(ctx)
		wantExtra := append([]library.Agent{{Code: "another", Name: "Another"}}, seededAgents()...)
		if err != nil || !reflect.DeepEqual(got, wantExtra) {
			t.Fatalf("ListAgents() = %#v, %v; want %#v", got, err, wantExtra)
		}
	})
}

func TestListAgentsReturnsSeededCodes(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	got, err := lib.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 含稳定代码与展示名称:结构体里没有布局规则,也没有绝对目录。
	want := seededAgents()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAgents() = %#v, want %#v", got, want)
	}
}
