package ai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const shortIdle = 200 * time.Millisecond

// streamServer 按接口类型回答流式响应:先停 before,再逐个发出 events,每个之间停 gap。
func streamServer(t *testing.T, before, gap time.Duration, events ...string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		pause := func(d time.Duration) bool {
			select {
			case <-time.After(d):
				return true
			case <-r.Context().Done():
				return false
			}
		}
		if !pause(before) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for i, event := range events {
			if i > 0 && !pause(gap) {
				return
			}
			_, _ = io.WriteString(w, event+"\n\n")
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func openAIEvents(words ...string) []string {
	var events []string
	for _, word := range words {
		events = append(events, `data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"`+word+`"},"finish_reason":null}]}`)
	}
	return append(events,
		`data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`)
}

func anthropicEvents(words ...string) []string {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
	}
	for _, word := range words {
		events = append(events, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\""+word+"\"}}")
	}
	return append(events,
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}")
}

func settingsFor(provider, url string) Settings {
	if provider == Anthropic {
		return Settings{Provider: Anthropic, BaseURL: url, APIKey: "k", Model: "m"}
	}
	return Settings{Provider: OpenAI, BaseURL: url + "/v1", APIKey: "k", Model: "m"}
}

func eventsFor(provider string, words ...string) []string {
	if provider == Anthropic {
		return anthropicEvents(words...)
	}
	return openAIEvents(words...)
}

// 服务一直在输出时，总时长超过超时也照样拿到完整回答。
func TestCallKeepsWaitingWhileTheAnswerStreams(t *testing.T) {
	words := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, provider := range []string{OpenAI, Anthropic} {
		url := streamServer(t, shortIdle/2, shortIdle/2, eventsFor(provider, words...)...)
		start := time.Now()
		answer, err := call(t.Context(), shortIdle, settingsFor(provider, url), "", "hi")
		if err != nil || answer != strings.Join(words, "") {
			t.Fatalf("%s: call = %q, %v, want the whole answer", provider, answer, err)
		}
		if elapsed := time.Since(start); elapsed < 3*shortIdle {
			t.Fatalf("%s: the stream took %s, want it to outlast the idle timeout", provider, elapsed)
		}
	}
}

// 连续一段时间没有新内容(还没回响应，或回了一半停住)时按连不上报错，不再等下去。
func TestCallGivesUpWhenNothingArrives(t *testing.T) {
	for _, provider := range []string{OpenAI, Anthropic} {
		for name, url := range map[string]string{
			"before answering": streamServer(t, 10*shortIdle, 0, eventsFor(provider, "a")...),
			"midway":           streamServer(t, 0, 10*shortIdle, eventsFor(provider, "a")...),
		} {
			start := time.Now()
			_, err := call(t.Context(), shortIdle, settingsFor(provider, url), "", "hi")
			if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "no new content") {
				t.Errorf("%s silent %s: call = %v, want ErrUnreachable for no new content", provider, name, err)
			}
			if elapsed := time.Since(start); elapsed > 5*shortIdle {
				t.Errorf("%s silent %s: gave up after %s, want about %s", provider, name, elapsed, shortIdle)
			}
		}
	}
}

// 回答到一半服务发来错误事件(如过载)时，和请求失败时的 5xx 一样按连不上报错，带上服务的原因。
func TestCallTreatsErrorEventsAsServiceFailures(t *testing.T) {
	for provider, events := range map[string][]string{
		OpenAI:    {openAIEvents("a")[0], `data: {"error":{"message":"stub overloaded"}}`},
		Anthropic: {anthropicEvents("a")[0], "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"stub overloaded\"}}"},
	} {
		url := streamServer(t, 0, 0, events...)
		_, err := call(t.Context(), shortIdle, settingsFor(provider, url), "", "hi")
		if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "stub overloaded") {
			t.Errorf("%s: call = %v, want ErrUnreachable with the service's reason", provider, err)
		}
	}
}

// 流正常结束，但没等到结束原因(回答没说完)时按连不上报错，不把半截回答当成功。
func TestCallRejectsAnAnswerThatNeverFinishes(t *testing.T) {
	for _, provider := range []string{OpenAI, Anthropic} {
		events := eventsFor(provider, "half")
		cut := 1
		if provider == Anthropic {
			cut = 3
		}
		url := streamServer(t, 0, 0, events[:cut]...)
		answer, err := call(t.Context(), shortIdle, settingsFor(provider, url), "", "hi")
		if !errors.Is(err, ErrUnreachable) || answer != "" {
			t.Errorf("%s: call = %q, %v, want ErrUnreachable and no answer", provider, answer, err)
		}
	}
}

// 请求体里要了多个候选(如 n 为 2)时只拼第一个候选的内容，结束原因也只看它的。
func TestCallKeepsOnlyTheFirstChoice(t *testing.T) {
	chunk := func(index int, content, finish string) string {
		return `data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":` +
			string(rune('0'+index)) + `,"delta":{"content":"` + content + `"},"finish_reason":` + finish + `}]}`
	}
	for name, tc := range map[string]struct {
		events []string
		want   error
	}{
		"first finishes": {[]string{chunk(0, "[1", "null"), chunk(1, "[]", "null"), chunk(0, "]", `"stop"`), chunk(1, "", `"length"`), `data: [DONE]`}, nil},
		"first truncated": {[]string{chunk(0, "[1", "null"), chunk(0, "", `"length"`), chunk(1, "[]", `"stop"`), `data: [DONE]`}, ErrTruncated},
	} {
		url := streamServer(t, 0, 0, tc.events...)
		answer, err := call(t.Context(), shortIdle, settingsFor(OpenAI, url), "", "hi")
		if tc.want == nil && (err != nil || answer != "[1]") || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: call = %q, %v, want the first choice's answer or %v", name, answer, err, tc.want)
		}
	}
}
