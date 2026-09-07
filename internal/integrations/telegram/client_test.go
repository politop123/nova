package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendMessagePostsTelegramPayload(t *testing.T) {
	var gotPath string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()

	client, err := NewClient("test-token", WithBaseURLs(server.URL, server.URL+"/file"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.SendMessage(t.Context(), 42, "Привіт"); err != nil {
		t.Fatalf("send message: %v", err)
	}
	if gotPath != "/bottest-token/sendMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotPayload["text"] != "Привіт" || gotPayload["chat_id"].(float64) != 42 {
		t.Fatalf("payload = %#v", gotPayload)
	}
}

func TestDownloadVoiceGetsFileAndDownloadsContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/bottest-token/getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"voice-id","file_path":"voice/file.oga"}}`))
		case r.URL.Path == "/file/bottest-token/voice/file.oga":
			w.Header().Set("Content-Type", "audio/ogg")
			_, _ = w.Write([]byte("voice-bytes"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient("test-token", WithBaseURLs(server.URL, server.URL+"/file"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	got, err := client.DownloadVoice(t.Context(), "voice-id")
	if err != nil {
		t.Fatalf("download voice: %v", err)
	}
	if string(got.Data) != "voice-bytes" {
		t.Fatalf("data = %q", string(got.Data))
	}
	if got.Filename != "file.oga" {
		t.Fatalf("filename = %q", got.Filename)
	}
	if !strings.HasPrefix(got.ContentType, "audio/ogg") {
		t.Fatalf("content type = %q", got.ContentType)
	}
}
