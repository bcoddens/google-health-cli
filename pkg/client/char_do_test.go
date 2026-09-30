// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A 429 response must retry and honor the Retry-After header value even when
// it exceeds the exponential backoff that would otherwise apply.
func TestCharDo_TooManyRequestsHonorsRetryAfterOverBackoff(t *testing.T) {
	ts := &fakeTokenSource{token: "tok"}
	attempts := 0
	var sleptDurations []time.Duration
	orig := sleep
	sleep = func(d time.Duration) { sleptDurations = append(sleptDurations, d) }
	t.Cleanup(func() { sleep = orig })

	c := newTestClient(ts, func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			h := http.Header{}
			h.Set("Retry-After", "5") // exceeds the 1s backoff for attempt 1
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":429,"message":"rate limited"}}`)),
				Header:     h,
			}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	})

	resp, err := c.Do(&Request{Method: "GET", Path: "/x"})
	if err != nil {
		t.Fatalf("expected success on retry, got %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	if len(sleptDurations) != 1 || sleptDurations[0] != 5*time.Second {
		t.Errorf("slept %v, want a single 5s sleep (Retry-After overrides backoff)", sleptDurations)
	}
}

// A persistent 429 exhausts retries and keeps the API error classification.
func TestCharDo_PersistentTooManyRequestsExhaustsRetries(t *testing.T) {
	stubSleep(t)
	attempts := 0
	c := newTestClient(&fakeTokenSource{token: "tok"}, func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: 429,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":429,"message":"rate limited"}}`)),
			Header:     http.Header{},
		}, nil
	})

	_, err := c.Do(&Request{Method: "GET", Path: "/x"})
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("got %T (%v), want *CLIError", err, err)
	}
	if cliErr.Code != ExitAPIError {
		t.Errorf("exit code = %d, want %d (api)", cliErr.Code, ExitAPIError)
	}
	if attempts != MaxRetries+1 {
		t.Errorf("attempts = %d, want %d", attempts, MaxRetries+1)
	}
}

// A transport-level failure reaching the OAuth token endpoint is reported as
// a network error (exit 4), distinct from a rejected/invalid credential.
func TestCharDo_TokenEndpointNetworkFailureExitsFour(t *testing.T) {
	stubSleep(t)
	ts := &fakeTokenSource{err: &url.Error{Op: "Post", URL: "https://oauth2.googleapis.com/token", Err: errors.New("dial tcp: connection refused")}}
	c := newTestClient(ts, func(*http.Request) (*http.Response, error) {
		t.Fatal("transport should never be reached")
		return nil, nil
	})

	_, err := c.Do(&Request{Method: "GET", Path: "/x"})
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("got %T (%v), want *CLIError", err, err)
	}
	if cliErr.Code != ExitNetworkError {
		t.Errorf("exit code = %d, want %d (network)", cliErr.Code, ExitNetworkError)
	}
	if cliErr.Type != "network" {
		t.Errorf("type = %q, want network", cliErr.Type)
	}
	if !strings.Contains(cliErr.Message, "could not reach the OAuth token endpoint") {
		t.Errorf("message = %q, want the token-endpoint network hint", cliErr.Message)
	}
	if ts.calls != 1 {
		t.Errorf("token source called %d times, want 1 (no retry on deterministic failure)", ts.calls)
	}
}

// A cancelled/expired context surfaces from the underlying http.Client.Do as
// a plain transport error and is retried like any other network failure,
// exhausting retries and mapping to exit 4.
func TestCharDo_ContextCanceledExitsFourAfterRetries(t *testing.T) {
	stubSleep(t)
	attempts := 0
	c := newTestClient(&fakeTokenSource{token: "tok"}, func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, &url.Error{Op: "Get", URL: "https://health.googleapis.com/v4/x", Err: context.Canceled}
	})

	_, err := c.Do(&Request{Method: "GET", Path: "/x"})
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("got %T (%v), want *CLIError", err, err)
	}
	if cliErr.Code != ExitNetworkError {
		t.Errorf("exit code = %d, want %d (network)", cliErr.Code, ExitNetworkError)
	}
	if attempts != MaxRetries+1 {
		t.Errorf("attempts = %d, want %d", attempts, MaxRetries+1)
	}
}

// doOnce must set an explicit request ContentType verbatim when provided,
// building the Content-Type header from the request field rather than
// defaulting to application/json.
func TestCharDoOnce_ExplicitContentTypeOverridesDefault(t *testing.T) {
	ts := &fakeTokenSource{token: "tok"}
	var gotContentType string
	c := newTestClient(ts, func(r *http.Request) (*http.Response, error) {
		gotContentType = r.Header.Get("Content-Type")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})

	_, err := c.Do(&Request{Method: "POST", Path: "/x", Body: []byte(`x`), ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotContentType != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain (explicit override)", gotContentType)
	}
}

// doOnce must default to application/json when a body is present but no
// ContentType was specified.
func TestCharDoOnce_DefaultsJSONContentTypeWithBody(t *testing.T) {
	ts := &fakeTokenSource{token: "tok"}
	var gotContentType string
	c := newTestClient(ts, func(r *http.Request) (*http.Response, error) {
		gotContentType = r.Header.Get("Content-Type")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})

	_, err := c.Do(&Request{Method: "POST", Path: "/x", Body: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json default", gotContentType)
	}
}

// doOnce must append the URL-encoded query string when Query is set.
func TestCharDoOnce_EncodesQueryParams(t *testing.T) {
	ts := &fakeTokenSource{token: "tok"}
	var gotURL string
	c := newTestClient(ts, func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})

	_, err := c.Do(&Request{Method: "GET", Path: "/x", Query: url.Values{"a": []string{"1"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotURL, "?a=1") {
		t.Errorf("url = %q, want query string appended", gotURL)
	}
}

// The request body must be replayed identically on every retry attempt (the
// reason Request.Body is []byte rather than a one-shot io.Reader).
func TestCharDo_BodyReplayedIdenticallyAcrossRetries(t *testing.T) {
	stubSleep(t)
	attempts := 0
	var seenBodies []string
	c := newTestClient(&fakeTokenSource{token: "tok"}, func(r *http.Request) (*http.Response, error) {
		attempts++
		b, _ := io.ReadAll(r.Body)
		seenBodies = append(seenBodies, string(b))
		if attempts < 3 {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"error":{"code":503,"message":"unavailable"}}`)), Header: http.Header{}}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})

	_, err := c.Do(&Request{Method: "POST", Path: "/x", Body: []byte(`{"payload":"unchanged"}`)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seenBodies) != 3 {
		t.Fatalf("saw %d attempts, want 3", len(seenBodies))
	}
	for i, b := range seenBodies {
		if b != `{"payload":"unchanged"}` {
			t.Errorf("attempt %d body = %q, want unchanged payload", i, b)
		}
	}
}
