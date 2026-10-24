//go:build !windows

package agentdir

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

func TestMain(m *testing.M) {
	shell := os.Getenv("AGENTDIR_TEST_SHELL")
	if shell == "" {
		shell = "sh"
	}
	os.Setenv("MECHBOX_TEST_WSL_SHELL", shell)
	os.Exit(m.Run())
}

func unc(path string) string {
	return wsl.Join("test", path)
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func npxLayout(t *testing.T) string {
	t.Helper()
	home := tempDir(t)
	write(t, filepath.Join(home, ".agents/skills/shared/SKILL.md"), "---\nname: shared\n---\n", 0o644)
	write(t, filepath.Join(home, ".agents/skills/shared/scripts/run.sh"), "#!/bin/sh\necho hi\n", 0o755)
	write(t, filepath.Join(home, ".agents/skills/shared/.git/HEAD"), "ref", 0o644)
	symlink(t, "../../.agents/skills/shared", filepath.Join(home, ".claude/skills/shared"))
	write(t, filepath.Join(home, ".claude/skills/own/SKILL.md"), "---\nname: own\n---\n", 0o644)
	write(t, filepath.Join(home, ".claude/skills/.hidden/SKILL.md"), "x", 0o644)
	write(t, filepath.Join(home, ".claude/skills/notes/README.md"), "x", 0o644)
	symlink(t, "../../nowhere", filepath.Join(home, ".claude/skills/dangling"))
	return home
}

func TestDistroReadMatchesNative(t *testing.T) {
	home := npxLayout(t)
	ctx := context.Background()
	for _, folder := range []string{".claude/skills/shared", ".claude/skills/own", ".agents/skills/shared"} {
		local := filepath.Join(home, folder)
		want, got := readFolders(t, local), readFolders(t, unc(local))
		if got[0].Err != nil || want[0].Err != nil || !skillfile.Equal(got[0].Files, want[0].Files) {
			t.Errorf("ReadFolders(%s) = %v %v, want %v %v", folder, got[0].Files, got[0].Err, want[0].Files, want[0].Err)
		}
		if wantTime := want[0].Modified; !got[0].Modified.Equal(wantTime.Truncate(1e9)) && !got[0].Modified.Equal(wantTime) {
			t.Errorf("Modified(%s) = %v, want %v", folder, got[0].Modified, wantTime)
		}
		if got[0].Exists != want[0].Exists || got[0].LinkTarget != unc(want[0].LinkTarget) && want[0].LinkTarget != "" {
			t.Errorf("state(%s) = %+v, want %+v", folder, got[0], want[0])
		}
	}
	files, _ := ReadFiles(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 100, 1<<20)
	modes := map[string]skillfile.Mode{}
	for _, file := range files {
		modes[file.Path] = file.Mode
	}
	if modes["scripts/run.sh"] != skillfile.ModeExecutable || modes["SKILL.md"] != skillfile.ModeRegular {
		t.Errorf("modes = %v", modes)
	}
	if _, ok := modes[".git/HEAD"]; ok {
		t.Error("ignored .git read")
	}
	if _, err := ReadFiles(ctx, unc(filepath.Join(home, ".claude/skills/dangling")), 100, 1<<20); !errors.Is(err, ErrIrregularFile) {
		t.Errorf("dangling: %v", err)
	}
	if _, err := ReadFiles(ctx, unc(filepath.Join(home, "missing")), 100, 1<<20); !errors.Is(err, ErrIrregularFile) {
		t.Errorf("missing: %v", err)
	}
	if _, err := ReadFiles(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 1, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too many files: %v", err)
	}
	write(t, filepath.Join(home, ".agents/skills/shared/.git/objects/big"), strings.Repeat("x", 4096), 0o644)
	if _, err := ReadFiles(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 100, 1024); err != nil {
		t.Errorf("ignored folders count toward the limit: %v", err)
	}
	if _, err := ReadFiles(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 100, 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too many bytes: %v", err)
	}
	symlink(t, "/etc/hosts", filepath.Join(home, ".claude/skills/own/link"))
	if _, err := ReadFiles(ctx, unc(filepath.Join(home, ".claude/skills/own")), 100, 1<<20); !errors.Is(err, ErrIrregularFile) {
		t.Errorf("inner symlink: %v", err)
	}
}

func readFolders(t *testing.T, folders ...string) []FolderState {
	t.Helper()
	states := make([]FolderState, len(folders))
	err := ReadFolders(context.Background(), folders, 10, 64<<20, func(index int, state FolderState) error {
		states[index] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return states
}

func TestDistroReadFoldersInBatches(t *testing.T) {
	home := tempDir(t)
	var folders []string
	for i := range 5 {
		folder := filepath.Join(home, "skills", strconv.Itoa(i))
		write(t, filepath.Join(folder, "SKILL.md"), strings.Repeat(strconv.Itoa(i), 30<<20), 0o644)
		folders = append(folders, unc(folder))
	}
	folders = append(folders, unc(filepath.Join(home, "skills/missing")))
	states := readFolders(t, folders...)
	for i, state := range states[:5] {
		if state.Err != nil || len(state.Files) != 1 || len(state.Files[0].Content) != 30<<20 || state.Files[0].Content[0] != byte('0'+i) {
			t.Errorf("folder %d: %v", i, state.Err)
		}
	}
	if missing := states[5]; missing.Exists || !errors.Is(missing.Err, ErrIrregularFile) {
		t.Errorf("missing = %+v", missing)
	}
}

func TestDistroStatFoldersFollowsLinks(t *testing.T) {
	home := npxLayout(t)
	link := filepath.Join(home, ".claude/skills/shared")
	target := filepath.Join(home, ".agents/skills/shared")
	symlink(t, link, filepath.Join(home, "second"))
	paths := []string{filepath.Join(home, "second"), link, target, filepath.Join(home, ".claude/skills/dangling"), filepath.Join(home, "missing")}
	for _, convert := range []func(string) string{func(p string) string { return p }, unc} {
		var folders []string
		for _, p := range paths {
			folders = append(folders, convert(p))
		}
		states, err := StatFolders(context.Background(), folders)
		if err != nil {
			t.Fatal(err)
		}
		second, linked, real, dangling, missing := states[0], states[1], states[2], states[3], states[4]
		if second.LinkTarget != convert(target) || linked.LinkTarget != convert(target) || real.LinkTarget != "" {
			t.Errorf("link targets: %q %q %q", second.LinkTarget, linked.LinkTarget, real.LinkTarget)
		}
		if dangling.LinkTarget != convert(filepath.Join(home, "nowhere")) || !dangling.Exists || dangling.Resolves || missing.Exists || !second.Resolves {
			t.Errorf("dangling %+v, missing %+v", dangling, missing)
		}
		if !second.LinksThrough(linked) || !second.LinksThrough(real) || linked.LinksThrough(second) || real.LinksThrough(linked) {
			t.Error("LinksThrough follows the chain")
		}
		if linked.SameEntry(real) || !linked.SameEntry(linked) {
			t.Error("SameEntry compares the entries, not where they lead")
		}
	}
}

func TestDistroListMatchesNative(t *testing.T) {
	home := npxLayout(t)
	directory := filepath.Join(home, ".claude/skills")
	want, err := List(directory)
	if err != nil {
		t.Fatal(err)
	}
	got, err := List(unc(directory))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("List = %+v, want %+v", got, want)
	}
	for i := range want {
		w := want[i]
		w.Path = unc(w.Path)
		if w.Target != "" {
			w.Target = unc(w.Target)
		}
		if !reflect.DeepEqual(got[i], w) {
			t.Errorf("folder %d = %+v, want %+v", i, got[i], w)
		}
	}
	if missing, err := List(unc(filepath.Join(home, "missing"))); err != nil || missing != nil {
		t.Errorf("missing directory: %v %v", missing, err)
	}
}

func TestDistroPathQueries(t *testing.T) {
	home := npxLayout(t)
	link := filepath.Join(home, ".claude/skills/shared")
	target := filepath.Join(home, ".agents/skills/shared")
	if got := LinkTarget(unc(link)); got != unc(target) {
		t.Errorf("LinkTarget = %q", got)
	}
	if got := LinkTarget(unc(target)); got != "" {
		t.Errorf("LinkTarget(not a link) = %q", got)
	}
	if got := LinkTarget(unc(filepath.Join(home, ".claude/skills/dangling"))); got != unc(filepath.Join(home, "nowhere")) {
		t.Errorf("LinkTarget(dangling) = %q", got)
	}
	symlink(t, link, filepath.Join(home, "second"))
	if got := LinkChain(unc(filepath.Join(home, "second"))); !slices.Equal(got, []string{unc(link), unc(target)}) {
		t.Errorf("LinkChain = %q", got)
	}
	for path, want := range map[string]bool{link: true, filepath.Join(home, ".claude/skills/dangling"): true, filepath.Join(home, "missing"): false} {
		if got, err := Exists(unc(path)); err != nil || got != want {
			t.Errorf("Exists(%s) = %v %v", path, got, err)
		}
	}
	if ok, err := DirectoryExists(unc(link)); err != nil || !ok {
		t.Errorf("DirectoryExists(link) = %v %v", ok, err)
	}
	if ok, err := DirectoryExists(unc(filepath.Join(home, ".claude/skills/own/SKILL.md"))); err != nil || ok {
		t.Errorf("DirectoryExists(file) = %v %v", ok, err)
	}
	if Resolves(unc(filepath.Join(home, ".claude/skills/dangling"))) || !Resolves(unc(link)) {
		t.Error("Resolves")
	}
	if !SameFolder(unc(link), unc(target)) || SameEntry(unc(link), unc(target)) {
		t.Error("link and target: same folder but not the same entry")
	}
	if !SameEntry(unc(filepath.Join(home, ".claude/../.claude/skills/shared")), unc(link)) {
		t.Error("SameEntry through ..")
	}
	if within := Within([]string{unc(filepath.Join(home, ".agents"))}); !within(unc(target)) || within(unc(link)) {
		t.Error("Within compares resolved paths")
	}
	if within := Within([]string{unc(filepath.Join(home, ".claude/../.agents")), filepath.Join(home, ".claude")}); !within(unc(target)) || within(target) {
		t.Error("Within with directories on both systems")
	}
	if got := Canonical(unc(filepath.Join(link, "new/deeper"))); got != unc(filepath.Join(target, "new/deeper")) {
		t.Errorf("Canonical = %q", got)
	}
	if !Inside(unc(home), unc(filepath.Join(link, "x"))) || Inside(unc(filepath.Join(home, ".claude")), unc(filepath.Join(link, "x"))) {
		t.Error("Inside compares canonical paths")
	}
	if SameFolder(unc(target), target) || Inside(home, unc(target)) {
		t.Error("paths on different systems are never the same")
	}
	if root, err := ProjectRoot(unc(filepath.Join(home, ".claude/skills/shared"))); err != nil || root != unc(target) {
		t.Errorf("ProjectRoot = %q %v", root, err)
	}
	if _, err := ProjectRoot(unc(filepath.Join(home, "missing"))); !errors.Is(err, ErrMissing) {
		t.Errorf("ProjectRoot(missing) = %v", err)
	}
	if _, err := ProjectRoot(unc(filepath.Join(home, ".claude/skills/own/SKILL.md"))); !errors.Is(err, ErrNotDirectory) {
		t.Errorf("ProjectRoot(file) = %v", err)
	}
	if content, err := ReadFile(unc(filepath.Join(home, ".claude/skills/own/SKILL.md"))); err != nil || string(content) != "---\nname: own\n---\n" {
		t.Errorf("ReadFile = %q %v", content, err)
	}
	if _, err := ReadFile(unc(filepath.Join(home, "missing"))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile(missing) = %v", err)
	}
}

func TestDistroHome(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	resolved, err := DistroHome(context.Background(), "home")
	if err != nil {
		t.Fatal(err)
	}
	unc := func(path string) string { return wsl.Join("home", path) }
	if resolved.Root() != unc(home) {
		t.Fatalf("Root = %q", resolved.Root())
	}
	if got := resolved.Join(".claude/skills"); got != unc(filepath.Join(home, ".claude/skills")) {
		t.Errorf("Join = %q", got)
	}
	for input, want := range map[string]string{
		"~/.foo/skills":                    ".foo/skills",
		unc(filepath.Join(home, ".bar/x")): ".bar/x",
	} {
		if got, err := resolved.Relative(input); err != nil || got != want {
			t.Errorf("Relative(%q) = %q %v", input, got, err)
		}
	}
	if _, err := resolved.Relative(filepath.Join(home, ".bar")); !errors.Is(err, ErrOutside) && !errors.Is(err, ErrNotAbsolute) {
		t.Errorf("Relative(native path) = %v", err)
	}
	if _, err := resolved.Relative(unc("/elsewhere")); !errors.Is(err, ErrOutside) {
		t.Errorf("Relative(outside) = %v", err)
	}
}

func TestDistroChange(t *testing.T) {
	home := npxLayout(t)
	ctx := context.Background()
	skills := filepath.Join(home, ".codex/skills")
	files := []skillfile.File{
		{Path: "SKILL.md", Content: []byte("---\nname: new\n---\n"), Mode: skillfile.ModeRegular},
		{Path: "scripts/go.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeExecutable},
	}

	var change Change
	if err := change.Stage(ctx, unc(skills), "new", files); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	change.Done()
	info, err := os.Stat(filepath.Join(skills, "new/scripts/go.sh"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable not kept: %v %v", info, err)
	}
	if got, err := ReadFiles(ctx, unc(filepath.Join(skills, "new")), 10, 1<<20); err != nil || !skillfile.Equal(got, files) {
		t.Fatalf("written files = %v %v", got, err)
	}
	if entries, _ := os.ReadDir(skills); len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}

	replaced := []skillfile.File{{Path: "SKILL.md", Content: []byte("v2"), Mode: skillfile.ModeRegular}}
	change = Change{}
	if err := change.Stage(ctx, unc(skills), "new", replaced); err != nil {
		t.Fatal(err)
	}
	change.Remove(unc(filepath.Join(home, ".claude/skills/own")))
	if err := change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := change.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadFiles(ctx, unc(filepath.Join(skills, "new")), 10, 1<<20); !skillfile.Equal(got, files) {
		t.Fatalf("rollback did not restore: %v", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/own/SKILL.md")); err != nil {
		t.Fatalf("rollback did not restore removal: %v", err)
	}
	if entries, _ := os.ReadDir(skills); len(entries) != 1 {
		t.Fatalf("leftovers after rollback: %v", entries)
	}

	change = Change{}
	if err := change.Copy(ctx, unc(filepath.Join(home, ".claude/skills/shared")), unc(skills), "new"); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(ctx); !errors.As(err, new(*TargetExistsError)) {
		t.Fatalf("copy onto existing folder: %v", err)
	}
	if err := change.Rollback(); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(home, ".claude/skills/shared")
	change = Change{}
	if err := change.Detach(ctx, unc(link)); err != nil {
		t.Fatal(err)
	}
	change.Remove(unc(filepath.Join(home, ".claude/skills/dangling")))
	if err := change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	change.Done()
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("detached folder is still a link: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(link, "scripts/run.sh")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("detached copy lost the executable bit: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills/dangling")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed link still there: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".claude/skills")); len(entries) != 4 {
		t.Fatalf("leftovers after detach: %v", entries)
	}
	if err := (&Change{}).Copy(ctx, link, unc(skills), "x"); err == nil {
		t.Fatal("copy across systems succeeded")
	}
}

func TestDistroCommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempDir(t)
	write(t, filepath.Join(root, ".claude/skills/tracked/SKILL.md"), "x", 0o644)
	write(t, filepath.Join(root, ".claude/skills/untracked/SKILL.md"), "x", 0o644)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", ".claude/skills/tracked"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"},
	} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	got, err := Committed(context.Background(), unc(root), ".claude/skills")
	if err != nil || !reflect.DeepEqual(got, map[string]bool{"tracked": true}) {
		t.Fatalf("Committed = %v %v", got, err)
	}
}
