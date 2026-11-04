package tts

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Where the ElevenLabs key comes from, in order: the environment, then the
// personal secrets file beside the personal recognizer rules. The file is
// the way to have the key without exporting it in every shell; it should
// be mode 0600, and a looser mode is logged rather than refused, since
// the reader may not be able to fix it right now.
//
// The key's value is never logged, never put in an error, and never
// written anywhere: this file reads it and hands it to the provider, and
// the provider sends it to ElevenLabs. What gets logged is *where* it came
// from, so a reader can tell which of the two sources won.
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

// ElevenLabsKey returns the configured key and where it came from (the
// variable name or the file path), or "" when there is none. Surrounding
// whitespace is dropped, since a key pasted into a file usually ends in a
// newline.
func ElevenLabsKey() (key, source string) {
	if k := strings.TrimSpace(os.Getenv(ElevenLabsKeyEnv)); k != "" {
		return k, "$" + ElevenLabsKeyEnv
	}
	path := SecretsPath()
	if path == "" {
		return "", ""
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		log.Printf("tts: %s is readable by others (mode %o); chmod 600 it", path, fi.Mode().Perm())
	}
	var s secrets
	if err := json.Unmarshal(buf, &s); err != nil {
		// Not the decoder's message: it can quote a character of the file.
		log.Printf("tts: %s is not valid JSON; expected {\"elevenlabs_api_key\": \"…\"}", path)
		return "", ""
	}
	if k := strings.TrimSpace(s.ElevenLabsAPIKey); k != "" {
		return k, path
	}
	return "", ""
}

// FromEnvironment builds the service from whatever keys are configured,
// and says where the key was found — for the startup log, which names the
// source and never the value. Nil when no provider is configured: the
// endpoint then reports no providers and the UI offers the browser voice
// alone.
func FromEnvironment() (svc *Service, keySource string) {
	var providers []Provider
	if k, src := ElevenLabsKey(); k != "" {
		providers = append(providers, NewElevenLabs(k, os.Getenv(ElevenLabsURLEnv)))
		keySource = src
	}
	if len(providers) == 0 {
		return nil, ""
	}
	return NewService(providers, 64, 30), keySource
}
