package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	openAIBaseURL = "https://api.openai.com/v1/"
	testPrompt    = "Reply with OK."
	testTimeout   = 30 * time.Second
)

var (
	ErrUnreachable = errors.New("ai service unreachable")
	ErrPermission  = errors.New("ai service rejected the api key")
	ErrNotFound    = errors.New("ai model or endpoint not found")
	ErrRejected    = errors.New("ai service rejected the request")
)

type Settings struct {
	Provider, BaseURL, APIKey, Model string
}

func Test(ctx context.Context, settings Settings) error {
	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	var err error
	if settings.Provider == Anthropic {
		err = testAnthropic(testCtx, settings)
	} else {
		err = testOpenAI(testCtx, settings)
	}
	if err == nil || ctx.Err() != nil {
		return err
	}
	status, reason := describe(err)
	return fmt.Errorf("%w: %s", classify(status), reason)
}

func testOpenAI(ctx context.Context, settings Settings) error {
	base := settings.BaseURL
	if base == "" {
		base = openAIBaseURL
	}
	chat := openai.NewChatCompletionService(
		openaioption.WithBaseURL(base),
		openaioption.WithAPIKey(settings.APIKey),
		openaioption.WithMaxRetries(0),
	)
	_, err := chat.New(ctx, openai.ChatCompletionNewParams{
		Model:    settings.Model,
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(testPrompt)},
	})
	return err
}

func testAnthropic(ctx context.Context, settings Settings) error {
	opts := []anthropicoption.RequestOption{
		anthropicoption.WithoutEnvironmentDefaults(),
		anthropicoption.WithAPIKey(settings.APIKey),
		anthropicoption.WithMaxRetries(0),
	}
	if settings.BaseURL != "" {
		opts = append(opts, anthropicoption.WithBaseURL(settings.BaseURL))
	}
	client := anthropic.NewClient(opts...)
	_, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     settings.Model,
		MaxTokens: 16,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(testPrompt))},
	})
	return err
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
