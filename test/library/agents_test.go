package library_test

import (
	"reflect"
	"testing"
)

func TestListAgentsReturnsSeededCodes(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	got, err := lib.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 含稳定代码与展示名称:结构体里没有 Agent 目录。
	want := seededAgents()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAgents() = %#v, want %#v", got, want)
	}
}
