package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	openaissestream "github.com/openai/openai-go/v3/packages/ssestream"
)

const (
	OpenAI    = "openai"
	Anthropic = "anthropic"
)

const (
	openAIBaseURL        = "https://api.openai.com/v1/"
	testPrompt           = "Reply with OK."
	testIdleTimeout      = 60 * time.Second
	translateIdleTimeout = 2 * time.Minute
	maxTokens            = 128000
)

var (
	ErrUnreachable   = errors.New("ai service unreachable")
	ErrPermission    = errors.New("ai service rejected the api key")
	ErrNotFound      = errors.New("ai model or endpoint not found")
	ErrRejected      = errors.New("ai service rejected the request")
	ErrNotConfigured = errors.New("ai model not configured")
	ErrTruncated     = errors.New("ai answer was cut off by max_tokens")
	errNoEvents      = errors.New("the answer has no stream events")
	errUnfinished    = fmt.Errorf("%w: the answer stream ended before the answer finished", ErrUnreachable)
)

type Header struct {
	Name, Value string
}

type Settings struct {
	Provider, BaseURL, APIKey, Model string
	Headers                          []Header
	Body                             map[string]any
}

func ParseHeaders(text string) ([]Header, error) {
	var headers []Header
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !validHeaderName(name) {
			return nil, fmt.Errorf("header line %q is not name: value", line)
		}
		if strings.ContainsFunc(value, func(r rune) bool { return r < ' ' && r != '\t' || r == 0x7f }) {
			return nil, fmt.Errorf("header %s has control characters", name)
		}
		headers = append(headers, Header{Name: name, Value: value})
	}
	return headers, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		return false
	}
	return true
}

func ParseBody(text string) (map[string]any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("body is not a json object: %w", err)
	}
	if body == nil {
		return nil, errors.New("body is not a json object")
	}
	if decoder.More() {
		return nil, errors.New("body has text after the json object")
	}
	return body, nil
}

func Test(ctx context.Context, settings Settings) error {
	_, err := call(ctx, testIdleTimeout, settings, "", testPrompt)
	return err
}

func Translate(ctx context.Context, settings Settings, text, language string) (string, error) {
	if settings.Model == "" {
		return "", ErrNotConfigured
	}
	return call(ctx, translateIdleTimeout, settings, translatePrompt(language), text)
}

func translatePrompt(language string) string {
	return fmt.Sprintf("You translate the description of an AI agent skill into %s. "+
		"Reply with the translation only, without quotes, notes or explanations. "+
		"Keep code, commands, file names, product names and other proper nouns unchanged. "+
		"If the text is already in that language, reply with it unchanged.", languageName(language))
}

func languageName(language string) string {
	lower := strings.ToLower(language)
	primary, _, _ := strings.Cut(lower, "-")
	switch primary {
	case "zh":
		if strings.Contains(lower, "hant") || strings.HasSuffix(lower, "-tw") || strings.HasSuffix(lower, "-hk") || strings.HasSuffix(lower, "-mo") {
			return "Traditional Chinese"
		}
		return "Simplified Chinese"
	case "en":
		return "English"
	}
	return "the language with BCP 47 tag " + language
}

func call(ctx context.Context, idle time.Duration, settings Settings, system, prompt string) (string, error) {
	callCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	silent := fmt.Errorf("no new content for %s", idle)
	timer := time.AfterFunc(idle, func() { cancel(silent) })
	defer timer.Stop()
	received := func() { timer.Reset(idle) }
	var answer string
	var err error
	if settings.Provider == Anthropic {
		answer, err = askAnthropic(callCtx, received, settings, system, prompt)
	} else {
		answer, err = askOpenAI(callCtx, received, settings, system, prompt)
	}
	if err == nil || ctx.Err() != nil {
		return answer, err
	}
	if context.Cause(callCtx) == silent {
		return "", fmt.Errorf("%w: %s", ErrUnreachable, silent)
	}
	if errors.Is(err, ErrRejected) || errors.Is(err, ErrTruncated) || errors.Is(err, ErrUnreachable) {
		return "", err
	}
	status, reason := describe(err)
	return "", fmt.Errorf("%w: %s", classify(status), reason)
}

func bodyKeys(body map[string]any) []string {
	return slices.Sorted(maps.Keys(body))
}

func pathKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func askOpenAI(ctx context.Context, received func(), settings Settings, system, prompt string) (string, error) {
	base := settings.BaseURL
	if base == "" {
		base = openAIBaseURL
	}
	opts := []openaioption.RequestOption{
		openaioption.WithBaseURL(base),
		openaioption.WithAPIKey(settings.APIKey),
		openaioption.WithMaxRetries(0),
	}
	for _, header := range settings.Headers {
		opts = append(opts, openaioption.WithHeader(header.Name, header.Value))
	}
	for _, key := range bodyKeys(settings.Body) {
		opts = append(opts, openaioption.WithJSONSet(pathKey(key), settings.Body[key]))
	}
	messages := []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)}
	if system != "" {
		messages = append([]openai.ChatCompletionMessageParamUnion{openai.SystemMessage(system)}, messages...)
	}
	chat := openai.NewChatCompletionService(opts...)
	stream := chat.NewStreaming(ctx, openai.ChatCompletionNewParams{Model: settings.Model, Messages: messages})
	defer stream.Close()
	var text strings.Builder
	var finish string
	chunks, choices := 0, 0
	for stream.Next() {
		received()
		chunks++
		for _, choice := range stream.Current().Choices {
			if choice.Index != 0 {
				continue
			}
			choices++
			text.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
	}
	if err := stream.Err(); err != nil {
		return "", err
	}
	if chunks == 0 {
		return "", errNoEvents
	}
	if choices == 0 {
		return "", fmt.Errorf("%w: the answer has no choices", ErrRejected)
	}
	if finish == "" {
		return "", errUnfinished
	}
	return answerText(text.String(), finish)
}

func askAnthropic(ctx context.Context, received func(), settings Settings, system, prompt string) (string, error) {
	opts := []anthropicoption.RequestOption{
		anthropicoption.WithoutEnvironmentDefaults(),
		anthropicoption.WithAPIKey(settings.APIKey),
		anthropicoption.WithMaxRetries(0),
	}
	if settings.BaseURL != "" {
		opts = append(opts, anthropicoption.WithBaseURL(settings.BaseURL))
	}
	for _, header := range settings.Headers {
		opts = append(opts, anthropicoption.WithHeader(header.Name, header.Value))
	}
	for _, key := range bodyKeys(settings.Body) {
		opts = append(opts, anthropicoption.WithJSONSet(pathKey(key), settings.Body[key]))
	}
	params := anthropic.MessageNewParams{
		Model:     settings.Model,
		MaxTokens: maxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	client := anthropic.NewClient(opts...)
	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var resp anthropic.Message
	events := 0
	for stream.Next() {
		received()
		events++
		if err := resp.Accumulate(stream.Current()); err != nil {
			return "", err
		}
	}
	if err := stream.Err(); err != nil {
		return "", err
	}
	if events == 0 {
		return "", errNoEvents
	}
	if resp.StopReason == "" {
		return "", errUnfinished
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return answerText(text.String(), string(resp.StopReason))
}

func answerText(text, finish string) (string, error) {
	if finish == "length" || finish == "max_tokens" {
		return "", fmt.Errorf("%w (finish reason %q)", ErrTruncated, finish)
	}
	text = strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(text, "<think>"); ok {
		_, after, found := strings.Cut(rest, "</think>")
		if !found {
			return "", fmt.Errorf("%w: the answer stops inside <think> (finish reason %q)", ErrTruncated, finish)
		}
		text = strings.TrimSpace(after)
	}
	if text == "" {
		return "", fmt.Errorf("%w: the answer has no text (finish reason %q)", ErrRejected, finish)
	}
	return text, nil
}

func describe(err error) (int, string) {
	if apiErr, ok := errors.AsType[*openai.Error](err); ok {
		if apiErr.Message == "" {
			return apiErr.StatusCode, apiErr.Error()
		}
		return apiErr.StatusCode, apiErr.Error() + ": " + apiErr.Message
	}
	if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
		if apiErr.StatusCode == http.StatusOK {
			return http.StatusInternalServerError, apiErr.Error()
		}
		return apiErr.StatusCode, apiErr.Error()
	}
	if streamErr, ok := errors.AsType[*openaissestream.StreamError](err); ok {
		return http.StatusInternalServerError, streamErr.Error()
	}
	if _, ok := errors.AsType[net.Error](err); ok || errors.Is(err, context.DeadlineExceeded) {
		return 0, err.Error()
	}
	return http.StatusNotFound, "the answer is not from this api: " + err.Error()
}

func classify(status int) error {
	switch {
	case status == 0, status == http.StatusRequestTimeout, status == http.StatusTooManyRequests, status >= 500:
		return ErrUnreachable
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ErrPermission
	case status == http.StatusNotFound:
		return ErrNotFound
	default:
		return ErrRejected
	}
}
