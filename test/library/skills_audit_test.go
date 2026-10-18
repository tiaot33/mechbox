package library_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestGetSkillAuditLooksUpLibrarySkillBySourceAndName(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	blockGitHubClone(t)
	available := true
	audits := serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		if !available {
			return textResponse(http.StatusInternalServerError, ""), nil
		}
		return textResponse(http.StatusOK, sampleAuditBody), nil
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"first"})
	if err != nil || len(fetched) != 1 {
		t.Fatalf("fetch = %#v, %v", fetched, err)
	}
	if urls := audits.urls(); len(urls) != 0 {
		t.Fatalf("fetch contacted skills.sh: %v", urls)
	}

	want := &library.SkillAudit{Availability: library.AuditAvailable, Entries: sampleAuditEntries("first")}
	for range 2 {
		got, err := lib.GetSkillAudit(t.Context(), fetched[0].ID)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("GetSkillAudit = %#v, %v; want %#v", got, err, want)
		}
	}
	// 不缓存:每次查看技能详情都重查。
	wantURL := "https://skills.sh/api/v1/skills/audit/owner/repo/first"
	if urls := audits.urls(); !reflect.DeepEqual(urls, []string{wantURL, wantURL}) {
		t.Fatalf("audit URLs = %v", urls)
	}

	available = false
	if got, err := lib.GetSkillAudit(t.Context(), fetched[0].ID); err != nil || !reflect.DeepEqual(got, &library.SkillAudit{Availability: library.AuditUnavailable}) {
		t.Fatalf("unavailable audit = %#v, %v", got, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lib.GetSkillAudit(ctx, fetched[0].ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled audit = %v", err)
	}

	before := len(audits.urls())
	if _, err := lib.GetSkillAudit(t.Context(), fetched[0].ID+100); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown skill audit = %v, want NotFound", err)
	}
	if urls := audits.urls(); len(urls) != before {
		t.Fatalf("unknown skill contacted skills.sh: %v", urls[before:])
	}
}

func TestGetSkillAuditSkipsSourcesOutsideSkillsSh(t *testing.T) {
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), mockPreviewArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
	if err != nil || len(fetched) == 0 {
		t.Fatalf("fetch = %#v, %v", fetched, err)
	}
	audits := serveSkillsAudit(t, func(*http.Request) (*http.Response, error) {
		t.Error("URL source audit contacted skills.sh")
		return textResponse(http.StatusInternalServerError, ""), nil
	})
	for _, skill := range fetched {
		if audit, err := lib.GetSkillAudit(t.Context(), skill.ID); err != nil || audit != nil {
			t.Fatalf("URL source audit = %#v, %v; want nil", audit, err)
		}
	}
	if urls := audits.urls(); len(urls) != 0 {
		t.Fatalf("URL source audit contacted skills.sh: %v", urls)
	}
}
