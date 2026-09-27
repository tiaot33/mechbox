package library_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/tiaot33/skills-tui/internal/library"
)

// holdDirectTransaction 用直连模拟"另一个进程"在库上开着一个事务(begin 为 BEGIN 或 BEGIN IMMEDIATE),
// 返回结束它的函数。读事务在 BEGIN 后跟一条 SELECT 才真正持有读锁。
func holdDirectTransaction(t *testing.T, path, begin string) func() {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), begin); err != nil {
		t.Fatal(err)
	}
	var agents int
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM agents`).Scan(&agents); err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() {
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		_ = conn.Close()
		_ = db.Close()
	})
	t.Cleanup(release)
	return release
}

// 另一进程正在长读(TUI 遍历一个大 skill 的文件)时,本进程的写事务照常提交,而不是在提交阶段等它读完。
// rollback journal 下这会等满 busy_timeout 后失败;WAL 下读者永不阻塞写者。
func TestFetchCommitsWhileAnotherProcessHoldsLongRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	release := holdDirectTransaction(t, path, `BEGIN`)
	defer release()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "# example\n")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	skill, err := fetchLocal(ctx, lib, source)
	if err != nil {
		t.Fatalf("Fetch while another process reads = %v, want success", err)
	}
	release()
	skills, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].ID != skill.ID {
		t.Fatalf("ListSkills after install = %v, want the fetched skill", summaryIDs(skills))
	}
}

// 另一进程占着写锁超过等待上限时,写操作返回可重试的 Busy 而不是 Internal;底层 SQLite 错误仍可经 errors 取到。
// 这条要等满 5 s 的 busy_timeout,接受它慢。
func TestWriteReportsBusyWhenAnotherProcessHoldsWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	release := holdDirectTransaction(t, path, `BEGIN IMMEDIATE`)
	defer release()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "# example\n")
	started := time.Now()
	_, err = fetchLocal(t.Context(), lib, source)
	typed := requireLibraryError(t, err, library.Busy)
	if library.IsCode(err, library.Internal) {
		t.Fatalf("busy error also reported as internal: %v", typed)
	}
	sqliteErr, ok := errors.AsType[sqlite3.Error](err)
	if !ok || sqliteErr.Code != sqlite3.ErrBusy {
		t.Fatalf("underlying error = %v, want sqlite3.ErrBusy reachable via errors.AsType", err)
	}
	if waited := time.Since(started); waited < 4*time.Second {
		t.Fatalf("Fetch gave up after %v, want it to wait for busy_timeout first", waited)
	}
	// 对方释放后同一操作直接成功:Busy 确实只是"稍后重试"。
	release()
	if _, err := fetchLocal(t.Context(), lib, source); err != nil {
		t.Fatalf("Fetch after the other writer released = %v, want success", err)
	}
}

// 两个 Library 实例(等价于 TUI 与桌面端两个进程——SQLite 的文件锁按连接生效)并发混合读写若干轮,
// 全部成功、无任何错误码;结束后两边看到的 skill 列表一致。
func TestTwoLibraryInstancesInterleaveReadsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	var instances [2]library.Library
	for i := range instances {
		lib, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer lib.Close()
		instances[i] = lib
	}
	const rounds = 20
	// 来源目录在主 goroutine 里准备好:并发 goroutine 内不能调 t.Fatal。
	var sources [2][rounds]string
	for i := range instances {
		for round := range rounds {
			name := fmt.Sprintf("instance%d-round%d", i, round)
			sources[i][round] = filepath.Join(t.TempDir(), name)
			writeLocalSkill(t, sources[i][round], "# "+name+"\n")
		}
	}
	failures := make(chan error, 2*rounds*3)
	var wg sync.WaitGroup
	for i, lib := range instances {
		wg.Go(func() {
			for round := range rounds {
				name := filepath.Base(sources[i][round])
				skill, err := fetchLocal(t.Context(), lib, sources[i][round])
				if err != nil {
					failures <- fmt.Errorf("%s fetch: %w", name, err)
					continue
				}
				if _, err := lib.ListSkills(t.Context()); err != nil {
					failures <- fmt.Errorf("%s list: %w", name, err)
				}
				if round%2 == 0 {
					if err := lib.RemoveSkill(t.Context(), skill.ID, library.RemoveSkillOptions{}); err != nil {
						failures <- fmt.Errorf("%s remove: %w", name, err)
					}
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var seen [2][]library.SkillID
	for i, lib := range instances {
		skills, err := lib.ListSkills(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		seen[i] = summaryIDs(skills)
	}
	if len(seen[0]) != rounds || !slices.Equal(seen[0], seen[1]) {
		t.Fatalf("instances disagree after interleaved writes: %v vs %v (want %d shared skills)", seen[0], seen[1], rounds)
	}
}

// Open 在确认这是本工具的库之后把它切到 WAL;该模式持久化在文件里,关闭再打开仍是 WAL。
func TestOpenSwitchesLibraryToWALAndKeepsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	if mode := journalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode after first Open = %q, want wal", mode)
	}
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if mode := journalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode after reopen = %q, want wal", mode)
	}
}
