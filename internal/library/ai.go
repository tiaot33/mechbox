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
	// Headers 是每个请求额外带的 HTTP 头,一行一个「名字: 值」,空行忽略;存的是用户填的原文。
	// 和库自己设的头同名(如 Authorization)时用这里的,给要另外鉴权的代理用。
	Headers string `json:"headers,omitempty"`
	// Body 是合并进每个请求体的 JSON 对象,存的是用户填的原文。顶层的每个字段原样写进请求体,覆盖库自己填的同名字段,
	// 用来设置思考深度这类接口参数:OpenAI 兼容的接口如 {"reasoning_effort": "high"},
	// Anthropic 的接口如 {"thinking": {"type": "enabled", "budget_tokens": 2048}};库在 Anthropic 的请求里填的
	// max_tokens 是 4096,思考预算更大时在这里一并写上更大的 max_tokens。
	Body string `json:"body,omitempty"`
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
	return aiSettingsOf(stored), nil
}

func aiSettingsOf(stored store.AISettings) AISettings {
	return normalizeAISettings(AISettings{
		Provider: AIProvider(stored.Provider), BaseURL: stored.BaseURL, APIKey: stored.APIKey, Model: stored.Model,
		Headers: stored.Headers, Body: stored.Body,
	})
}

// SetAISettings 把 AI 设置(各项去掉首尾空白)存进数据库;留空的项从数据库删掉,全部留空即清掉。
// 只认数据库里的这一组,不读环境变量(OPENAI_API_KEY、ANTHROPIC_API_KEY 等)。
// AI 设置和 GitHub 令牌一样随推送、拉回走:拉回后换成拉回的那份库里的,那份库里没有就是没设置。
// 接口类型不是 [AIProviderOpenAI]、[AIProviderAnthropic] 之一、服务地址不是 http(s) 地址、请求头有一行不是「名字: 值」,
// 或请求体不是一个 JSON 对象时返回 InvalidArgument(Details.Field 为 provider / base_url / headers / body),原来的设置不变。
func (l *Library) SetAISettings(ctx context.Context, settings AISettings) (err error) {
	defer func() { err = typedError(err) }()
	settings = normalizeAISettings(settings)
	if _, err := parseAISettings("set ai settings", settings); err != nil {
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
		Headers: settings.Headers, Body: settings.Body,
	})
}

// TestAISettings 用 settings(设置页上还没保存的那一组也行,各项去掉首尾空白)向服务发一条很短的消息,
// 看能不能连上、API Key、模型、请求头与请求体能不能用;不读也不改数据库里的设置。最多等 60 秒(开着思考的模型回答得慢)。
// 设置本身无效时返回 InvalidArgument(Details.Field 为 provider / base_url / headers / body / model;模型必填,API Key 可以留空);
// 服务的回答按 HTTP 状态归类:401 / 403 为 Permission,404 与回答不是这种接口的响应(都是模型或服务地址不对)为 NotFound,
// 连不上、超时、429 与 5xx 为 SourceUnreachable,其他 4xx 与回答里没有文字为 InvalidArgument,
// 回答被 max_tokens 截断(如思考用完了额度)为 InvalidArgument 且 Details.Field 为 max_tokens;
// Message 里带服务原样给出的原因。
func (l *Library) TestAISettings(ctx context.Context, settings AISettings) (err error) {
	defer func() { err = typedError(err) }()
	settings = normalizeAISettings(settings)
	parsed, err := parseAISettings("test ai settings", settings)
	if err != nil {
		return err
	}
	if settings.Model == "" {
		return argumentError("test ai settings", "model", "model is required")
	}
	return ai.Test(ctx, parsed)
}

func normalizeAISettings(settings AISettings) AISettings {
	settings.Provider = AIProvider(strings.TrimSpace(string(settings.Provider)))
	if settings.Provider == "" {
		settings.Provider = AIProviderOpenAI
	}
	settings.BaseURL = strings.TrimSpace(settings.BaseURL)
	settings.APIKey = strings.TrimSpace(settings.APIKey)
	settings.Model = strings.TrimSpace(settings.Model)
	settings.Headers = strings.TrimSpace(settings.Headers)
	settings.Body = strings.TrimSpace(settings.Body)
	return settings
}

func parseAISettings(operation string, settings AISettings) (ai.Settings, error) {
	switch settings.Provider {
	case AIProviderOpenAI, AIProviderAnthropic:
	default:
		return ai.Settings{}, argumentError(operation, "provider", "unknown ai provider "+string(settings.Provider))
	}
	if settings.BaseURL != "" {
		u, err := url.Parse(settings.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ai.Settings{}, argumentError(operation, "base_url", "base url must be an http(s) url")
		}
	}
	headers, err := ai.ParseHeaders(settings.Headers)
	if err != nil {
		return ai.Settings{}, argumentError(operation, "headers", err.Error())
	}
	body, err := ai.ParseBody(settings.Body)
	if err != nil {
		return ai.Settings{}, argumentError(operation, "body", err.Error())
	}
	return ai.Settings{
		Provider: string(settings.Provider), BaseURL: settings.BaseURL, APIKey: settings.APIKey, Model: settings.Model,
		Headers: headers, Body: body,
	}, nil
}
