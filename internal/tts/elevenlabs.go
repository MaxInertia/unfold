package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ElevenLabs speaks through api.elevenlabs.io. Two calls: the account's
// voice list, and text-to-speech in one of them. Flash v2.5 is the model:
// the lowest latency they offer and half the credits of Multilingual v2,
// which is the right trade for a sentence or two per stop.
type ElevenLabs struct {
	Key     string
	BaseURL string // "https://api.elevenlabs.io/v1" unless overridden (tests)
	Model   string
	Client  *http.Client
}

const (
	elevenBase  = "https://api.elevenlabs.io/v1"
	elevenModel = "eleven_flash_v2_5"
	// 64 kbps mp3: fine for speech, a quarter the bytes of the default.
	elevenFormat = "mp3_44100_64"
)

// NewElevenLabs builds the provider. baseURL may be empty for the real
// service.
func NewElevenLabs(key, baseURL string) *ElevenLabs {
	if baseURL == "" {
		baseURL = elevenBase
	}
	return &ElevenLabs{
		Key:     key,
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   elevenModel,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *ElevenLabs) ID() string    { return "elevenlabs" }
func (e *ElevenLabs) Label() string { return "ElevenLabs" }

func (e *ElevenLabs) Voices(ctx context.Context) ([]Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+"/voices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", e.Key)
	body, _, err := e.do(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Voices []struct {
			ID       string            `json:"voice_id"`
			Name     string            `json:"name"`
			Category string            `json:"category"`
			Labels   map[string]string `json:"labels"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("elevenlabs voices: %w", err)
	}
	out := make([]Voice, 0, len(resp.Voices))
	for _, v := range resp.Voices {
		name := v.Name
		// "Rachel – calm, American" reads better in a dropdown than the
		// bare name when the labels are there.
		var tags []string
		for _, k := range []string{"accent", "description", "gender"} {
			if t := v.Labels[k]; t != "" {
				tags = append(tags, t)
			}
		}
		if len(tags) > 0 {
			name += " – " + strings.Join(tags, ", ")
		}
		out = append(out, Voice{ID: v.ID, Name: name})
	}
	return out, nil
}

func (e *ElevenLabs) Synth(ctx context.Context, voiceID, text string) ([]byte, string, error) {
	if voiceID == "" {
		return nil, "", fmt.Errorf("elevenlabs: a voice is required")
	}
	payload, _ := json.Marshal(map[string]any{"text": text, "model_id": e.Model})
	u := fmt.Sprintf("%s/text-to-speech/%s?output_format=%s", e.BaseURL, url.PathEscape(voiceID), elevenFormat)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("xi-api-key", e.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	body, ctype, err := e.do(req)
	if err != nil {
		return nil, "", err
	}
	if ctype == "" {
		ctype = "audio/mpeg"
	}
	return body, ctype, nil
}

// do runs the request and turns a non-2xx into an error carrying the
// status and the start of the body, which is where ElevenLabs puts the
// reason ("invalid_api_key", "quota_exceeded").
func (e *ElevenLabs) do(req *http.Request) ([]byte, string, error) {
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("elevenlabs: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, "", fmt.Errorf("elevenlabs: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return nil, "", fmt.Errorf("elevenlabs: HTTP %d: %s", resp.StatusCode, snippet)
	}
	return body, resp.Header.Get("Content-Type"), nil
}
