package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stub stands in for api.elevenlabs.io.
func stub(t *testing.T, synthCalls *int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/voices", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "k" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"detail":{"status":"invalid_api_key"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"voices": []map[string]any{
			{"voice_id": "v1", "name": "Rachel", "labels": map[string]string{"accent": "american", "gender": "female"}},
			{"voice_id": "v2", "name": "Callum"},
		}})
	})
	mux.HandleFunc("/v1/text-to-speech/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(synthCalls, 1)
		if r.Header.Get("xi-api-key") != "k" {
			w.WriteHeader(401)
			return
		}
		var body struct {
			Text  string `json:"text"`
			Model string `json:"model_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != elevenModel {
			t.Errorf("model: got %q", body.Model)
		}
		if strings.Contains(body.Text, "boom") {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("upstream fell over"))
			return
		}
		// The timestamped endpoint answers JSON: the audio in base64 and when
		// each character is spoken. Here every character takes a tenth of a
		// second.
		voice, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/v1/text-to-speech/"), "/with-timestamps")
		if !ok {
			t.Errorf("path: got %q, want the with-timestamps endpoint", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audio_base64": base64.StdEncoding.EncodeToString([]byte("MP3:" + voice + ":" + body.Text)),
			"alignment":    stubAlignment(body.Text),
		})
	})
	return httptest.NewServer(mux)
}

// stubAlignment times a text the way the stub speaks it: one character
// every 100ms.
func stubAlignment(text string) map[string]any {
	var chars []string
	var starts, ends []float64
	for i, r := range []rune(text) {
		chars = append(chars, string(r))
		starts = append(starts, float64(i)/10)
		ends = append(ends, float64(i+1)/10)
	}
	return map[string]any{
		"characters":                    chars,
		"character_start_times_seconds": starts,
		"character_end_times_seconds":   ends,
	}
}

func TestElevenLabs(t *testing.T) {
	var calls int32
	ts := stub(t, &calls)
	defer ts.Close()
	ctx := context.Background()

	t.Run("voices carry their labels", func(t *testing.T) {
		p := NewElevenLabs("k", ts.URL+"/v1")
		vs, err := p.Voices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 2 || vs[0].Name != "Rachel – american, female" || vs[1].Name != "Callum" {
			t.Errorf("voices: %+v", vs)
		}
	})

	t.Run("bad key is an error with the reason", func(t *testing.T) {
		p := NewElevenLabs("wrong", ts.URL+"/v1")
		_, err := p.Voices(ctx)
		if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "invalid_api_key") {
			t.Errorf("err: %v", err)
		}
	})

	t.Run("synth posts the text in the voice and keeps the timing", func(t *testing.T) {
		p := NewElevenLabs("k", ts.URL+"/v1")
		a, err := p.Synth(ctx, "v1", "hello")
		if err != nil {
			t.Fatal(err)
		}
		if a.ContentType != "audio/mpeg" || string(a.Data) != "MP3:v1:hello" {
			t.Errorf("got %q %q", a.ContentType, a.Data)
		}
		if want := []int{0, 100, 200, 300, 400}; !slices.Equal(a.Alignment, want) {
			t.Errorf("alignment: got %v, want %v", a.Alignment, want)
		}
	})

	t.Run("alignment is in the browser's string units", func(t *testing.T) {
		// "a😀b": the emoji is one character to ElevenLabs and two UTF-16
		// units to the page, so its time appears twice.
		got := alignmentUnits([]string{"a", "😀", "b"}, []float64{0, 0.25, 0.5004})
		if want := []int{0, 250, 250, 500}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		// Mismatched lists: the shorter decides.
		if got := alignmentUnits([]string{"a", "b"}, []float64{0}); !slices.Equal(got, []int{0}) {
			t.Errorf("mismatch: got %v", got)
		}
		if got := alignmentUnits(nil, nil); len(got) != 0 {
			t.Errorf("empty: got %v", got)
		}
	})

	t.Run("a response without alignment is still audio", func(t *testing.T) {
		plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"audio_base64": base64.StdEncoding.EncodeToString([]byte("MP3"))})
		}))
		defer plain.Close()
		a, err := NewElevenLabs("k", plain.URL+"/v1").Synth(ctx, "v1", "hi")
		if err != nil || string(a.Data) != "MP3" || a.Alignment != nil {
			t.Errorf("got %+v, %v", a, err)
		}
		empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"alignment":null}`))
		}))
		defer empty.Close()
		if _, err := NewElevenLabs("k", empty.URL+"/v1").Synth(ctx, "v1", "hi"); err == nil {
			t.Error("no audio should be an error")
		}
	})
}

