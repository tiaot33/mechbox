package library_test

import (
	"path/filepath"
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
		{name: "list scopes", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListScopes(t.Context())
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
