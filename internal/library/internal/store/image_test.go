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
	data, digest, err := pushing.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set(pushing, map[string]string{"sync.bucket": "another-bucket", "sync.last_pushed_at": "2026-10-26T00:00:00Z", "wsl_distro": "Debian"})
	if _, again, err := pushing.Snapshot(ctx); err != nil || again != digest {
		t.Fatalf("digest after changing machine config = %q, %v; want unchanged %q", again, err, digest)
	}
	set(pushing, map[string]string{"app.language": "zh"})
	if _, changed, err := pushing.Snapshot(ctx); err != nil || changed == digest {
		t.Fatalf("digest after changing app config = %q, %v; want a new digest", changed, err)
	}
	set(pushing, map[string]string{"app.language": "en"})
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