func TestService(t *testing.T) {
	var calls int32
	ts := stub(t, &calls)
	defer ts.Close()
	ctx := context.Background()
	p := NewElevenLabs("k", ts.URL+"/v1")
	s := NewService([]Provider{p}, 2, 3)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	s.limiter = newBucket(3, 3.0/60, s.now)

	t.Run("providers list voices and cache the list", func(t *testing.T) {
		infos := s.Providers(ctx)
		if len(infos) != 1 || infos[0].ID != "elevenlabs" || len(infos[0].Voices) != 2 || infos[0].Error != "" {
			t.Fatalf("infos: %+v", infos)
		}
		p.key = "wrong" // a later fetch would fail, but the hour isn't up
		if infos := s.Providers(ctx); infos[0].Error != "" {
			t.Errorf("expected cached list, got error %q", infos[0].Error)
		}
		p.key = "k"
	})

	t.Run("speak, then from cache without a second call", func(t *testing.T) {
		a, err := s.Speak(ctx, "elevenlabs", "v1", "hello")
		if err != nil {
			t.Fatal(err)
		}
		if a.Cached || string(a.Data) != "MP3:v1:hello" || a.ETag != ETag("elevenlabs", "v1", "hello") {
			t.Errorf("first: %+v", a)
		}
		b, err := s.Speak(ctx, "elevenlabs", "v1", "hello")
		if err != nil {
			t.Fatal(err)
		}
		if !b.Cached || atomic.LoadInt32(&calls) != 1 {
			t.Errorf("second: cached=%v calls=%d", b.Cached, calls)
		}
		// A different voice is a different utterance.
		if c, _ := s.Speak(ctx, "elevenlabs", "v2", "hello"); c.Cached || c.ETag == a.ETag {
			t.Errorf("voice not part of identity: %+v", c)
		}
	})

	t.Run("cache evicts the least recent", func(t *testing.T) {
		// Cache holds 2: "v1 hello" (touched) and "v2 hello". A third evicts
		// the older of them.
		_, _ = s.Speak(ctx, "elevenlabs", "v1", "hello") // touch
		if _, err := s.Speak(ctx, "elevenlabs", "v1", "third"); err != nil {
			t.Fatal(err)
		}
		if a, _ := s.Speak(ctx, "elevenlabs", "v1", "hello"); !a.Cached {
			t.Error("recently used entry was evicted")
		}
		// The rate limit has been spent (3 misses); a miss now is refused.
		if _, err := s.Speak(ctx, "elevenlabs", "v2", "hello"); !errors.Is(err, ErrRateLimit) {
			t.Errorf("expected rate limit, got %v", err)
		}
		// ...but a hit is still served.
		if a, err := s.Speak(ctx, "elevenlabs", "v1", "third"); err != nil || !a.Cached {
			t.Errorf("hit under rate limit: %v %+v", err, a)
		}
		now = now.Add(time.Minute)
		if _, err := s.Speak(ctx, "elevenlabs", "v2", "hello"); err != nil {
			t.Errorf("after refill: %v", err)
		}
	})

	t.Run("validation", func(t *testing.T) {
		if _, err := s.Speak(ctx, "nope", "v1", "x"); !errors.Is(err, ErrNoProvider) {
			t.Error(err)
		}
		if _, err := s.Speak(ctx, "elevenlabs", "v1", ""); !errors.Is(err, ErrEmpty) {
			t.Error(err)
		}
		if _, err := s.Speak(ctx, "elevenlabs", "v1", strings.Repeat("x", MaxTextLen+1)); !errors.Is(err, ErrTooLong) {
			t.Error(err)
		}
		now = now.Add(time.Minute)
		if _, err := s.Speak(ctx, "elevenlabs", "v1", "boom"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("upstream failure: %v", err)
		}
	})
}

