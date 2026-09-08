package openaiadapter

import (
	"testing"

	"nova.local/core/internal/core"
)

func TestNormalizeTranscriptionUploadMetadataRenamesTelegramOGA(t *testing.T) {
	filename, contentType := normalizeTranscriptionUploadMetadata("voice/file_12.oga", "application/octet-stream")
	if filename != "file_12.ogg" {
		t.Fatalf("filename = %q, want file_12.ogg", filename)
	}
	if contentType != "audio/ogg" {
		t.Fatalf("contentType = %q, want audio/ogg", contentType)
	}
}

func TestNormalizeTranscriptionUploadMetadataKeepsSupportedName(t *testing.T) {
	filename, contentType := normalizeTranscriptionUploadMetadata("audio.webm", "audio/webm")
	if filename != "audio.webm" || contentType != "audio/webm" {
		t.Fatalf("metadata = %q/%q, want audio.webm/audio/webm", filename, contentType)
	}
}

func TestNormalizeTranscriptionUploadMetadataInfersExtensionFromMimeType(t *testing.T) {
	filename, contentType := normalizeTranscriptionUploadMetadata("telegram-file", "audio/webm; codecs=opus")
	if filename != "telegram-file.webm" || contentType != "audio/webm" {
		t.Fatalf("metadata = %q/%q, want telegram-file.webm/audio/webm", filename, contentType)
	}
}

func TestNormalizeTranscriptionUploadMetadataInfersContentTypeFromExtension(t *testing.T) {
	filename, contentType := normalizeTranscriptionUploadMetadata("voice-note.mp3", "application/octet-stream")
	if filename != "voice-note.mp3" || contentType != "audio/mpeg" {
		t.Fatalf("metadata = %q/%q, want voice-note.mp3/audio/mpeg", filename, contentType)
	}
}

func TestNormalizeTranscriptionUploadMetadataFallsBackToTelegramOgg(t *testing.T) {
	filename, contentType := normalizeTranscriptionUploadMetadata("voice-note.bin", "application/octet-stream")
	if filename != "voice-note.ogg" || contentType != "audio/ogg" {
		t.Fatalf("metadata = %q/%q, want voice-note.ogg/audio/ogg", filename, contentType)
	}
}

func TestActionPlanSchemaAllowsReadOnlyStatusIntents(t *testing.T) {
	schema := actionPlanSchema()
	properties := schema["properties"].(map[string]any)
	intent := properties["intent"].(map[string]any)
	values := intent["enum"].([]string)

	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, want := range []string{core.IntentGitStatus, core.IntentSystemStatus} {
		if !seen[want] {
			t.Fatalf("action plan schema does not allow %q", want)
		}
	}
}
