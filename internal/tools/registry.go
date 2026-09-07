package tools

import (
	"context"
	"fmt"

	"nova.local/core/internal/policy"
)

type ExecutionContext struct {
	UserID         string
	TraceID        string
	IdempotencyKey string
}

type Tool interface {
	Name() string
	Description() string
	Permission() policy.Permission
	Execute(ctx context.Context, input map[string]any, execution ExecutionContext) (any, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(tool Tool) error {
	if _, exists := r.tools[tool.Name()]; exists {
		return fmt.Errorf("tool %q is already registered", tool.Name())
	}
	r.tools[tool.Name()] = tool
	return nil
}

func (r *Registry) Get(name string) (Tool, error) {
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool, nil
}

func (r *Registry) List() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result
}
