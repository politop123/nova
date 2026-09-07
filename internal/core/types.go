package core

const (
	ChannelWeb      = "web"
	ChannelTelegram = "telegram"
	ChannelIOS      = "ios"
	ChannelMac      = "mac"
	ChannelPhone    = "phone"
)

const (
	ModalityText  = "text"
	ModalityVoice = "voice"
)

// NovaInput is the channel-neutral envelope. Adapters must normalize into this
// shape before the core makes routing, memory, or policy decisions.
type NovaInput struct {
	UserID         string         `json:"userId"`
	Channel        string         `json:"channel"`
	Modality       string         `json:"modality"`
	Text           string         `json:"text"`
	TraceID        string         `json:"traceId"`
	ConversationID string         `json:"conversationId,omitempty"`
	SessionID      string         `json:"sessionId,omitempty"`
	DeviceContext  map[string]any `json:"deviceContext,omitempty"`
}

type NovaUsage struct {
	Feature           string  `json:"feature"`
	Model             string  `json:"model,omitempty"`
	InputTokens       int     `json:"inputTokens,omitempty"`
	CachedInputTokens int     `json:"cachedInputTokens,omitempty"`
	OutputTokens      int     `json:"outputTokens,omitempty"`
	EstimatedCostUSD  float64 `json:"estimatedCostUsd,omitempty"`
}

// ModelResponse is the provider-neutral result returned by a model adapter.
// Keeping usage beside the text makes cost accounting part of the core
// contract instead of something the HTTP layer has to infer from provider
// specific response types.
type ModelResponse struct {
	Text  string    `json:"text"`
	Usage NovaUsage `json:"usage"`
}

type NovaOutput struct {
	TraceID        string     `json:"traceId"`
	ConversationID string     `json:"conversationId"`
	Text           string     `json:"text"`
	Usage          *NovaUsage `json:"usage,omitempty"`
}
