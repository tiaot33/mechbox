//go:build !windows

package agentdir

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

func TestMain(m *testing.M) {
	shell := os.Getenv("AGENTDIR_TEST_SHELL")
	if shell == "" {
		shell = "sh"
	}
	wsl.Emulate(shell)
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
		if got[0].Err != nil || want[0].Err != nil || !maps.Equal(skillfile.Digests(got[0].Files), skillfile.Digests(want[0].Files)) {
			t.Errorf("ReadFolders(%s) = %v %v, want %v %v", folder, got[0].Files, got[0].Err, want[0].Files, want[0].Err)
		}
		if wantTime := want[0].Modified; !got[0].Modified.Equal(wantTime.Truncate(1e9)) && !got[0].Modified.Equal(wantTime) {
			t.Errorf("Modified(%s) = %v, want %v", folder, got[0].Modified, wantTime)
		}
		if got[0].Exists != want[0].Exists || got[0].LinkTarget != unc(want[0].LinkTarget) && want[0].LinkTarget != "" {
			t.Errorf("state(%s) = %+v, want %+v", folder, got[0], want[0])
		}
	}
	state, _ := ReadFolder(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 100, 1<<20)
	modes := map[string]skillfile.Mode{}
	for _, file := range state.Files {
		modes[file.Path] = file.Mode
	}
	if modes["scripts/run.sh"] != skillfile.ModeExecutable || modes["SKILL.md"] != skillfile.ModeRegular {
		t.Errorf("modes = %v", modes)
	}
	if _, ok := modes[".git/HEAD"]; ok {
		t.Error("ignored .git read")
	}
	if err := folderErr(ctx, unc(filepath.Join(home, ".claude/skills/dangling")), 100, 1<<20); !errors.Is(err, ErrIrregularFile) {
		t.Errorf("dangling: %v", err)
	}
	if err := folderErr(ctx, unc(filepath.Join(home, "missing")), 100, 1<<20); !errors.Is(err, ErrIrregularFile) {
		t.Errorf("missing: %v", err)
	}
	if err := folderErr(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 1, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too many files: %v", err)
	}
	write(t, filepath.Join(home, ".agents/skills/shared/.git/objects/big"), strings.Repeat("x", 4096), 0o644)
	if err := folderErr(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 100, 1024); err != nil {
		t.Errorf("ignored folders count toward the limit: %v", err)
	}
	if err := folderErr(ctx, unc(filepath.Join(home, ".claude/skills/shared")), 100, 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too many bytes: %v", err)
	}
	symlink(t, "/etc/hosts", filepath.Join(home, ".claude/skills/own/link"))
	if err := folderErr(ctx, unc(filepath.Join(home, ".claude/skills/own")), 100, 1<<20); !errors.Is(err, ErrIrregularFile) {
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
	paths := []string{filepath.Join(home, "second"), link, target, filepath.Join(home, ".claude/skills/dangling"), filepath.Join(home, "missing"), filepath.Join(home, ".claude/skills/own/SKILL.md")}
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
		if file := states[5]; !second.Directory || !linked.Directory || !real.Directory || dangling.Directory || missing.Directory || !file.Resolves || file.Directory {
			t.Errorf("Directory: %v %v %v %v %v %v", second.Directory, linked.Directory, real.Directory, dangling.Directory, missing.Directory, file.Directory)
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
	metadata := filepath.Join(home, ".agents/skills/shared/agents/openai.yaml")
	if err := os.MkdirAll(filepath.Dir(metadata), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, []byte("policy:\n  allow_implicit_invocation: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := List(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(want, func(f Folder) bool {
		return f.Name == "shared" && string(f.CodexMetadata) == "policy:\n  allow_implicit_invocation: false\n"
	}) {
		t.Fatalf("native List = %+v, want shared with agents/openai.yaml", want)
	}
	got, err := List(t.Context(), unc(directory))
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
	if missing, err := List(t.Context(), unc(filepath.Join(home, "missing"))); err != nil || missing != nil {
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
	if inside, err := Within(context.Background(), []string{unc(filepath.Join(home, ".agents"))}, []string{unc(target), unc(link)}); err != nil || !slices.Equal(inside, []bool{true, false}) {
		t.Errorf("Within compares resolved paths: %v %v", inside, err)
	}
	if inside, err := Within(context.Background(), []string{unc(filepath.Join(home, ".claude/../.agents")), filepath.Join(home, ".claude")}, []string{unc(target), target}); err != nil || !slices.Equal(inside, []bool{true, false}) {
		t.Errorf("Within with directories on both systems: %v %v", inside, err)
	}
	if got := Canonical(unc(filepath.Join(link, "new/deeper"))); got != unc(filepath.Join(target, "new/deeper")) {
		t.Errorf("Canonical = %q", got)
	}
	paths := []string{filepath.Join(link, "new/deeper"), target, filepath.Join(home, ".claude/../.claude/skills/shared"), filepath.Join(home, "missing/x")}
	var mixed []string
	for _, path := range paths {
		mixed = append(mixed, unc(path), path)
	}
	all, err := CanonicalAll(context.Background(), mixed)
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range mixed {
		if want := Canonical(path); all[i] != want {
			t.Errorf("CanonicalAll[%d] = %q, Canonical(%q) = %q", i, all[i], path, want)
		}
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
	if content, err := ReadFile(t.Context(), unc(filepath.Join(home, ".claude/skills/own/SKILL.md"))); err != nil || string(content) != "---\nname: own\n---\n" {
		t.Errorf("ReadFile = %q %v", content, err)
	}
	if _, err := ReadFile(t.Context(), unc(filepath.Join(home, "missing"))); !errors.Is(err, os.ErrNotExist) {
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
	if got, err := ReadFolder(ctx, unc(filepath.Join(skills, "new")), 10, 1<<20); err != nil || !maps.Equal(skillfile.Digests(got.Files), skillfile.Digests(files)) {
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
	if got, _ := ReadFolder(ctx, unc(filepath.Join(skills, "new")), 10, 1<<20); !maps.Equal(skillfile.Digests(got.Files), skillfile.Digests(files)) {
		t.Fatalf("rollback did not restore: %v", got.Files)
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

func folderErr(ctx context.Context, root string, maxFiles int, maxBytes int64) error {
	state, err := ReadFolder(ctx, root, maxFiles, maxBytes)
	if err != nil {
		return err
	}
	return state.Err
}

func TestDistroCanonicalStartsNoProcessForDirectoriesAndMissingPaths(t *testing.T) {
	home := npxLayout(t)
	shims := t.TempDir()
	log := filepath.Join(t.TempDir(), "commands")
	for _, command := range []string{"dirname", "basename", "readlink"} {
		real, err := exec.LookPath(command)
		if err != nil {
			t.Skip(command + " not found")
		}
		write(t, filepath.Join(shims, command), "#!/bin/sh\necho "+command+" >> '"+log+"'\nexec '"+real+"' \"$@\"\n", 0o755)
	}
	shell := filepath.Join(t.TempDir(), "sh")
	base := cmp.Or(os.Getenv("AGENTDIR_TEST_SHELL"), "sh")
	write(t, shell, "#!/bin/sh\nPATH='"+shims+"':$PATH exec "+base+"\n", 0o755)
	defer wsl.Emulate(shell)()

	var paths []string
	for i := range 200 {
		paths = append(paths, unc(filepath.Join(home, fmt.Sprintf("projects/p%d/.agent-%d/skills", i%20, i))))
	}
	paths = append(paths, unc(filepath.Join(home, ".claude/skills/shared")), unc(filepath.Join(home, ".claude/skills")))
	canonical, err := CanonicalAll(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if want := unc(filepath.Join(home, "projects/p3/.agent-3/skills")); canonical[3] != want {
		t.Errorf("canonical[3] = %q, want %q", canonical[3], want)
	}
	if want := unc(filepath.Join(home, ".agents/skills/shared")); canonical[200] != want {
		t.Errorf("canonical link = %q, want %q", canonical[200], want)
	}
	if started, err := os.ReadFile(log); err == nil {
		t.Errorf("canonicalizing directories and missing paths started processes:\n%s", started)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CanonicalAll(ctx, paths); !errors.Is(err, context.Canceled) {
		t.Errorf("CanonicalAll after cancel = %v", err)
	}
	if _, err := Within(ctx, paths, []string{unc(filepath.Join(home, ".agents/skills/shared"))}); !errors.Is(err, context.Canceled) {
		t.Errorf("Within after cancel = %v", err)
	}
}

func TestDistroListAllListsEveryDirectoryInOneCommandAndStopsWhenCancelled(t *testing.T) {
	home := npxLayout(t)
	metadata := filepath.Join(home, ".agents/skills/shared/agents/openai.yaml")
	write(t, metadata, "policy:\n  allow_implicit_invocation: false\n", 0o644)
	directories := []string{filepath.Join(home, ".claude/skills"), filepath.Join(home, "missing"), filepath.Join(home, ".agents/skills")}
	var paths []string
	for _, directory := range directories {
		paths = append(paths, unc(directory))
	}
	listed, err := ListAll(t.Context(), paths)
	if err != nil {
		t.Fatal(err)
	}
	for i, directory := range directories {
		want, err := List(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		for j := range want {
			want[j].Path = unc(want[j].Path)
			if want[j].Target != "" {
				want[j].Target = unc(want[j].Target)
			}
		}
		if listed[i].Err != nil || !reflect.DeepEqual(listed[i].Folders, want) {
			t.Errorf("ListAll[%d] = %+v, %v; want %+v", i, listed[i].Folders, listed[i].Err, want)
		}
	}

	shims := t.TempDir()
	real, err := exec.LookPath("wc")
	if err != nil {
		t.Skip("wc not found")
	}
	write(t, filepath.Join(shims, "wc"), "#!/bin/sh\nsleep 1\nexec '"+real+"' \"$@\"\n", 0o755)
	shell := filepath.Join(t.TempDir(), "sh")
	write(t, shell, "#!/bin/sh\nPATH='"+shims+"':$PATH exec "+cmp.Or(os.Getenv("AGENTDIR_TEST_SHELL"), "sh")+"\n", 0o755)
	defer wsl.Emulate(shell)()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ListAll(ctx, paths); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ListAll cancelled while listing = %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("ListAll returned %v after being cancelled at 300ms", took)
	}
}

func TestDistroCanonicalStopsAtTheRootWhenNothingResolves(t *testing.T) {
	home := npxLayout(t)
	shims := t.TempDir()
	write(t, filepath.Join(shims, "readlink"), "#!/bin/sh\nexit 1\n", 0o755)
	shell := filepath.Join(t.TempDir(), "sh")
	base := cmp.Or(os.Getenv("AGENTDIR_TEST_SHELL"), "sh")
	write(t, shell, "#!/bin/sh\n{ echo 'cd() { return 1; }'; cat; } | PATH='"+shims+"':$PATH "+base+"\n", 0o755)
	defer wsl.Emulate(shell)()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(home, ".claude/skills")
	canonical, err := CanonicalAll(ctx, []string{unc(path)})
	if err != nil || canonical[0] != unc(path) {
		t.Fatalf("CanonicalAll with nothing resolving = %q, %v; want %q", canonical, err, unc(path))
	}
}
