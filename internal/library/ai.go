package library

import (
	"context"
	"net/url"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/ai"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// AIProvider 是 AI 服务的接口类型,决定用哪种 SDK 调用。
type AIProvider string

const (
	// AIProviderOpenAI 是 OpenAI 兼容的接口(Chat Completions):OpenAI、DeepSeek、OpenRouter、Ollama 等服务都提供它。
	AIProviderOpenAI AIProvider = ai.OpenAI
	// AIProviderAnthropic 是 Anthropic 的接口(Messages)。
	AIProviderAnthropic AIProvider = ai.Anthropic
)

// AISettings 是调用 AI 用的设置,用 [Library.SetAISettings] 存进数据库。
type AISettings struct {
	// Provider 是接口类型;传入时留空为 OpenAI 兼容,读出的总是两者之一。
	Provider AIProvider `json:"provider,omitempty"`
	// BaseURL 是服务地址;留空时用所选接口的官方地址。
	BaseURL string `json:"base_url,omitempty"`
	// APIKey 是 API Key;Ollama 这类不要 Key 的服务可以留空。
	APIKey string `json:"api_key,omitempty"`
	// Model 是模型名,原样传给服务。
	Model string `json:"model,omitempty"`
}

// AISettings 返回存在数据库里的 AI 设置(设置页显示它);没填的项为空,接口类型没选过时为 [AIProviderOpenAI]。
// 拉回后是拉回的那份库里的。
func (l *Library) AISettings(ctx context.Context) (settings AISettings, err error) {
	defer func() { err = typedError(err) }()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	stored, err := l.store.AISettings(ctx)
	if err != nil {
		return AISettings{}, err
	}
	return normalizeAISettings(AISettings{
		Provider: AIProvider(stored.Provider), BaseURL: stored.BaseURL, APIKey: stored.APIKey, Model: stored.Model,
	}), nil
}

// SetAISettings 把 AI 设置(各项去掉首尾空白)存进数据库;留空的项从数据库删掉,全部留空即清掉。
// 只认数据库里的这一组,不读环境变量(OPENAI_API_KEY、ANTHROPIC_API_KEY 等)。
// AI 设置和 GitHub 令牌一样随推送、拉回走:拉回后换成拉回的那份库里的,那份库里没有就是没设置。
// 接口类型不是 [AIProviderOpenAI]、[AIProviderAnthropic] 之一,或服务地址不是 http(s) 地址时返回 InvalidArgument
// (Details.Field 为 provider / base_url),原来的设置不变。
func (l *Library) SetAISettings(ctx context.Context, settings AISettings) (err error) {
	defer func() { err = typedError(err) }()
	settings = normalizeAISettings(settings)
	if err := validateAISettings("set ai settings", settings); err != nil {
		return err
	}
	provider := string(settings.Provider)
	if settings.Provider == AIProviderOpenAI {
		provider = ""
	}
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	return l.store.SetAISettings(ctx, store.AISettings{
		Provider: provider, BaseURL: settings.BaseURL, APIKey: settings.APIKey, Model: settings.Model,
	})
}

// TestAISettings 用 settings(设置页上还没保存的那一组也行,各项去掉首尾空白)向服务发一条很短的消息,
// 看能不能连上、API Key 与模型能不能用;不读也不改数据库里的设置。最多等 30 秒。
// 设置本身无效时返回 InvalidArgument(Details.Field 为 provider / base_url / model;模型必填,API Key 可以留空);
// 服务的回答按 HTTP 状态归类:401 / 403 为 Permission,404 与回答不是这种接口的响应(都是模型或服务地址不对)为 NotFound,
// 连不上、超时、429 与 5xx 为 SourceUnreachable,其他 4xx 为 InvalidArgument;Message 里带服务原样给出的原因。
func (l *Library) TestAISettings(ctx context.Context, settings AISettings) (err error) {
	defer func() { err = typedError(err) }()
	settings = normalizeAISettings(settings)
	if err := validateAISettings("test ai settings", settings); err != nil {
		return err
	}
	if settings.Model == "" {
		return argumentError("test ai settings", "model", "model is required")
	}
	return ai.Test(ctx, ai.Settings{
		Provider: string(settings.Provider), BaseURL: settings.BaseURL, APIKey: settings.APIKey, Model: settings.Model,
	})
}

func normalizeAISettings(settings AISettings) AISettings {
	settings.Provider = AIProvider(strings.TrimSpace(string(settings.Provider)))
	if settings.Provider == "" {
		settings.Provider = AIProviderOpenAI
	}
	settings.BaseURL = strings.TrimSpace(settings.BaseURL)
	settings.APIKey = strings.TrimSpace(settings.APIKey)
	settings.Model = strings.TrimSpace(settings.Model)
	return settings
}

func validateAISettings(operation string, settings AISettings) error {
	switch settings.Provider {
	case AIProviderOpenAI, AIProviderAnthropic:
	default:
		return argumentError(operation, "provider", "unknown ai provider "+string(settings.Provider))
	}
	if settings.BaseURL == "" {
		return nil
	}
	u, err := url.Parse(settings.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return argumentError(operation, "base_url", "base url must be an http(s) url")
	}
	return nil
}
