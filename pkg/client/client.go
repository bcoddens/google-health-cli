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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ghealth/internal/version"
	"ghealth/pkg/auth"
)

const MaxRetries = 3

var BaseURL = resolveBaseURL()

const defaultBaseURL = "https://health.googleapis.com/v4"

func resolveBaseURL() string {
	if v := os.Getenv("GHEALTH_BASE_URL"); v != "" {
		return v
	}
	return defaultBaseURL
}

// sleep is swappable so tests can run the retry loop without real backoff.
var sleep = time.Sleep

type Client struct {
	httpClient  *http.Client
	tokenSource auth.TokenSource
}

func New(ts auth.TokenSource) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		tokenSource: ts,
	}
}

// Request represents an API request to be executed.
// Body is []byte so it can be re-read across retries safely.
type Request struct {
	Method      string
	Path        string // relative to BaseURL, e.g., "/users/me/dataTypes/steps/dataPoints"
	Query       url.Values
	Body        []byte
	ContentType string
}

// Response wraps the raw API response.
type Response struct {
	StatusCode int
	Body       json.RawMessage
	Headers    http.Header
}

// contentTypeHeader is the HTTP header name for the request/response media type.
const contentTypeHeader = "Content-Type"

// Do executes an API request with auth, retry, and error handling.
func (c *Client) Do(req *Request) (*Response, error) {
	var lastErr error
	var nextDelay time.Duration // server-requested delay (Retry-After) for the next attempt
	refreshed := false          // whether we've already force-refreshed the token

	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			sleep(retryBackoff(attempt, nextDelay))
		}
		nextDelay = 0

		resp, err := c.doOnce(req)
		if err != nil {
			// Token acquisition failures are deterministic — retrying with
			// backoff cannot help. Fail fast.
			if terminalErr, retryable := mapTransportError(err); !retryable {
				return nil, terminalErr
			}
			lastErr = err
			// Network errors: retry
			continue
		}

		outcome := c.classifyResponse(resp, refreshed)
		if outcome.done {
			return outcome.resp, outcome.err
		}
		refreshed = refreshed || outcome.refresh
		nextDelay = outcome.delay
		lastErr = outcome.err
	}

	// Retries exhausted. An HTTP-status error keeps its API classification
	// (exit 1, e.g. persistent 429/5xx); anything else never got an HTTP
	// response and is a network problem (exit 4).
	return nil, finalRetryError(lastErr)
}

// retryBackoff computes the delay before the given retry attempt (1-based),
// honoring a server-requested Retry-After delay when it's longer than the
// exponential backoff that would otherwise apply.
func retryBackoff(attempt int, serverDelay time.Duration) time.Duration {
	backoff := time.Duration(1<<uint(attempt-1)) * time.Second
	if serverDelay > backoff {
		backoff = serverDelay // honor Retry-After when it's longer than our backoff
	}
	return backoff
}

// mapTransportError classifies a doOnce transport-level error. Auth
// (token-acquisition) failures are deterministic and never retried: a
// transport-level failure reaching the token endpoint is a network problem
// (exit 4) — re-login would fail the same way; everything else is an auth
// problem (exit 2) with the documented recovery hint. Any other error is
// retryable (network errors reaching the API itself).
func mapTransportError(err error) (terminalErr error, retryable bool) {
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		return nil, true
	}
	if isAuthNetworkError(err) {
		return NewNetworkError(fmt.Sprintf("could not reach the OAuth token endpoint: %v", authErr.Err)), false
	}
	return NewAuthError(authErr.Error(), "Run 'ghealth auth login' to re-authenticate"), false
}

// retryOutcome is the result of classifying one HTTP response within the
// retry loop: either a final answer for Do to return (done), or state to
// carry into the next attempt.
type retryOutcome struct {
	done    bool
	resp    *Response
	err     error // done: error to return; !done: error to remember as lastErr
	delay   time.Duration
	refresh bool
}

