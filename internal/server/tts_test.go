package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MaxInertia/unfold/internal/indexer"
	"github.com/MaxInertia/unfold/internal/tts"
)

// A stand-in for ElevenLabs: one voice, and audio that echoes the text,
// answered the way the timestamped endpoint does — JSON with the audio in
// base64 and a start time per character (here, one every 100ms).
func elevenStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/voices", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"voices":[{"voice_id":"v1","name":"Rachel"}]}`))
	})
	mux.HandleFunc("/v1/text-to-speech/", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Text string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		var chars []string
		var starts []float64
		for i, c := range body.Text {
			chars = append(chars, string(c))
			starts = append(starts, float64(i)/10)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audio_base64": base64.StdEncoding.EncodeToString([]byte("MP3:" + body.Text)),
			"alignment":    map[string]any{"characters": chars, "character_start_times_seconds": starts},
		})
	})
	return httptest.NewServer(mux)
}

func TestTTSEndpoint(t *testing.T) {
	idx := indexer.New()
	if err := idx.Load("", "github.com/MaxInertia/unfold/internal/tours"); err != nil {
		t.Fatalf("indexer.Load: %v", err)
	}

	t.Run("unconfigured", func(t *testing.T) {
		srv := New(idx)
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()
		var resp struct{ Providers []tts.ProviderInfo }
		getJSON(t, ts.URL+"/api/tts", http.StatusOK, &resp)
		if len(resp.Providers) != 0 {
			t.Errorf("providers: %+v", resp.Providers)
		}
		res, _ := http.Post(ts.URL+"/api/tts", "application/json", strings.NewReader(`{"provider":"elevenlabs","voice":"v1","text":"hi"}`))
		if res.StatusCode != http.StatusNotImplemented {
			t.Errorf("POST without a provider: %d", res.StatusCode)
		}
	})

	eleven := elevenStub(t)
	defer eleven.Close()
	srv := New(idx)
	srv.SetTTS(tts.NewService([]tts.Provider{tts.NewElevenLabs("k", eleven.URL+"/v1")}, 8, 30))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("lists voices", func(t *testing.T) {
		var resp struct{ Providers []tts.ProviderInfo }
		getJSON(t, ts.URL+"/api/tts", http.StatusOK, &resp)
		if len(resp.Providers) != 1 || resp.Providers[0].ID != "elevenlabs" || len(resp.Providers[0].Voices) != 1 {
			t.Errorf("providers: %+v", resp.Providers)
		}
	})

	post := func(body string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/tts", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	t.Run("synthesizes, then serves from cache", func(t *testing.T) {
		res := post(`{"provider":"elevenlabs","voice":"v1","text":"hello"}`, nil)
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || string(body) != "MP3:hello" || res.Header.Get("Content-Type") != "audio/mpeg" {
			t.Fatalf("first: %d %q %q", res.StatusCode, body, res.Header.Get("Content-Type"))
		}
		etag := res.Header.Get("ETag")
		if etag == "" || res.Header.Get("X-Cache") != "" {
			t.Errorf("first: etag=%q x-cache=%q", etag, res.Header.Get("X-Cache"))
		}
		// The word timing rides in a header, in milliseconds per character.
		if al := res.Header.Get("X-Alignment"); al != "[0,100,200,300,400]" {
			t.Errorf("first: X-Alignment=%q", al)
		}
		res = post(`{"provider":"elevenlabs","voice":"v1","text":"hello"}`, nil)
		if res.Header.Get("X-Cache") != "hit" {
			t.Errorf("second: X-Cache=%q", res.Header.Get("X-Cache"))
		}
		if al := res.Header.Get("X-Alignment"); al != "[0,100,200,300,400]" {
			t.Errorf("cached: X-Alignment=%q", al)
		}
		res = post(`{"provider":"elevenlabs","voice":"v1","text":"hello"}`, map[string]string{"If-None-Match": etag})
		if res.StatusCode != http.StatusNotModified {
			t.Errorf("If-None-Match: %d", res.StatusCode)
		}
	})

	t.Run("guards", func(t *testing.T) {
		if res := post(`{"provider":"elevenlabs","voice":"v1","text":"x"}`, map[string]string{"Sec-Fetch-Site": "cross-site"}); res.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site: %d", res.StatusCode)
		}
		if res := post(`{"provider":"nope","voice":"v1","text":"x"}`, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("unknown provider: %d", res.StatusCode)
		}
		if res := post(`{"provider":"elevenlabs","voice":"v1","text":""}`, nil); res.StatusCode != http.StatusBadRequest {
			t.Errorf("empty: %d", res.StatusCode)
		}
		long, _ := json.Marshal(map[string]string{"provider": "elevenlabs", "voice": "v1", "text": strings.Repeat("x", tts.MaxTextLen+1)})
		if res := post(string(long), nil); res.StatusCode != http.StatusBadRequest {
			t.Errorf("too long: %d", res.StatusCode)
		}
	})
}
