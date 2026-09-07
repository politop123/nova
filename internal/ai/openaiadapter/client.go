package openaiadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"nova.local/core/internal/core"
)

type Client struct {
	client             openai.Client
	model              string
	transcriptionModel string
}

func New(apiKey, model, transcriptionModel string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("OPENAI_API_KEY is not configured")
	}
	transcriptionModel = strings.TrimSpace(transcriptionModel)
	if transcriptionModel == "" {
		transcriptionModel = "gpt-4o-mini-transcribe"
	}
	return &Client{
		client:             openai.NewClient(option.WithAPIKey(apiKey)),
		model:              model,
		transcriptionModel: transcriptionModel,
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

func (c *Client) PlanActions(ctx context.Context, model, instructions, input string) (core.ActionPlanResponse, error) {
	response, err := c.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:           shared.ResponsesModel(model),
		Instructions:    openai.String(instructions),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(input)},
		MaxOutputTokens: openai.Int(700),
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:        "nova_action_plan",
					Description: openai.String("A validated NOVA intent and typed action plan."),
					Schema:      actionPlanSchema(),
					Strict:      openai.Bool(true),
				},
			},
			Verbosity: responses.ResponseTextConfigVerbosityLow,
		},
	})
	if err != nil {
		return core.ActionPlanResponse{}, err
	}
	var plan core.ActionPlan
	if err := json.Unmarshal([]byte(response.OutputText()), &plan); err != nil {
		return core.ActionPlanResponse{}, err
	}
	return core.ActionPlanResponse{
		Plan: normalizeActionPlan(plan),
		Usage: core.NovaUsage{
			Model:             model,
			InputTokens:       int(response.Usage.InputTokens),
			CachedInputTokens: int(response.Usage.InputTokensDetails.CachedTokens),
			OutputTokens:      int(response.Usage.OutputTokens),
		},
	}, nil
}

func normalizeActionPlan(plan core.ActionPlan) core.ActionPlan {
	plan.Intent = strings.TrimSpace(plan.Intent)
	plan.Reply = strings.TrimSpace(plan.Reply)
	if plan.Intent == "" {
		plan.Intent = core.IntentUnknown
	}
	for i := range plan.Actions {
		action := &plan.Actions[i]
		action.Type = strings.TrimSpace(action.Type)
		action.Title = strings.TrimSpace(action.Title)
		action.Details = strings.TrimSpace(action.Details)
		action.Content = strings.TrimSpace(action.Content)
		action.Kind = strings.TrimSpace(action.Kind)
		action.DueAt = strings.TrimSpace(action.DueAt)
		action.TriggerAt = strings.TrimSpace(action.TriggerAt)
		action.Timezone = strings.TrimSpace(action.Timezone)
		action.DeliveryMethod = strings.TrimSpace(action.DeliveryMethod)
		action.Priority = strings.TrimSpace(action.Priority)
		action.ProjectKey = strings.TrimSpace(action.ProjectKey)
		action.ExpiresAt = strings.TrimSpace(action.ExpiresAt)
	}
	return plan
}

func actionPlanSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"intent", "confidence", "reply", "actions"},
		"properties": map[string]any{
			"intent": map[string]any{
				"type": "string",
				"enum": []string{
					core.IntentReply,
					core.IntentUnknown,
					core.IntentMemorySave,
					core.IntentTaskCreate,
					core.IntentReminderCreate,
				},
			},
			"confidence": map[string]any{
				"type": "number",
			},
			"reply": map[string]any{"type": "string"},
			"actions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"type", "title", "details", "content", "kind", "dueAt", "triggerAt",
						"timezone", "deliveryMethod", "priority", "projectKey", "expiresAt", "confidence",
					},
					"properties": map[string]any{
						"type": map[string]any{
							"type": "string",
							"enum": []string{core.ActionMemorySave, core.ActionTaskCreate, core.ActionReminderCreate},
						},
						"title":          map[string]any{"type": "string"},
						"details":        map[string]any{"type": "string"},
						"content":        map[string]any{"type": "string"},
						"kind":           map[string]any{"type": "string"},
						"dueAt":          map[string]any{"type": "string"},
						"triggerAt":      map[string]any{"type": "string"},
						"timezone":       map[string]any{"type": "string"},
						"deliveryMethod": map[string]any{"type": "string", "enum": []string{"", "web", "telegram"}},
						"priority":       map[string]any{"type": "string", "enum": []string{"", "low", "normal", "high"}},
						"projectKey":     map[string]any{"type": "string"},
						"expiresAt":      map[string]any{"type": "string"},
						"confidence": map[string]any{
							"type": "number",
						},
					},
				},
			},
		},
	}
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

func (c *Client) TranscribeAudio(ctx context.Context, audio []byte, filename, contentType, language string) (core.ModelResponse, error) {
	if len(audio) == 0 {
		return core.ModelResponse{}, errors.New("audio is empty")
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "telegram-voice.oga"
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "audio/ogg"
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = "uk"
	}
	response, err := c.client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:           openai.File(bytes.NewReader(audio), filename, contentType),
		Model:          openai.AudioModel(c.transcriptionModel),
		Language:       openai.String(language),
		Prompt:         openai.String("Українська голосова нотатка для персонального асистента NOVA."),
		Temperature:    openai.Float(0),
		ResponseFormat: openai.AudioResponseFormatJSON,
	})
	if err != nil {
		return core.ModelResponse{}, err
	}
	return core.ModelResponse{
		Text: strings.TrimSpace(response.Text),
		Usage: core.NovaUsage{
			Feature:      "telegram.voice.transcription",
			Model:        c.transcriptionModel,
			InputTokens:  int(response.Usage.InputTokens),
			OutputTokens: int(response.Usage.OutputTokens),
		},
	}, nil
}
