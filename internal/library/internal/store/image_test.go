package store

import (
	"bytes"
	"maps"
	"path/filepath"
	"testing"
)

func TestMachineConfigIsNotPushedAndIsKeptOnPull(t *testing.T) {
	ctx := t.Context()
	open := func(name string) *Store {
		t.Helper()
		s, err := Open(filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	set := func(s *Store, values map[string]string) {
		t.Helper()
		for key, value := range values {
			if err := s.setConfig(ctx, key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	all := func(s *Store) map[string]string {
		t.Helper()
		values, err := s.configWithPrefix(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		return values
	}

	pushing := open("pushing.db")
	set(pushing, map[string]string{
		"github_token": "pushed-token", "app.language": "en",
		"wsl_distro": "PUSHINGDISTRO", "sync.bucket": "pushing-bucket", "sync.secret_access_key": "PUSHINGSECRET",
	})
	data, err := pushing.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []string{"PUSHINGDISTRO", "PUSHINGSECRET", "pushing-bucket"} {
		if bytes.Contains(data, []byte(local)) {
			t.Fatalf("snapshot contains machine config %q", local)
		}
	}

	pulling := open("pulling.db")
	set(pulling, map[string]string{
		"github_token": "local-token", "app.dock_badge": "false",
		"wsl_distro": "Ubuntu", "sync.bucket": "pulling-bucket", "sync.access_key_id": "pulling-key",
	})
	image, err := OpenImage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if err := pulling.Replace(ctx, image); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"github_token": "pushed-token", "app.language": "en",
		"wsl_distro": "Ubuntu", "sync.bucket": "pulling-bucket", "sync.access_key_id": "pulling-key",
	}
	if got := all(pulling); !maps.Equal(got, want) {
		t.Fatalf("config after pull = %v, want %v", got, want)
	}
}
