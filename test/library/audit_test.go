package library_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// skillsAuditMock 把发往 skills.sh 的请求从当前 Transport 分流出来,其余请求原样透传。
type skillsAuditMock struct {
	mu       sync.Mutex
	requests []*http.Request
	respond  func(*http.Request) (*http.Response, error)
}

func serveSkillsAudit(t *testing.T, respond func(*http.Request) (*http.Response, error)) *skillsAuditMock {
	t.Helper()
	mock := &skillsAuditMock{respond: respond}
	client := http.DefaultClient
	previous := client.Transport
	if previous == nil {
		previous = http.DefaultTransport
	}
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "skills.sh" {
			return previous.RoundTrip(r)
		}
		mock.mu.Lock()
		mock.requests = append(mock.requests, r)
		respond := mock.respond
		mock.mu.Unlock()
		return respond(r)
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	return mock
}

func (m *skillsAuditMock) urls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	urls := make([]string, len(m.requests))
	for i, r := range m.requests {
		urls[i] = r.URL.String()
	}
	return urls
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

const sampleAuditBody = `{"id":"owner/repo/first","source":"owner/repo","slug":"first","audits":[
{"provider":"Gen Agent Trust Hub","slug":"agent-trust-hub","status":"pass","summary":"No issues","auditedAt":"2026-09-01T10:00:00Z","riskLevel":"SAFE","categories":["COMMAND_EXECUTION","PROMPT_INJECTION"]},
{"provider":"Socket","slug":"socket","status":"pass","summary":"No alerts","auditedAt":"2026-09-02T00:00:00Z"},
{"provider":"Snyk","slug":"snyk","status":"warn","summary":"Risk: MEDIUM · 1 issue","auditedAt":"2026-09-03T08:15:30.123456+00:00","riskLevel":"MEDIUM"}
]}`

func sampleAuditEntries(name string) []library.AuditEntry {
	return []library.AuditEntry{
		{Provider: "Gen Agent Trust Hub", Slug: "agent-trust-hub", Status: library.AuditPass, Summary: "No issues", AuditedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), RiskLevel: "SAFE", Categories: []string{"COMMAND_EXECUTION", "PROMPT_INJECTION"}, DetailURL: "https://skills.sh/owner/repo/" + name + "/security/agent-trust-hub"},
		{Provider: "Socket", Slug: "socket", Status: library.AuditPass, Summary: "No alerts", AuditedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), DetailURL: "https://skills.sh/owner/repo/" + name + "/security/socket"},
		{Provider: "Snyk", Slug: "snyk", Status: library.AuditWarn, Summary: "Risk: MEDIUM · 1 issue", AuditedAt: time.Date(2026, 9, 3, 8, 15, 30, 123456000, time.UTC), RiskLevel: "MEDIUM", DetailURL: "https://skills.sh/owner/repo/" + name + "/security/snyk"},
	}
}

func TestGetSkillAuditLoadsOnDemandAndCaches(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	audits := serveSkillsAudit(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Errorf("audit request = %s with Authorization %q", r.Method, r.Header.Get("Authorization"))
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("audit request lacks deadline")
		}
		return textResponse(http.StatusOK, sampleAuditBody), nil
	})

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	// 入库预览不查审计,候选列表也不带审计。
	if urls := audits.urls(); len(urls) != 0 {
		t.Fatalf("PreviewFetch contacted skills.sh: %v", urls)
	}
	want := &library.SkillAudit{Availability: library.AuditAvailable, Entries: sampleAuditEntries("first")}
	for range 2 {
		got, err := lib.GetSkillAudit(t.Context(), preview.ID, "first")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("GetSkillAudit = %#v, %v; want %#v", got, err, want)
		}
		// 返回值是副本,改动不影响入库预览里的缓存。
		got.Entries[0].Categories[0] = "mutated"
	}
	if urls := audits.urls(); !reflect.DeepEqual(urls, []string{"https://skills.sh/api/v1/skills/audit/owner/repo/first"}) {
		t.Fatalf("audit URLs = %v, want one lookup for the viewed candidate", urls)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("audit wrote Library: %v, %v", skills, err)
	}
}

