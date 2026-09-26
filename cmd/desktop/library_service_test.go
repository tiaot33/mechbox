package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestLibraryServiceCreatesLibraryInMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", appDirName, "library.db")
	service := NewLibraryService(path)
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() {
		if err := service.ServiceShutdown(); err != nil {
			t.Errorf("ServiceShutdown: %v", err)
		}
	})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("library file not created: %v", err)
	}
	skills, err := service.ListSkills(t.Context())
	if err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills = %v, %v; want empty", skills, err)
	}
	collections, err := service.ListCollections(t.Context())
	if err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections = %v, %v; want empty", collections, err)
	}
	groups, err := service.ListGroupsWithMembers(t.Context())
	if err != nil || len(groups) != 0 {
		t.Fatalf("ListGroupsWithMembers = %v, %v; want empty", groups, err)
	}
	projections, err := service.InspectProjections(t.Context(), library.ProjectionFilter{})
	if err != nil || len(projections) != 0 {
		t.Fatalf("InspectProjections = %v, %v; want empty", projections, err)
	}
	agents, err := service.ListAgents(t.Context())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ListAgents = %v, %v; want built-in agents", agents, err)
	}
	info, err := service.Info()
	if err != nil || info.LibraryPath != path || info.HomeDir == "" {
		t.Fatalf("Info = %+v, %v; want library path %q and a home directory", info, err, path)
	}
}

func TestLibraryServiceReopensExistingLibrary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	for range 2 {
		service := NewLibraryService(path)
		if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
			t.Fatalf("ServiceStartup: %v", err)
		}
		if err := service.ServiceShutdown(); err != nil {
			t.Fatalf("ServiceShutdown: %v", err)
		}
	}
}

func TestLibraryServiceRejectsForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := NewLibraryService(path)
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err == nil {
		_ = service.ServiceShutdown()
		t.Fatal("ServiceStartup succeeded on a foreign file")
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatalf("ServiceShutdown after failed startup: %v", err)
	}
}

func TestLibraryServiceSwallowsErrorsOfCancelledCalls(t *testing.T) {
	service := NewLibraryService(filepath.Join(t.TempDir(), "library.db"))
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = service.ServiceShutdown() })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ListSkills(ctx); err != nil {
		t.Fatalf("ListSkills on a cancelled call = %v; want nil so Wails discards the result", err)
	}
	// 未取消的调用照常返回错误。
	if _, err := service.Resolve(t.Context(), library.Scope{Kind: "bogus"}); err == nil {
		t.Fatal("Resolve with an invalid scope succeeded")
	}
}

func TestMarshalErrorEncodesLibraryError(t *testing.T) {
	var got library.Error
	if err := json.Unmarshal(marshalError(errors.New("boom")), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != library.ErrorOf(errors.New("boom")).Code || got.Message == "" {
		t.Fatalf("marshalError = %+v; want code and message from library.ErrorOf", got)
	}
}
