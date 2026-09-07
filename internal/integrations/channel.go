package integrations

import "nova.local/core/internal/core"

type ChannelAdapter interface {
	Channel() string
	Normalize(incoming any) (core.NovaInput, error)
	Deliver(output core.NovaOutput) error
}

type Health struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	CheckedAt  string `json:"checkedAt"`
}