func TestElevenLabsKeyFromFile(t *testing.T) {
	t.Setenv(ElevenLabsKeyEnv, "")
	t.Setenv("HOME", t.TempDir())
	if k, src := ElevenLabsKey(); k != "" || src != "" {
		t.Errorf("no file: got %q from %q", k, src)
	}
	path := SecretsPath()
	if err := writeFile(path, "{\"elevenlabs_api_key\":\"from-file\\n\"}"); err != nil {
		t.Fatal(err)
	}
	if k, src := ElevenLabsKey(); k != "from-file" || src != path {
		t.Errorf("file: got %q from %q", k, src)
	}
	t.Setenv(ElevenLabsKeyEnv, "from-env\n")
	if k, src := ElevenLabsKey(); k != "from-env" || src != "$"+ElevenLabsKeyEnv {
		t.Errorf("env wins: got %q from %q", k, src)
	}
}

// The key goes to ElevenLabs and nowhere else: not into logs, not into
// error messages, not into a formatted provider, and not across a redirect.
func TestKeyNeverLeaks(t *testing.T) {
	const secret = "sk-very-secret-key-value"
	leaked := func(what, s string) {
		t.Helper()
		if strings.Contains(s, secret) {
			t.Errorf("%s contains the key: %q", what, s)
		}
	}

	t.Run("formatting a provider", func(t *testing.T) {
		p := NewElevenLabs(secret, "")
		leaked("%v", fmt.Sprintf("%v", p))
		leaked("%+v", fmt.Sprintf("%+v", p))
		leaked("%#v", fmt.Sprintf("%#v", p))
		leaked("%s", fmt.Sprintf("%s", p))
		leaked("%v of struct", fmt.Sprintf("%v", *p))
	})

	t.Run("errors from the API", func(t *testing.T) {
		var calls int32
		ts := stub(t, &calls)
		defer ts.Close()
		p := NewElevenLabs(secret, ts.URL+"/v1")
		if _, err := p.Voices(context.Background()); err == nil {
			t.Fatal("expected a 401")
		} else {
			leaked("voices error", err.Error())
		}
		if _, err := p.Synth(context.Background(), "v1", "hi"); err == nil {
			t.Fatal("expected a 401")
		} else {
			leaked("synth error", err.Error())
		}
		p = NewElevenLabs(secret, "http://127.0.0.1:1/v1")
		if _, err := p.Voices(context.Background()); err == nil {
			t.Fatal("expected a connection error")
		} else {
			leaked("transport error", err.Error())
		}
	})

	t.Run("a redirect is not followed", func(t *testing.T) {
		var gotKey atomic.Value
		gotKey.Store("")
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKey.Store(r.Header.Get("xi-api-key"))
			_, _ = w.Write([]byte(`{"voices":[]}`))
		}))
		defer elsewhere.Close()
		eleven := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
		}))
		defer eleven.Close()
		p := NewElevenLabs(secret, eleven.URL+"/v1")
		_, err := p.Voices(context.Background())
		if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
			t.Errorf("redirect should surface as an error, got %v", err)
		}
		if gotKey.Load().(string) != "" {
			t.Error("the key followed the redirect to another host")
		}
	})

	t.Run("reading the key logs the source, not the value", func(t *testing.T) {
		t.Setenv(ElevenLabsKeyEnv, "")
		t.Setenv("HOME", t.TempDir())
		var logs bytes.Buffer
		log.SetOutput(&logs)
		defer log.SetOutput(os.Stderr)
		path := SecretsPath()
		if err := writeFile(path, `{"elevenlabs_api_key":"`+secret+`"}`); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(path, 0o644) // provokes the mode warning
		if k, _ := ElevenLabsKey(); k != secret {
			t.Fatalf("got %q", k)
		}
		if !strings.Contains(logs.String(), "chmod 600") {
			t.Errorf("expected the mode warning, got %q", logs.String())
		}
		// A malformed file logs a fixed message, not the decoder's quote of it.
		if err := writeFile(path, secret); err != nil {
			t.Fatal(err)
		}
		_, _ = ElevenLabsKey()
		leaked("log output", logs.String())
	})
}
