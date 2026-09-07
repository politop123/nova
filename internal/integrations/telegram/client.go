package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.telegram.org"
const defaultFileBase = "https://api.telegram.org/file"
const maxVoiceBytes = 24 * 1024 * 1024

type Client struct {
	token      string
	apiBase    string
	fileBase   string
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithBaseURLs(apiBase, fileBase string) Option {
	return func(c *Client) {
		if strings.TrimSpace(apiBase) != "" {
			c.apiBase = strings.TrimRight(apiBase, "/")
		}
		if strings.TrimSpace(fileBase) != "" {
			c.fileBase = strings.TrimRight(fileBase, "/")
		}
	}
}

func NewClient(token string, opts ...Option) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN is not configured")
	}
	client := &Client{
		token:    token,
		apiBase:  defaultAPIBase,
		fileBase: defaultFileBase,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	Voice     *Voice `json:"voice"`
	Caption   string `json:"caption"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type"`
	FileSize     int    `json:"file_size"`
}

type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int    `json:"file_size"`
	FilePath     string `json:"file_path"`
}

type VoiceDownload struct {
	Data        []byte
	Filename    string
	ContentType string
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	return c.call(ctx, "sendMessage", payload, nil)
}

func (c *Client) DownloadVoice(ctx context.Context, fileID string) (VoiceDownload, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return VoiceDownload{}, errors.New("voice file_id is required")
	}
	var result File
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &result); err != nil {
		return VoiceDownload{}, err
	}
	if result.FilePath == "" {
		return VoiceDownload{}, errors.New("telegram did not return file_path")
	}
	fileURL, err := url.JoinPath(c.fileBase, "bot"+c.token, result.FilePath)
	if err != nil {
		return VoiceDownload{}, fmt.Errorf("build telegram file URL: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return VoiceDownload{}, fmt.Errorf("create telegram file request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return VoiceDownload{}, fmt.Errorf("download telegram voice: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return VoiceDownload{}, fmt.Errorf("download telegram voice: status %d", response.StatusCode)
	}
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, io.LimitReader(response.Body, maxVoiceBytes+1)); err != nil {
		return VoiceDownload{}, fmt.Errorf("read telegram voice: %w", err)
	}
	if buffer.Len() > maxVoiceBytes {
		return VoiceDownload{}, fmt.Errorf("telegram voice is larger than %d bytes", maxVoiceBytes)
	}
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "audio/ogg"
	}
	return VoiceDownload{
		Data:        buffer.Bytes(),
		Filename:    path.Base(result.FilePath),
		ContentType: contentType,
	}, nil
}

func (c *Client) call(ctx context.Context, method string, payload any, result any) error {
	endpoint, err := url.JoinPath(c.apiBase, "bot"+c.token, method)
	if err != nil {
		return fmt.Errorf("build telegram API URL: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode telegram request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("telegram API request failed: %w", err)
	}
	defer response.Body.Close()

	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode telegram response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.OK {
		if envelope.Description == "" {
			envelope.Description = fmt.Sprintf("status %d", response.StatusCode)
		}
		return fmt.Errorf("telegram API %s failed: %s", method, envelope.Description)
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode telegram result: %w", err)
		}
	}
	return nil
}
