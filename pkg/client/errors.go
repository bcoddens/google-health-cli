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
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

// Exit codes.
const (
	ExitSuccess      = 0
	ExitAPIError     = 1
	ExitAuthError    = 2
	ExitValidation   = 3
	ExitNetworkError = 4
	ExitConfigError  = 5
)

// CLIError is a structured error with exit code, hint, and optional next steps.
//
// NextSteps is a copy-pastable, machine-discoverable list of actions a user or
// agent should take to recover. It is meant to be relayed verbatim to an end
// user — e.g. "Open https://...", "Run: ghealth setup --client-secret /path".
// Use it on errors where the recovery is multi-step and a single Hint isn't
// enough (typically: missing OAuth client_secret).
type CLIError struct {
	Type      string   `json:"type"`
	Code      int      `json:"code"`
	Status    int      `json:"status,omitempty"`
	Message   string   `json:"message"`
	Hint      string   `json:"hint,omitempty"`
	NextSteps []string `json:"next_steps,omitempty"`
}

// WithNextSteps attaches a recovery checklist and returns the same error so
// callers can chain: return NewConfigError(...).WithNextSteps(steps).
func (e *CLIError) WithNextSteps(steps []string) *CLIError {
	e.NextSteps = steps
	return e
}

func (e *CLIError) Error() string {
	return e.Message
}

func (e *CLIError) ExitCode() int {
	return e.Code
}

// AsCLIError reports whether err is, or wraps, a *CLIError.
func AsCLIError(err error) (*CLIError, bool) {
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return cliErr, true
	}
	return nil, false
}

// WriteError writes a structured JSON error to stderr and returns the exit code.
func WriteError(e *CLIError) int {
	errJSON, _ := json.MarshalIndent(map[string]*CLIError{"error": e}, "", "  ")
	fmt.Fprintln(os.Stderr, string(errJSON))
	return e.Code
}

// NewAPIError creates an error from an API response.
func NewAPIError(status int, message, hint string) *CLIError {
	return &CLIError{
		Type:    "api",
		Code:    ExitAPIError,
		Status:  status,
		Message: message,
		Hint:    hint,
	}
}

// NewAuthError creates an authentication error.
func NewAuthError(message, hint string) *CLIError {
	return &CLIError{
		Type:    "auth",
		Code:    ExitAuthError,
		Message: message,
		Hint:    hint,
	}
}

// NewValidationError creates a validation error.
func NewValidationError(message, hint string) *CLIError {
	return &CLIError{
		Type:    "validation",
		Code:    ExitValidation,
		Message: message,
		Hint:    hint,
	}
}

// NewNetworkError creates a network error.
func NewNetworkError(message string) *CLIError {
	return &CLIError{
		Type:    "network",
		Code:    ExitNetworkError,
		Message: message,
	}
}

// NewConfigError creates a configuration error.
func NewConfigError(message, hint string) *CLIError {
	return &CLIError{
		Type:    "config",
		Code:    ExitConfigError,
		Message: message,
		Hint:    hint,
	}
}

// AuthError wraps authentication errors.
type AuthError struct {
	Err error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("authentication failed: %v", e.Err)
}

func (e *AuthError) Unwrap() error {
	return e.Err
}

// isAuthNetworkError reports whether a token-acquisition failure was a
// transport-level problem (DNS, TCP, TLS) rather than an OAuth rejection.
// oauth2 wraps server rejections in a typed *oauth2.RetrieveError, which is
// checked first; transport errors in oauth2 v0.28 are flattened with %v into
// the "cannot fetch token" message (the url.Error chain is lost), so a
// message check is the necessary fallback.
func isAuthNetworkError(err error) bool {
	var rerr *oauth2.RetrieveError
	if errors.As(err, &rerr) {
		return false
	}
	var uerr *neturl.Error
	if errors.As(err, &uerr) {
		return true
	}
	return strings.Contains(err.Error(), "cannot fetch token")
}

// parseAPIError extracts structured error info from an API response.
func parseAPIError(resp *Response) *CLIError {
	var apiErr struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}

	if err := json.Unmarshal(resp.Body, &apiErr); err == nil && apiErr.Error.Message != "" {
		hint := hintForAPIError(resp.StatusCode, apiErr.Error.Message)
		return NewAPIError(resp.StatusCode, apiErr.Error.Message, hint)
	}

	return NewAPIError(resp.StatusCode, fmt.Sprintf("API error (HTTP %d): %s", resp.StatusCode, string(resp.Body)), "")
}

// hintForAPIError returns contextual guidance for known API error patterns.
func hintForAPIError(status int, message string) string {
	switch {
	case status == 403:
		return "Check that the Health API is enabled and you have the required OAuth scopes"
	case strings.Contains(message, "INVALID_DATA_POINT_FILTER_DATA_TYPE_MEMBER"):
		return "The filter field is not valid for this data type. " +
			"Civil time fields (civil_start_time, civil_end_time) must NOT have a Z suffix. " +
			"Physical time fields (physical_time, start_time, end_time) MUST have a Z suffix. " +
			"Sleep only supports civil_end_time/end_time (not start_time). " +
			"Use --dry-run to inspect the generated filter expression"
	case strings.Contains(message, "INVALID_DATA_POINT_FILTER_TIMESTAMP_FORMAT"):
		return "The timestamp format is wrong for this filter field. " +
			"Civil time fields use ISO 8601 (e.g. \"2026-03-28\" or \"2026-03-28T00:00:00\", no Z). " +
			"Physical time fields use RFC-3339 (e.g. \"2026-03-28T00:00:00Z\", with Z)"
	case strings.Contains(message, "INVALID_DATA_POINT_FILTER"):
		return "Check your --filter syntax. Filters follow AIP-160: only >= and < comparators, AND to combine. " +
			"Use --dry-run to see the generated filter"
	case isRollupRangeError(message):
		return "Rollup ranges are capped per request: 14 days for heart-rate, total-calories, " +
			"active-minutes, and calories-in-heart-rate-zone; 90 days for other types. " +
			"Split the range into smaller chunks and merge the results"
	}
	return ""
}

// isRollupRangeError matches API messages complaining that a rollup/daily
// rollup range exceeds the per-type cap (backstop for requests built via
// --json, which bypass local range validation).
func isRollupRangeError(message string) bool {
	m := strings.ToLower(message)
	if !strings.Contains(m, "range") {
		return false
	}
	return strings.Contains(m, "exceed") || strings.Contains(m, "too long") ||
		strings.Contains(m, "too large") || strings.Contains(m, "maximum")
}