func TestGetSkillAuditValidatesHandleAndCandidate(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	blockGitHubClone(t)
	audits := serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusOK, `{"audits":[{"provider":"Runlayer","slug":"runlayer","status":"fail","summary":"3/8 files flagged","auditedAt":"2026-09-01T00:00:00Z","riskLevel":"HIGH"}]}`), nil
	})
	lib := openTestLibrary(t)
	spec := gitHubRequest()
	spec.GitHub.Input = "owner/repo@second"
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   library.FetchPreviewID
		name string
		code library.ErrorCode
	}{
		{"unknown", "second", library.NotFound},
		{preview.ID, "first", library.NotFound}, // 被 Names 筛掉的候选不在入库预览里
		{preview.ID, "../x", library.InvalidArgument},
	} {
		if _, err := lib.GetSkillAudit(t.Context(), tc.id, tc.name); !library.IsCode(err, tc.code) {
			t.Fatalf("GetSkillAudit(%q, %q) = %v, want %s", tc.id, tc.name, err, tc.code)
		}
	}
	if urls := audits.urls(); len(urls) != 0 {
		t.Fatalf("invalid lookups contacted skills.sh: %v", urls)
	}
	audit, err := lib.GetSkillAudit(t.Context(), preview.ID, "second")
	if err != nil || audit == nil || audit.Entries[0].Status != library.AuditFail {
		t.Fatalf("GetSkillAudit = %#v, %v", audit, err)
	}
	// 审计结论只供展示:fail 也不挡入库;入库后的 Skill、列表与版本历史都没有审计的落脚点。
	fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 1 || fetched[0].Name != "second" {
		t.Fatalf("fetch = %#v, %v", fetched, err)
	}
	skill, err := lib.GetSkill(t.Context(), fetched[0].ID)
	if err != nil || !reflect.DeepEqual(skill, fetched[0]) {
		t.Fatalf("GetSkill = %#v, %v", skill, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || !reflect.DeepEqual(skills, fetched) {
		t.Fatalf("ListSkills = %#v, %v", skills, err)
	}
	if versions, err := lib.ListVersions(t.Context(), fetched[0].ID); err != nil || len(versions) != 1 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	// 入库成功后句柄失效。
	if _, err := lib.GetSkillAudit(t.Context(), preview.ID, "second"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("audit after fetch = %v, want NotFound", err)
	}

	// URL 来源不在 skills.sh 收录范围:返回 nil,也不发请求。
	archiveSpec := mockPreviewArchive(t)
	untouched := serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		t.Error("archive audit contacted skills.sh")
		return textResponse(http.StatusInternalServerError, ""), nil
	})
	preview, err = lib.PreviewFetch(t.Context(), archiveSpec)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range preview.Candidates {
		if audit, err := lib.GetSkillAudit(t.Context(), preview.ID, skill.Name); err != nil || audit != nil {
			t.Fatalf("URL audit = %#v, %v; want nil", audit, err)
		}
	}
	if len(untouched.urls()) != 0 {
		t.Fatalf("archive audit contacted skills.sh: %v", untouched.urls())
	}
}

func TestGetSkillAuditWorksOnCloneFallback(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	redirectGitHubClone(t, repo)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusInternalServerError, "boom"), nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusOK, sampleAuditBody), nil
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := &library.SkillAudit{Availability: library.AuditAvailable, Entries: sampleAuditEntries("demo")}
	if got, err := lib.GetSkillAudit(t.Context(), preview.ID, "demo"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback audit = %#v, %v", got, err)
	}
}

