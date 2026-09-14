package library_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiaot33/skills-tui/library"
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

func (m *skillsAuditMock) setRespond(respond func(*http.Request) (*http.Response, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.respond = respond
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

func TestGitHubPreviewCarriesSkillsAuditsForDisplay(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
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
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	want := []library.FetchSkill{
		{Name: "first", Path: "first", Audit: &library.SkillAudit{Availability: library.AuditAvailable, Entries: sampleAuditEntries("first")}},
		{Name: "second", Path: "second", Audit: &library.SkillAudit{Availability: library.AuditAvailable, Entries: sampleAuditEntries("second")}},
	}
	if !reflect.DeepEqual(fetch.Skills, want) {
		t.Fatalf("Fetch candidates = %#v, want %#v", fetch.Skills, want)
	}
	urls := audits.urls()
	slices.Sort(urls)
	if !reflect.DeepEqual(urls, []string{"https://skills.sh/api/v1/skills/audit/owner/repo/first", "https://skills.sh/api/v1/skills/audit/owner/repo/second"}) {
		t.Fatalf("audit URLs = %v", urls)
	}
	assertGitHubPreviewRequests(t, api, fx, "first/SKILL.md", "second/SKILL.md")

	// 列表只回放会话快照,不再访问 skills.sh。
	audits.setRespond(func(*http.Request) (*http.Response, error) {
		t.Error("listing contacted skills.sh")
		return textResponse(http.StatusInternalServerError, ""), nil
	})
	fetch.Skills[0].Audit.Entries[0].Summary = "changed by caller"
	listed, err := lib.ListFetchSkills(t.Context(), fetch.ID)
	if err != nil || !reflect.DeepEqual(listed.Skills, want) {
		t.Fatalf("ListFetchSkills = %#v, %v", listed, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("preview wrote Library: %v, %v", skills, err)
	}
}

func TestSkillsAuditOnlyCoversSelectedGitHubCandidates(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	failing := serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusOK, `{"audits":[{"provider":"Runlayer","slug":"runlayer","status":"fail","summary":"3/8 files flagged","auditedAt":"2026-09-01T00:00:00Z","riskLevel":"HIGH"}]}`), nil
	})
	lib := openTestLibrary(t)
	spec := gitHubSpec()
	spec.Names = []string{"second"}
	fetch, err := lib.Fetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if urls := failing.urls(); !reflect.DeepEqual(urls, []string{"https://skills.sh/api/v1/skills/audit/owner/repo/second"}) {
		t.Fatalf("audit URLs = %v, want only the selected candidate", urls)
	}
	if len(fetch.Skills) != 1 || fetch.Skills[0].Audit == nil || fetch.Skills[0].Audit.Entries[0].Status != library.AuditFail {
		t.Fatalf("candidates = %#v", fetch.Skills)
	}
	// 审计结论只供展示:fail 也不挡安装,入库的 Skill 不带审计。
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || len(installed) != 1 || installed[0].Name != "second" {
		t.Fatalf("install = %#v, %v", installed, err)
	}

	// URL 来源不在 skills.sh 收录范围:候选上没有审计字段,也不发请求。
	archiveSpec := mockFetchArchive(t)
	untouched := serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		t.Error("archive fetch contacted skills.sh")
		return textResponse(http.StatusInternalServerError, ""), nil
	})
	fetch, err = lib.Fetch(t.Context(), archiveSpec)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range fetch.Skills {
		if skill.Audit != nil {
			t.Fatalf("archive candidate carries audit: %#v", skill)
		}
	}
	if len(untouched.urls()) != 0 {
		t.Fatalf("archive fetch contacted skills.sh: %v", untouched.urls())
	}
}

func TestSkillsAuditAppearsOnCloneFallback(t *testing.T) {
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
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	want := &library.SkillAudit{Availability: library.AuditAvailable, Entries: sampleAuditEntries("demo")}
	if len(fetch.Skills) != 1 || !reflect.DeepEqual(fetch.Skills[0].Audit, want) {
		t.Fatalf("fallback candidates = %#v", fetch.Skills)
	}
}

func TestSkillsAuditIsBoundedInTimeAndHonorsCancel(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	fx := inspectGitHubRepo(t, repo)
	synctest.Test(t, func(t *testing.T) {
		serveGitHubAPI(t, fx)
		blockGitHubClone(t)
		serveSkillsAudit(t, func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		lib := openFetchLibrary(t, ":memory:")
		started := time.Now()
		fetch, err := lib.Fetch(t.Context(), gitHubSpec())
		if err != nil {
			t.Fatal(err)
		}
		// 审计确实等到了自身超时(而非被跳过),但不会拖过整体预算。
		if elapsed := time.Since(started); elapsed < time.Second || elapsed > 10*time.Second {
			t.Fatalf("slow skills.sh delayed Fetch by %v", elapsed)
		}
		if len(fetch.Skills) != 1 || !reflect.DeepEqual(fetch.Skills[0].Audit, &library.SkillAudit{Availability: library.AuditUnavailable}) {
			t.Fatalf("timed-out audit = %#v", fetch.Skills)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := lib.Fetch(ctx, gitHubSpec())
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	})
}

func TestSkillsAuditFailuresDegradeWithoutFailingFetch(t *testing.T) {
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
			fetch, err := lib.Fetch(t.Context(), gitHubSpec())
			if err != nil {
				t.Fatal(err)
			}
			if len(fetch.Skills) != 1 || fetch.Skills[0].Name != "demo" || !reflect.DeepEqual(fetch.Skills[0].Audit, &tc.want) {
				t.Fatalf("candidates = %#v, want audit %#v", fetch.Skills, tc.want)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("preview wrote Library: %v, %v", skills, err)
			}
		})
	}
}
