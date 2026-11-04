package tts

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// Where the ElevenLabs key comes from, in order: the environment, then the
// personal secrets file beside the personal recognizer rules. The file is
// the way to have the key without exporting it in every shell; it should
// be mode 0600, and a looser mode is logged rather than refused, since
// the reader may not be able to fix it right now.
const (
	ElevenLabsKeyEnv = "ELEVENLABS_API_KEY"
	// ElevenLabsURLEnv overrides the API base URL — for pointing at a stub.
	ElevenLabsURLEnv = "UNFOLD_ELEVENLABS_URL"
	secretsFile      = "secrets.json"
)

type secrets struct {
	ElevenLabsAPIKey string `json:"elevenlabs_api_key"`
}

// SecretsPath is the personal secrets file; empty without a home directory.
func SecretsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "unfold", secretsFile)
}

// ElevenLabsKey returns the configured key, or "" when there is none.
func ElevenLabsKey() string {
	if k := os.Getenv(ElevenLabsKeyEnv); k != "" {
		return k
	}
	path := SecretsPath()
	if path == "" {
		return ""
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		log.Printf("tts: %s is readable by others (mode %o); chmod 600 it", path, fi.Mode().Perm())
	}
	var s secrets
	if err := json.Unmarshal(buf, &s); err != nil {
		log.Printf("tts: %s: %v", path, err)
		return ""
	}
	return s.ElevenLabsAPIKey
}

// FromEnvironment builds the service from whatever keys are configured.
// Nil when no provider is: the endpoint then reports no providers and the
// UI offers the browser voice alone.
func FromEnvironment() *Service {
	var providers []Provider
	if k := ElevenLabsKey(); k != "" {
		providers = append(providers, NewElevenLabs(k, os.Getenv(ElevenLabsURLEnv)))
	}
	if len(providers) == 0 {
		return nil
	}
	return NewService(providers, 64, 30)
}
