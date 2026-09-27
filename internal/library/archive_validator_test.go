package library_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// URL 来源的抓取记录里多了服务端给出的 HTTP validator:Fetch / Update 成功抓取后写到 Source 上,
// 之后 CheckUpdates / Update 带条件头发请求,304 即无更新、零字节下载;预览 永远不带条件头。

var conditionalHeaders = []string{"If-None-Match", "If-Modified-Since"}

// assertConditional 断言这一批请求恰好一个,带且只带 header=want 这一个条件头。
func assertConditional(t *testing.T, requests []http.Header, header, want string) {
	t.Helper()
	request := singleRequest(t, requests)
	for _, name := range conditionalHeaders {
		expected := ""
		if name == header {
			expected = want
		}
		if got := request.Get(name); got != expected {
			t.Fatalf("%s = %q, want %s=%q only: %#v", name, got, header, want, request)
		}
	}
}

// assertUnconditional 断言这一批请求恰好一个,且不带任何条件头。
func assertUnconditional(t *testing.T, requests []http.Header) {
	t.Helper()
	request := singleRequest(t, requests)
	for _, name := range conditionalHeaders {
		if got := request.Get(name); got != "" {
			t.Fatalf("%s = %q, want no conditional headers: %#v", name, got, request)
		}
	}
}

func singleRequest(t *testing.T, requests []http.Header) http.Header {
	t.Helper()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want exactly one: %#v", len(requests), requests)
	}
	return requests[0]
}

func fetchArchive(t *testing.T, lib library.Library, spec library.PreviewFetchRequest) []library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
	if err != nil || len(fetched) == 0 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	return fetched
}

