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
)

const (
	OpenAI    = "openai"
	Anthropic = "anthropic"
)

const (
	openAIBaseURL    = "https://api.openai.com/v1/"
	testPrompt       = "Reply with OK."
	testTimeout      = 60 * time.Second
	translateTimeout = 2 * time.Minute
	maxTokens        = 4096
)

var (
	ErrUnreachable   = errors.New("ai service unreachable")
	ErrPermission    = errors.New("ai service rejected the api key")
	ErrNotFound      = errors.New("ai model or endpoint not found")
	ErrRejected      = errors.New("ai service rejected the request")
	ErrNotConfigured = errors.New("ai model not configured")
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
	_, err := call(ctx, testTimeout, settings, "", testPrompt)
	return err
}

func Translate(ctx context.Context, settings Settings, text, language string) (string, error) {
	if settings.Model == "" {
		return "", ErrNotConfigured
	}
	return call(ctx, translateTimeout, settings, translatePrompt(language), text)
}

func translatePrompt(language string) string {
	return fmt.Sprintf("You translate the description of an AI agent skill into %[1]s. "+
		"Reply with the translation only, without quotes, notes or explanations. "+
		"Keep code, commands, file names, product names and other proper nouns unchanged. "+
		"If the text is already in %[1]s, reply with it unchanged.", language)
}

func call(ctx context.Context, timeout time.Duration, settings Settings, system, prompt string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var answer string
	var err error
	if settings.Provider == Anthropic {
		answer, err = askAnthropic(callCtx, settings, system, prompt)
	} else {
		answer, err = askOpenAI(callCtx, settings, system, prompt)
	}
	if err == nil || ctx.Err() != nil {
		return answer, err
	}
	if errors.Is(err, ErrRejected) {
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

func askOpenAI(ctx context.Context, settings Settings, system, prompt string) (string, error) {
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
	resp, err := chat.New(ctx, openai.ChatCompletionNewParams{Model: settings.Model, Messages: messages})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%w: the answer has no choices", ErrRejected)
	}
	choice := resp.Choices[0]
	return answerText(choice.Message.Content, choice.FinishReason)
}

func askAnthropic(ctx context.Context, settings Settings, system, prompt string) (string, error) {
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
	resp, err := client.Messages.New(ctx, params)
	if err != nil {
		return "", err
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
	text = strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(text, "<think>"); ok {
		if _, after, found := strings.Cut(rest, "</think>"); found {
			text = strings.TrimSpace(after)
		}
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
		return apiErr.StatusCode, apiErr.Error()
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
