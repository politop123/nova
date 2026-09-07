package voice

type Mode string

const (
	Economy Mode = "economy"
	Live    Mode = "live"
)

type SessionOptions struct {
	Mode                Mode
	Language            string
	NativeTTSPref       bool
	SilenceTrimmedLocal bool
}

type Usage struct {
	AudioMinutes      float64
	AudioInputTokens  int
	AudioOutputTokens int
	EstimatedCostUSD  float64
}

// Audio adapters are deliberately reserved for v0.2. The v0.1 core is text-first.
type TranscriptionAdapter interface {
	Transcribe(audio <-chan []byte) (string, error)
}
