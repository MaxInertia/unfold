package tts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("MP3:" + strings.TrimPrefix(r.URL.Path, "/v1/text-to-speech/") + ":" + body.Text))
	})
	return httptest.NewServer(mux)
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

	t.Run("synth posts the text in the voice", func(t *testing.T) {
		p := NewElevenLabs("k", ts.URL+"/v1")
		data, ctype, err := p.Synth(ctx, "v1", "hello")
		if err != nil {
			t.Fatal(err)
		}
		if ctype != "audio/mpeg" || string(data) != "MP3:v1:hello" {
			t.Errorf("got %q %q", ctype, data)
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
		p.Key = "wrong" // a later fetch would fail, but the hour isn't up
		if infos := s.Providers(ctx); infos[0].Error != "" {
			t.Errorf("expected cached list, got error %q", infos[0].Error)
		}
		p.Key = "k"
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
	if k := ElevenLabsKey(); k != "" {
		t.Errorf("no file: got %q", k)
	}
	path := SecretsPath()
	if err := writeFile(path, `{"elevenlabs_api_key":"from-file"}`); err != nil {
		t.Fatal(err)
	}
	if k := ElevenLabsKey(); k != "from-file" {
		t.Errorf("file: got %q", k)
	}
	t.Setenv(ElevenLabsKeyEnv, "from-env")
	if k := ElevenLabsKey(); k != "from-env" {
		t.Errorf("env wins: got %q", k)
	}
}
