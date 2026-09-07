package openaiadapter

import (
	"context"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"nova.local/core/internal/core"
)

type Client struct {
	client openai.Client
	model  string
}

func New(apiKey, model string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("OPENAI_API_KEY is not configured")
	}
	return &Client{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}, nil
}

func (c *Client) Respond(ctx context.Context, instructions, input string) (string, error) {
	return c.RespondWithModel(ctx, c.model, instructions, input)
}

func (c *Client) RespondWithModel(ctx context.Context, model, instructions, input string) (string, error) {
	result, err := c.RespondWithModelUsage(ctx, model, instructions, input)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func (c *Client) RespondWithModelUsage(ctx context.Context, model, instructions, input string) (core.ModelResponse, error) {
	response, err := c.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:           shared.ResponsesModel(model),
		Instructions:    openai.String(instructions),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(input)},
		MaxOutputTokens: openai.Int(512),
	})
	if err != nil {
		return core.ModelResponse{}, err
	}
	return core.ModelResponse{
		Text: response.OutputText(),
		Usage: core.NovaUsage{
			Model:             model,
			InputTokens:       int(response.Usage.InputTokens),
			CachedInputTokens: int(response.Usage.InputTokensDetails.CachedTokens),
			OutputTokens:      int(response.Usage.OutputTokens),
		},
	}, nil
}

func (c *Client) RespondWithModelStream(ctx context.Context, model, instructions, input string, onDelta func(string)) (core.ModelResponse, error) {
	stream := c.client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model:           shared.ResponsesModel(model),
		Instructions:    openai.String(instructions),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(input)},
		MaxOutputTokens: openai.Int(512),
	})
	defer stream.Close()

	var builder strings.Builder
	var usage core.NovaUsage
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta == "" {
				continue
			}
			builder.WriteString(event.Delta)
			if onDelta != nil {
				onDelta(event.Delta)
			}
		case "response.completed":
			usage = core.NovaUsage{
				Model:             model,
				InputTokens:       int(event.Response.Usage.InputTokens),
				CachedInputTokens: int(event.Response.Usage.InputTokensDetails.CachedTokens),
				OutputTokens:      int(event.Response.Usage.OutputTokens),
			}
		case "response.failed", "response.incomplete", "error":
			if event.Message != "" {
				return core.ModelResponse{}, errors.New(event.Message)
			}
			return core.ModelResponse{}, errors.New("model stream did not complete")
		}
	}
	if err := stream.Err(); err != nil {
		return core.ModelResponse{}, err
	}
	usage.Model = model
	return core.ModelResponse{Text: builder.String(), Usage: usage}, nil
}