func TestGetSkillAuditIsBoundedInTimeAndHonorsCancel(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	fx := inspectGitHubRepo(t, repo)
	synctest.Test(t, func(t *testing.T) {
		serveGitHubAPI(t, fx)
		blockGitHubClone(t)
		audits := serveSkillsAudit(t, func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		lib := openLibraryAt(t, ":memory:")
		preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		got, err := lib.GetSkillAudit(t.Context(), preview.ID, "demo")
		if err != nil || !reflect.DeepEqual(got, &library.SkillAudit{Availability: library.AuditUnavailable}) {
			t.Fatalf("timed-out audit = %#v, %v", got, err)
		}
		// 审计确实等到了自身超时,且不会无限拖住调用方。
		if elapsed := time.Since(started); elapsed < time.Second || elapsed > 10*time.Second {
			t.Fatalf("slow skills.sh took %v", elapsed)
		}
		// 不可用不缓存:再次查看会重查。
		if _, err := lib.GetSkillAudit(t.Context(), preview.ID, "demo"); err != nil || len(audits.urls()) != 2 {
			t.Fatalf("unavailable audit was cached: %v, %v", audits.urls(), err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := lib.GetSkillAudit(ctx, preview.ID, "demo")
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	})
}

func TestGetSkillAuditFailuresDegradeToAvailability(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	fx := inspectGitHubRepo(t, repo)
	for _, tc := range []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
		want    library.SkillAudit
	}{
		{"not audited", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusNotFound, `{"error":"not_found","message":"No security audits found for this skill."}`), nil
		}, library.SkillAudit{Availability: library.AuditNone}},
		{"empty list", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusOK, `{"id":"owner/repo/demo","audits":[]}`), nil
		}, library.SkillAudit{Availability: library.AuditNone}},
		{"server error", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusInternalServerError, ""), nil
		}, library.SkillAudit{Availability: library.AuditUnavailable}},
		{"bad request", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusBadRequest, `{"error":"invalid_path"}`), nil
		}, library.SkillAudit{Availability: library.AuditUnavailable}},
		{"invalid json", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusOK, `broken`), nil
		}, library.SkillAudit{Availability: library.AuditUnavailable}},
		{"missing list", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusOK, `{}`), nil
		}, library.SkillAudit{Availability: library.AuditUnavailable}},
		{"oversized", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusOK, `{"audits":[{"provider":"`+strings.Repeat("x", 1<<20)+`"}]}`), nil
		}, library.SkillAudit{Availability: library.AuditUnavailable}},
		{"offline", func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		}, library.SkillAudit{Availability: library.AuditUnavailable}},
		{"lenient entries", func(*http.Request) (*http.Response, error) {
			return textResponse(http.StatusOK, `{"audits":[
{"provider":"Socket","slug":"socket","status":"pass","summary":"No alerts","auditedAt":"garbage"},
{"slug":"missing-provider","status":"pass","summary":"dropped","auditedAt":"2026-09-01T00:00:00Z"},
{"provider":"Runlayer","slug":"runlayer","status":"mystery","summary":"Unknown verdict","auditedAt":"2026-09-01T00:00:00Z","riskLevel":"NONE"}
]}`), nil
		}, library.SkillAudit{Availability: library.AuditAvailable, Entries: []library.AuditEntry{
			{Provider: "Socket", Slug: "socket", Status: library.AuditPass, Summary: "No alerts", DetailURL: "https://skills.sh/owner/repo/demo/security/socket"},
			{Provider: "Runlayer", Slug: "runlayer", Status: "mystery", Summary: "Unknown verdict", AuditedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), RiskLevel: "NONE", DetailURL: "https://skills.sh/owner/repo/demo/security/runlayer"},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveGitHubAPI(t, fx)
			blockGitHubClone(t)
			serveSkillsAudit(t, tc.respond)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			got, err := lib.GetSkillAudit(t.Context(), preview.ID, "demo")
			if err != nil || !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("audit = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}
