// Package tts turns text into speech through a paid voice, on the server,
// so the browser never holds the key. The web UI reads tour stops aloud
// with the browser's own voice by default; this is the better voice a
// reader can opt into, behind /api/tts.
//
// The service in front of the provider does three things the provider
// shouldn't: it caches audio by content, so re-reading a stop or stepping
// back costs nothing; it rate-limits, so a runaway page can't spend the
// account; and it remembers the voice list for a while, since that is
// slow to fetch and never changes mid-session.
package tts

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Voice is one voice a provider offers.
type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Provider is a speech backend.
type Provider interface {
	ID() string
	Label() string
	Voices(ctx context.Context) ([]Voice, error)
	// Synth returns encoded audio and its MIME type.
	Synth(ctx context.Context, voiceID, text string) ([]byte, string, error)
}

// ProviderInfo is what the UI needs to offer a provider: its voices.
type ProviderInfo struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Voices []Voice `json:"voices"`
	// Error is set, with Voices empty, when the voice list couldn't be
	// fetched — a bad key, usually — so the UI can say so instead of
	// offering nothing.
	Error string `json:"error,omitempty"`
}

// Audio is a synthesized utterance.
type Audio struct {
	Data        []byte
	ContentType string
	// ETag identifies the content (provider, voice, text) so the browser can
	// cache it too.
	ETag   string
	Cached bool
}

// MaxTextLen bounds one request. A tour stop's description is a paragraph;
// anything longer is not a stop and is not worth the credits.
const MaxTextLen = 2000

var (
	ErrNoProvider = errors.New("no such voice provider")
	ErrTooLong    = errors.New("text too long")
	ErrEmpty      = errors.New("nothing to say")
	ErrRateLimit  = errors.New("too many requests")
)

// Service fronts one or more providers.
type Service struct {
	providers []Provider
	byID      map[string]Provider

	mu      sync.Mutex
	cache   map[string]*list.Element // etag → entry
	order   *list.List               // most recent at the front
	maxSize int

	voices    map[string]voicesEntry
	voicesTTL time.Duration

	limiter *bucket
	now     func() time.Time
}

type entry struct {
	etag  string
	audio Audio
}

type voicesEntry struct {
	info ProviderInfo
	at   time.Time
}

// NewService fronts the given providers with a cache of maxCached
// utterances and a rate limit of perMinute requests (bursting to the same
// number).
func NewService(providers []Provider, maxCached, perMinute int) *Service {
	s := &Service{
		byID:      map[string]Provider{},
		cache:     map[string]*list.Element{},
		order:     list.New(),
		maxSize:   maxCached,
		voices:    map[string]voicesEntry{},
		voicesTTL: time.Hour,
		now:       time.Now,
	}
	for _, p := range providers {
		s.providers = append(s.providers, p)
		s.byID[p.ID()] = p
	}
	s.limiter = newBucket(perMinute, float64(perMinute)/60, s.now)
	return s
}

// Providers lists every provider with its voices. The voice list is
// fetched once an hour per provider; a failure is reported on the entry
// and retried on the next call.
func (s *Service) Providers(ctx context.Context) []ProviderInfo {
	out := make([]ProviderInfo, 0, len(s.providers))
	for _, p := range s.providers {
		s.mu.Lock()
		e, ok := s.voices[p.ID()]
		fresh := ok && e.info.Error == "" && s.now().Sub(e.at) < s.voicesTTL
		s.mu.Unlock()
		if fresh {
			out = append(out, e.info)
			continue
		}
		info := ProviderInfo{ID: p.ID(), Label: p.Label(), Voices: []Voice{}}
		if vs, err := p.Voices(ctx); err != nil {
			info.Error = err.Error()
		} else if vs != nil {
			info.Voices = vs
		}
		s.mu.Lock()
		s.voices[p.ID()] = voicesEntry{info: info, at: s.now()}
		s.mu.Unlock()
		out = append(out, info)
	}
	return out
}

// ETag names an utterance: the same provider, voice and text always have
// the same tag, which is what makes the cache (and the browser's) work.
func ETag(provider, voice, text string) string {
	h := sha256.Sum256([]byte(provider + "\x00" + voice + "\x00" + text))
	return `"` + hex.EncodeToString(h[:16]) + `"`
}

// Speak synthesizes text in the given provider's voice, from cache when it
// has been said before. Only a miss counts against the rate limit: a
// cached utterance costs nothing to serve or to the account.
func (s *Service) Speak(ctx context.Context, providerID, voiceID, text string) (Audio, error) {
	p, ok := s.byID[providerID]
	if !ok {
		return Audio{}, ErrNoProvider
	}
	if text == "" {
		return Audio{}, ErrEmpty
	}
	if len(text) > MaxTextLen {
		return Audio{}, ErrTooLong
	}
	etag := ETag(providerID, voiceID, text)
	s.mu.Lock()
	if el, ok := s.cache[etag]; ok {
		s.order.MoveToFront(el)
		a := el.Value.(*entry).audio
		s.mu.Unlock()
		a.Cached = true
		return a, nil
	}
	if !s.limiter.take() {
		s.mu.Unlock()
		return Audio{}, ErrRateLimit
	}
	s.mu.Unlock()

	data, ctype, err := p.Synth(ctx, voiceID, text)
	if err != nil {
		return Audio{}, err
	}
	a := Audio{Data: data, ContentType: ctype, ETag: etag}
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.cache[etag]; ok {
		// Raced with another request for the same text; keep the first.
		s.order.MoveToFront(el)
		return el.Value.(*entry).audio, nil
	}
	el := s.order.PushFront(&entry{etag: etag, audio: a})
	s.cache[etag] = el
	for s.order.Len() > s.maxSize {
		last := s.order.Back()
		s.order.Remove(last)
		delete(s.cache, last.Value.(*entry).etag)
	}
	return a, nil
}

// bucket is a token bucket: capacity tokens, refilled at rate per second.
type bucket struct {
	tokens   float64
	capacity float64
	rate     float64
	last     time.Time
	now      func() time.Time
}

func newBucket(capacity int, perSecond float64, now func() time.Time) *bucket {
	return &bucket{tokens: float64(capacity), capacity: float64(capacity), rate: perSecond, last: now(), now: now}
}

// take spends a token if one is available. Called under Service.mu.
func (b *bucket) take() bool {
	t := b.now()
	b.tokens += t.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
