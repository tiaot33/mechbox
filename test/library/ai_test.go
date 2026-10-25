package library_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func setAISettings(t *testing.T, lib *library.Library, settings library.AISettings) {
	t.Helper()
	if err := lib.SetAISettings(t.Context(), settings); err != nil {
		t.Fatalf("SetAISettings(%+v) = %v", settings, err)
	}
}

func aiSettingsAre(t *testing.T, lib *library.Library, want library.AISettings) {
	t.Helper()
	if got, err := lib.AISettings(t.Context()); err != nil || got != want {
		t.Fatalf("AISettings = %+v, %v; want %+v", got, err, want)
	}
}

// AI 设置存在数据库里:各项去掉首尾空白,重新打开同一个数据库仍是保存的那一组;留空的项删掉,全部留空即清掉。
func TestAISettingsAreTrimmedKeptAndCleared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	first := openTestLibraryAt(t, path)
	aiSettingsAre(t, first, library.AISettings{})
	setAISettings(t, first, library.AISettings{
		Provider: library.AIProviderAnthropic, BaseURL: " https://ai.example.test/ ", APIKey: "  sk-test\n", Model: " claude-test ",
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibraryAt(t, path)
	aiSettingsAre(t, lib, library.AISettings{
		Provider: library.AIProviderAnthropic, BaseURL: "https://ai.example.test/", APIKey: "sk-test", Model: "claude-test",
	})
	setAISettings(t, lib, library.AISettings{APIKey: "sk-other", Model: "gpt-test"})
	aiSettingsAre(t, lib, library.AISettings{APIKey: "sk-other", Model: "gpt-test"})
	setAISettings(t, lib, library.AISettings{})
	aiSettingsAre(t, lib, library.AISettings{})
	if token, err := lib.GitHubToken(t.Context()); err != nil || token != "" {
		t.Fatalf("GitHubToken = %q, %v; ai settings must not touch other settings", token, err)
	}
}

// 接口类型不认识、服务地址不是 http(s) 地址时,保存与测试连接都报 InvalidArgument 并指出是哪一项,原来的设置不变;
// 测试连接还要求填了 API Key 与模型。
func TestAISettingsRejectInvalidValues(t *testing.T) {
	lib := openTestLibrary(t)
	saved := library.AISettings{APIKey: "sk-test", Model: "gpt-test"}
	setAISettings(t, lib, saved)
	for _, tc := range []struct {
		settings library.AISettings
		field    string
		testOnly bool
	}{
		{library.AISettings{Provider: "gemini", APIKey: "k", Model: "m"}, "provider", false},
		{library.AISettings{BaseURL: "api.example.test/v1", APIKey: "k", Model: "m"}, "base_url", false},
		{library.AISettings{BaseURL: "ftp://api.example.test", APIKey: "k", Model: "m"}, "base_url", false},
		{library.AISettings{BaseURL: "https://", APIKey: "k", Model: "m"}, "base_url", false},
		{library.AISettings{APIKey: " ", Model: "m"}, "api_key", true},
		{library.AISettings{APIKey: "k"}, "model", true},
	} {
		err := lib.TestAISettings(t.Context(), tc.settings)
		if e := library.ErrorOf(err); e == nil || e.Code != library.InvalidArgument || e.Details.Field != tc.field {
			t.Errorf("TestAISettings(%+v) = %#v, want InvalidArgument on %s", tc.settings, e, tc.field)
		}
		if tc.testOnly {
			continue
		}
		err = lib.SetAISettings(t.Context(), tc.settings)
		if e := library.ErrorOf(err); e == nil || e.Code != library.InvalidArgument || e.Details.Field != tc.field {
			t.Errorf("SetAISettings(%+v) = %#v, want InvalidArgument on %s", tc.settings, e, tc.field)
		}
	}
	aiSettingsAre(t, lib, saved)
}

// AI 设置和 GitHub 令牌一样随推送、拉回走:拉回的一侧换成推送那份库里的;拉回一份没有 AI 设置的库,本机的随之清掉。
func TestAISettingsTravelWithSync(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	pushed := library.AISettings{Provider: library.AIProviderAnthropic, APIKey: "sk-synced", Model: "claude-test"}
	pushing := stub.open(t)
	setAISettings(t, pushing, pushed)
	if _, err := pushing.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	pulling := stub.open(t)
	setAISettings(t, pulling, library.AISettings{APIKey: "sk-local", Model: "gpt-local"})
	pull := func() {
		t.Helper()
		plan, err := pulling.PlanSyncPull(ctx)
		if err != nil || !plan.CanPull {
			t.Fatalf("pull plan = %#v, %v", plan, err)
		}
		if err := pulling.SyncPull(ctx, plan.Remote.Identity); err != nil {
			t.Fatal(err)
		}
	}
	pull()
	aiSettingsAre(t, pulling, pushed)

	setAISettings(t, pushing, library.AISettings{})
	plan, err := pushing.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pushing.SyncPush(ctx, plan.Remote.Identity); err != nil {
		t.Fatal(err)
	}
	pull()
	aiSettingsAre(t, pulling, library.AISettings{})
}

// aiRequest 是 AI 服务桩记下的一次请求。
type aiRequest struct {
	path, authorization, apiKey, organization, model string
}

// aiStub 按 OpenAI 兼容的 Chat Completions 与 Anthropic 的 Messages 回答;status 不是 200 时回答那个状态与一条错误。
type aiStub struct {
	url      string
	mu       sync.Mutex
	status   int
	requests []aiRequest
}

func newAIStub(t *testing.T) *aiStub {
	t.Helper()
	stub := &aiStub{status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		stub.mu.Lock()
		stub.requests = append(stub.requests, aiRequest{
			path: r.URL.Path, authorization: r.Header.Get("Authorization"), apiKey: r.Header.Get("X-Api-Key"),
			organization: r.Header.Get("OpenAI-Organization"), model: body.Model,
		})
		status := stub.status
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"stub_error","message":"stub says no"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"`+body.Model+`",`+
				`"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"`+body.Model+`",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	stub.url = server.URL
	return stub
}

func (s *aiStub) take() []aiRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := s.requests
	s.requests = nil
	return requests
}

func (s *aiStub) respond(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// 测试连接用传入的这一组(不必先保存)向服务发一条消息:OpenAI 兼容的接口发到服务地址下的 chat/completions,
// 带 Bearer API Key;Anthropic 的接口发到 v1/messages,带 x-api-key。环境变量里的 Key、组织与令牌都不用。
func TestAISettingsTestSendsMessageWithGivenSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-openai-key")
	t.Setenv("OPENAI_ORG_ID", "env-org")
	t.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-auth-token")
	stub := newAIStub(t)
	lib := openTestLibrary(t)
	for _, tc := range []struct {
		settings library.AISettings
		want     aiRequest
	}{
		{
			library.AISettings{BaseURL: stub.url + "/v1", APIKey: " sk-form ", Model: "gpt-test"},
			aiRequest{path: "/v1/chat/completions", authorization: "Bearer sk-form", model: "gpt-test"},
		},
		{
			library.AISettings{Provider: library.AIProviderAnthropic, BaseURL: stub.url, APIKey: "sk-ant-form", Model: "claude-test"},
			aiRequest{path: "/v1/messages", apiKey: "sk-ant-form", model: "claude-test"},
		},
	} {
		if err := lib.TestAISettings(t.Context(), tc.settings); err != nil {
			t.Fatalf("TestAISettings(%+v) = %v", tc.settings, err)
		}
		if got := stub.take(); len(got) != 1 || got[0] != tc.want {
			t.Fatalf("requests for %s = %+v, want %+v", tc.settings.Provider, got, tc.want)
		}
	}
	aiSettingsAre(t, lib, library.AISettings{})
}

// 服务的回答按 HTTP 状态归类:401 / 403 为 Permission,404 为 NotFound,429 与 5xx 为 SourceUnreachable,
// 其他 4xx 为 InvalidArgument;连不上也是 SourceUnreachable。失败不重试,一次测试只发一条消息。
func TestAISettingsTestClassifiesServiceAnswers(t *testing.T) {
	stub := newAIStub(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	lib := openTestLibrary(t)
	for _, provider := range []library.AIProvider{library.AIProviderOpenAI, library.AIProviderAnthropic} {
		settings := library.AISettings{Provider: provider, BaseURL: stub.url + "/v1", APIKey: "sk-test", Model: "model-test"}
		if provider == library.AIProviderAnthropic {
			settings.BaseURL = stub.url
		}
		for status, want := range map[int]library.ErrorCode{
			http.StatusUnauthorized:        library.Permission,
			http.StatusForbidden:           library.Permission,
			http.StatusNotFound:            library.NotFound,
			http.StatusBadRequest:          library.InvalidArgument,
			http.StatusTooManyRequests:     library.SourceUnreachable,
			http.StatusInternalServerError: library.SourceUnreachable,
		} {
			stub.respond(status)
			err := lib.TestAISettings(t.Context(), settings)
			if !library.IsCode(err, want) || !strings.Contains(err.Error(), "stub says no") {
				t.Errorf("%s answering %d: TestAISettings = %v, want %s with the service's reason", provider, status, err, want)
			}
			if got := stub.take(); len(got) != 1 {
				t.Errorf("%s answering %d: %d requests, want 1", provider, status, len(got))
			}
		}
		settings.BaseURL = closed.URL
		if err := lib.TestAISettings(t.Context(), settings); !library.IsCode(err, library.SourceUnreachable) {
			t.Errorf("%s unreachable: TestAISettings = %v, want SourceUnreachable", provider, err)
		}
	}
}