// classifyResponse decides whether a non-transport-error response ends the
// retry loop (success, or a non-retryable 4xx) or should be retried (401/403
// refresh-and-retry, 429/5xx backoff-and-retry).
func (c *Client) classifyResponse(resp *Response, refreshed bool) retryOutcome {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return retryOutcome{done: true, resp: resp}
	}

	// 401: force-refresh the token once (in case it was revoked, or expired
	// mid-retry-sequence while the local expiry was still in the future) and
	// retry. Gated on a flag rather than attempt==0 so a 401 arriving after
	// earlier 5xx/429 retries still triggers a refresh.
	if resp.StatusCode == 401 && !refreshed {
		c.invalidateToken()
		return retryOutcome{err: parseAPIError(resp), refresh: true}
	}

	// 403 "insufficient authentication scopes": an access token can pass the
	// local validity check yet already be expired server-side, because the
	// oauth2 library treats a token as valid until ~10s before its expiry.
	// Within that skew window the API may return this 403 rather than a 401
	// (observed live: the first call after expiry returned it; the next
	// succeeded). A genuine scope gap reproduces identically after a refresh,
	// so one refresh+retry distinguishes a stale token from a real policy
	// denial without masking the latter.
	if resp.StatusCode == 403 && !refreshed && isStaleScopeError(resp) {
		c.invalidateToken()
		return retryOutcome{err: parseAPIError(resp), refresh: true}
	}

	// 429: honor Retry-After, retry. 5xx: retry (also honor Retry-After if
	// the server sent one).
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return retryOutcome{err: parseAPIError(resp), delay: parseRetryAfter(resp.Headers)}
	}

	// 4xx (not 401/429): don't retry
	return retryOutcome{done: true, resp: resp, err: parseAPIError(resp)}
}

// invalidateToken forces the next Token() call to fetch fresh credentials, if
// the configured token source supports it.
func (c *Client) invalidateToken() {
	if inv, ok := c.tokenSource.(auth.Invalidator); ok {
		inv.Invalidate()
	}
}

// finalRetryError converts the last error seen after retries are exhausted
// into the CLIError Do returns: an HTTP-status error keeps its API
// classification (exit 1, e.g. persistent 429/5xx); anything else never got
// an HTTP response and is a network problem (exit 4).
func finalRetryError(lastErr error) error {
	var cliErr *CLIError
	if errors.As(lastErr, &cliErr) {
		return cliErr
	}
	return NewNetworkError(fmt.Sprintf("request failed after %d attempts: %v", MaxRetries+1, lastErr))
}

// isStaleScopeError reports whether a 403 body is the "insufficient
// authentication scopes" variant (ACCESS_TOKEN_SCOPE_INSUFFICIENT) — the form a
// stale access token produces — rather than a policy denial. Only this variant
// earns a one-shot refresh+retry.
func isStaleScopeError(resp *Response) bool {
	body := string(resp.Body)
	return strings.Contains(body, "ACCESS_TOKEN_SCOPE_INSUFFICIENT") ||
		strings.Contains(strings.ToLower(body), "insufficient authentication scopes")
}

// parseRetryAfter returns the delay requested by a Retry-After header, in either
// delta-seconds ("120") or HTTP-date form. Returns 0 when absent or unparseable.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) doOnce(req *Request) (*Response, error) {
	fullURL := BaseURL + req.Path
	if len(req.Query) > 0 {
		fullURL += "?" + req.Query.Encode()
	}

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequest(req.Method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add auth header.
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, &AuthError{Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("x-goog-api-client", "ghealth/"+version.Version)

	if req.ContentType != "" {
		httpReq.Header.Set(contentTypeHeader, req.ContentType)
	} else if len(req.Body) > 0 {
		httpReq.Header.Set(contentTypeHeader, "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       json.RawMessage(body),
		Headers:    resp.Header,
	}, nil
}

// DryRun returns the request details as JSON without executing.
func (c *Client) DryRun(req *Request) (json.RawMessage, error) {
	fullURL := BaseURL + req.Path
	if len(req.Query) > 0 {
		fullURL += "?" + req.Query.Encode()
	}

	params := make(map[string]string)
	for k, v := range req.Query {
		params[k] = strings.Join(v, ",")
	}

	dryRun := map[string]interface{}{
		"method": req.Method,
		"url":    fullURL,
		"headers": map[string]string{
			"Authorization":     "Bearer [REDACTED]",
			"x-goog-api-client": "ghealth/" + version.Version,
			contentTypeHeader:   "application/json",
		},
	}
	if len(params) > 0 {
		dryRun["params"] = params
	}
	if len(req.Body) > 0 {
		dryRun["body"] = json.RawMessage(req.Body)
	}

	data, err := json.MarshalIndent(dryRun, "", "  ")
	return json.RawMessage(data), err
}