func TestURLCheckAndUpdateSendIfNoneMatchAndTreat304AsNoUpdate(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")}), `"v1"`)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	fetched := fetchArchive(t, lib, server.request())
	assertUnconditional(t, server.snapshot())
	id := fetched[0].ID

	// 入库后:检查带 If-None-Match,收到 304 → 无更新,正文一个字节都没读。
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates after fetch = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
	server.assertNoBodyRead(t)

	// 预览 必须拿到内容,永远不带条件头。
	if _, err := lib.PreviewFetch(ctx, server.request()); err != nil {
		t.Fatal(err)
	}
	assertUnconditional(t, server.snapshot())

	// 进程重启后 validator 仍在库里。
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates after reopen = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)

	// 收到 304 的 Update:不新增版本、刷新 last_fetched、validator 与 last_seen_ref 保持。
	before, err := lib.GetSkill(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.Update(ctx, id)
	if err != nil || result.Changed {
		t.Fatalf("Update on 304 = %#v, %v", result, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
	after, err := lib.GetSkill(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Source.LastFetched.After(before.Source.LastFetched) {
		t.Fatalf("last_fetched not refreshed: before %v, after %v", before.Source.LastFetched, after.Source.LastFetched)
	}
	if after.Source.LastSeenRef != before.Source.LastSeenRef {
		t.Fatalf("last_seen_ref changed on 304: %q → %q", before.Source.LastSeenRef, after.Source.LastSeenRef)
	}
	if versions, err := lib.ListVersions(ctx, id); err != nil || len(versions) != 1 {
		t.Fatalf("versions after 304 Update = %#v, %v", versions, err)
	}
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates after 304 Update = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
	server.assertNoBodyRead(t)
}

func TestURLUpdateWritesNewValidatorWhenArchiveChanges(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")}), `"v1"`)
	lib := openTestLibrary(t)
	ctx := t.Context()
	id := fetchArchive(t, lib, server.request())[0].ID
	server.snapshot()

	// 远端内容与 ETag 都变了:条件请求收到 200,按整包比较判定有更新。
	server.serve(zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")}), `"v2"`)
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || !available {
		t.Fatalf("CheckUpdates after remote change = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
	result, err := lib.Update(ctx, id)
	if err != nil || !result.Changed || string(result.Skill.Files[0].Content) != skillDocument("demo", "new") {
		t.Fatalf("Update after remote change = %#v, %v", result, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
	if versions, err := lib.ListVersions(ctx, id); err != nil || len(versions) != 2 {
		t.Fatalf("versions after changed Update = %#v, %v", versions, err)
	}
	// 新 validator 已写回:下一次检查用 "v2" 得到 304。
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates with new validator = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v2"`)
	server.assertNoBodyRead(t)
}

func TestURLWithoutValidatorNeverSendsConditionalHeaders(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := newArchiveServer(t, archive, "")
	lib := openTestLibrary(t)
	ctx := t.Context()
	id := fetchArchive(t, lib, server.request())[0].ID
	server.snapshot()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates = %v, %v", available, err)
	}
	assertUnconditional(t, server.snapshot())
	if result, err := lib.Update(ctx, id); err != nil || result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	assertUnconditional(t, server.snapshot())
	// 服务端后来开始给 ETag:Update 抓到后记录下来,之后才带条件头。
	server.serve(archive, `"late"`)
	if result, err := lib.Update(ctx, id); err != nil || result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	assertUnconditional(t, server.snapshot())
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"late"`)
}

func TestURLFallsBackToIfModifiedSinceWhenServerOnlySendsLastModified(t *testing.T) {
	const modified = "Tue, 15 Sep 2026 10:00:00 GMT"
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := newArchiveServer(t, archive, "")
	server.serveLastModified(archive, modified)
	lib := openTestLibrary(t)
	ctx := t.Context()
	id := fetchArchive(t, lib, server.request())[0].ID
	server.snapshot()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("CheckUpdates = %v, %v", available, err)
	}
	assertConditional(t, server.snapshot(), "If-Modified-Since", modified)
	server.assertNoBodyRead(t)
}

// ETag 的写法不影响回填:弱 ETag 与不合规的无引号 ETag 都原样进 If-None-Match,服务端按自己的写法比对即可 304。
func TestURLEchoesWeakAndUnquotedETagsInIfNoneMatch(t *testing.T) {
	for _, etag := range []string{`W/"weak-v1"`, `unquoted-v1`} {
		t.Run(etag, func(t *testing.T) {
			archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
			server := newArchiveServer(t, archive, etag)
			lib := openTestLibrary(t)
			ctx := t.Context()
			id := fetchArchive(t, lib, server.request())[0].ID
			server.snapshot()
			if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
				t.Fatalf("CheckUpdates = %v, %v", available, err)
			}
			assertConditional(t, server.snapshot(), "If-None-Match", etag)
			server.assertNoBodyRead(t)
		})
	}
}

// Last-Modified 不是合法 HTTP-date 时不记录:之后的请求不带条件头,与服务端不给 validator 时一致。
func TestURLIgnoresMalformedLastModified(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := newArchiveServer(t, archive, "")
	server.serveLastModified(archive, "yesterday-ish")
	lib := openTestLibrary(t)
	id := fetchArchive(t, lib, server.request())[0].ID
	server.snapshot()
	if available, err := checkSingleUpdate(t, lib, t.Context(), id); err != nil || available {
		t.Fatalf("CheckUpdates = %v, %v", available, err)
	}
	assertUnconditional(t, server.snapshot())
}

// 304 只在带了条件头的请求上有意义:预览 不带条件头却收到 304,按普通非 2xx 映射为 SourceUnreachable,不会被当成"无更新"。
func TestUnconditionalNotModifiedIsAnError(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := newArchiveServer(t, archive, "")
	lib := openTestLibrary(t)
	ctx := t.Context()
	id := fetchArchive(t, lib, server.request())[0].ID
	server.serveNotModified()
	if preview, err := lib.PreviewFetch(ctx, server.request()); !library.IsCode(err, library.SourceUnreachable) || preview.ID != "" {
		t.Fatalf("PreviewFetch on unconditional 304 = %#v, %v, want SourceUnreachable", preview, err)
	}
	// Source 没有 validator,检查也不带条件头,同样不能把 304 当无更新。
	if available, err := checkSingleUpdate(t, lib, ctx, id); !library.IsCode(err, library.SourceUnreachable) || available {
		t.Fatalf("CheckUpdates on unconditional 304 = %v, %v, want SourceUnreachable", available, err)
	}
}

func TestCheckUpdatesSendsOneConditionalRequestPerURLSource(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "old"),
	}), `"v1"`)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetched := fetchArchive(t, lib, server.request())
	server.snapshot()
	ids := skillIDs(fetched)
	results, err := lib.CheckUpdates(ctx, ids)
	if err != nil || len(results) != 2 || results[0].Available || results[1].Available || results[0].Error != nil || results[1].Error != nil {
		t.Fatalf("CheckUpdates on 304 = %#v, %v", results, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
	server.assertNoBodyRead(t)
	// 200 时仍按各 Skill 的内容分别比较。
	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "new"),
	}), `"v2"`)
	results, err = lib.CheckUpdates(ctx, ids)
	if err != nil || len(results) != 2 || results[0].Available || !results[1].Available {
		t.Fatalf("CheckUpdates on 200 = %#v, %v", results, err)
	}
	assertConditional(t, server.snapshot(), "If-None-Match", `"v1"`)
}
