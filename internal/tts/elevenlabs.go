package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// ElevenLabs speaks through api.elevenlabs.io. Two calls: the account's
// voice list, and text-to-speech in one of them. Flash v2.5 is the model:
// the lowest latency they offer and half the credits of Multilingual v2,
// which is the right trade for a sentence or two per stop.
//
// The key goes exactly one place: the xi-api-key header of requests to
// BaseURL. It is unexported so nothing outside this file can read it, the
// formatters below keep it out of %v and %#v, the client refuses redirects
// so a 3xx can't carry the header to another host, and no error this
// type returns includes it.
type ElevenLabs struct {
	key     string
	BaseURL string // "https://api.elevenlabs.io/v1" unless overridden (tests)
	Model   string
	Client  *http.Client
}

// keyHeader is the one header the key is ever written to.
const keyHeader = "xi-api-key"

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
		key:     key,
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   elevenModel,
		Client: &http.Client{
			Timeout: 30 * time.Second,
			// Go strips Authorization on a cross-host redirect but not custom
			// headers; not following redirects at all is the simple guarantee.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// String and GoString describe the provider without the key, so a %v in a
// log line or a %#v in a test failure never prints it. Value receivers,
// so they apply to the struct as well as a pointer to it.
func (e ElevenLabs) String() string { return "ElevenLabs(" + e.BaseURL + ")" }
func (e ElevenLabs) GoString() string {
	return "&tts.ElevenLabs{BaseURL: " + strconv.Quote(e.BaseURL) + ", key: <redacted>}"
}

func (e *ElevenLabs) ID() string    { return "elevenlabs" }
func (e *ElevenLabs) Label() string { return "ElevenLabs" }

func (e *ElevenLabs) Voices(ctx context.Context) ([]Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+"/voices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(keyHeader, e.key)
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

// Synth asks for the audio with timestamps: the same synthesis, answered
// as JSON with the audio in base64 and when each character is spoken, so
// the page's highlight can land on a name as it is said rather than guess
// from how far along the audio is. The base64 costs a third more bytes on
// the wire between us and ElevenLabs, nothing to the browser, which gets
// the decoded audio and the times as a header.
func (e *ElevenLabs) Synth(ctx context.Context, voiceID, text string) (Audio, error) {
	if voiceID == "" {
		return Audio{}, fmt.Errorf("elevenlabs: a voice is required")
	}
	payload, _ := json.Marshal(map[string]any{"text": text, "model_id": e.Model})
	u := fmt.Sprintf("%s/text-to-speech/%s/with-timestamps?output_format=%s", e.BaseURL, url.PathEscape(voiceID), elevenFormat)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return Audio{}, err
	}
	req.Header.Set(keyHeader, e.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	body, _, err := e.do(req)
	if err != nil {
		return Audio{}, err
	}
	var resp struct {
		Audio     string `json:"audio_base64"`
		Alignment *struct {
			Characters []string  `json:"characters"`
			Starts     []float64 `json:"character_start_times_seconds"`
		} `json:"alignment"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Audio{}, fmt.Errorf("elevenlabs synth: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(resp.Audio)
	if err != nil || len(data) == 0 {
		return Audio{}, fmt.Errorf("elevenlabs synth: no audio in the response")
	}
	// The format asked for is mp3 whatever the JSON envelope says.
	a := Audio{Data: data, ContentType: "audio/mpeg"}
	if resp.Alignment != nil {
		a.Alignment = alignmentUnits(resp.Alignment.Characters, resp.Alignment.Starts)
	}
	return a, nil
}

// alignmentUnits spreads ElevenLabs' per-character start times over the
// text's UTF-16 code units — the browser's index into the string it sent —
// rounded to milliseconds. A character outside the BMP takes two units and
// gets its start time twice. The two lists should be the same length; the
// shorter wins if they aren't, since a time without a character (or the
// reverse) places nothing.
func alignmentUnits(chars []string, starts []float64) []int {
	n := min(len(chars), len(starts))
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		ms := int(math.Round(starts[i] * 1000))
		for range utf16.Encode([]rune(chars[i])) {
			out = append(out, ms)
		}
	}
	return out
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
