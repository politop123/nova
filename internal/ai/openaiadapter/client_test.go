package openaiadapter

import (
	"testing"

	"nova.local/core/internal/core"
)

func TestNormalizeTranscriptionUploadMetadataRenamesTelegramOGA(t *testing.T) {
	filename, contentType := normalizeTranscriptionUploadMetadata("voice/file_12.oga", "application/octet-stream")
	if filename != "voice/file_12.ogg" {
		t.Fatalf("filename = %q, want voice/file_12.ogg", filename)
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
