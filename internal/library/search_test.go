package library_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestSearchSkillsReturnsGitHubSourcesWithoutChangingLibrary(t *testing.T) {
	lib := openTestLibrary(t)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://skills.sh/api/search?limit=20&q=testing+tools" {
			t.Errorf("search URL = %s", r.URL)
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("search lacks timeout")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"skills":[{"id":"owner/repo/testing","name":"testing","source":"owner/repo","installs":123},{"id":"owner/repo/minimal","name":"minimal","source":"owner/repo"},{"id":"other","name":"unsupported","source":"https://gitlab.com/owner/repo"}]}`))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	result, err := lib.SearchSkills(t.Context(), "  testing tools  ")
	// 无法转换为可预览 GitHub 地址的条目直接过滤掉。
	if err != nil || len(result) != 2 {
		t.Fatalf("search = %#v, %v", result, err)
	}
	first := result[0]
	wantPreview := library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo@testing"}}
	if first.ID != "owner/repo/testing" || first.Name != "testing" || !reflect.DeepEqual(first.Preview, wantPreview) || first.DetailURL != "https://skills.sh/owner/repo/testing" || first.Installs == nil || *first.Installs != 123 || result[1].Installs != nil {
		t.Fatalf("search metadata = %#v", result)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("skills = %v, %v", skills, err)
	}
	if collections, err := lib.ListCollections(t.Context()); err != nil || len(collections) != 0 {
		t.Fatalf("collections = %v, %v", collections, err)
	}
}

func TestSearchSkillsDistinguishesEmptyInvalidAndUnavailableResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		code       library.ErrorCode
	}{
		{"empty", `{"skills":[]}`, 200, ""},
		{"missing list", `{}`, 200, library.SourceInvalid},
		{"invalid json", `broken`, 200, library.SourceInvalid},
		{"missing name", `{"skills":[{"id":"owner/repo/a","source":"owner/repo"}]}`, 200, library.SourceInvalid},
		{"wrong installs", `{"skills":[{"id":"owner/repo/a","name":"a","source":"owner/repo","installs":"bad"}]}`, 200, library.SourceInvalid},
		{"HTTP error", ``, 500, library.SourceUnreachable},
		{"permission", ``, 403, library.Permission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			client := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = client })
			result, err := lib.SearchSkills(t.Context(), "testing")
			if tc.code == "" {
				if err != nil || result == nil || len(result) != 0 {
					t.Fatalf("empty = %#v, %v", result, err)
				}
			} else if !library.IsCode(err, tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if collections, err := lib.ListCollections(t.Context()); err != nil || len(collections) != 0 {
				t.Fatalf("collections = %#v, %v", collections, err)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("skills = %#v, %v", skills, err)
			}
		})
	}
}

func TestSearchSkillsLimitsResultsAndSupportsCancellation(t *testing.T) {
	lib := openTestLibrary(t)
	client := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = client })
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) { t.Fatal("empty query contacted network"); return nil, nil })}
	if _, err := lib.SearchSkills(t.Context(), " \t\n"); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("empty query = %v", err)
	}
	var entries []string
	for i := range 25 {
		entries = append(entries, fmt.Sprintf(`{"id":"owner/repo/a%d","name":"a%d","source":"owner/repo"}`, i, i))
	}
	http.DefaultClient.Transport = fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"skills":[` + strings.Join(entries, ",") + `]}`))}, nil
	})
	result, err := lib.SearchSkills(t.Context(), "test")
	if err != nil || len(result) != 20 {
		t.Fatalf("limit = %#v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if _, err := lib.SearchSkills(ctx, "test"); !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	http.DefaultClient.Transport = fetchTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := lib.SearchSkills(t.Context(), "test"); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("offline = %v", err)
	}
}

func TestSearchSkillsDoesNotInterpretDisplayNamesAsRefs(t *testing.T) {
	lib := openTestLibrary(t)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"skills":[{"id":"owner/repo/display","name":"display#other-ref","source":"owner/repo"}]}`))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	result, err := lib.SearchSkills(t.Context(), "display")
	if err != nil || len(result) != 0 {
		t.Fatalf("unsafe selector = %#v, %v", result, err)
	}
}
