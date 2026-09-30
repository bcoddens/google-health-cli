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

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"ghealth/pkg/auth"
	"ghealth/pkg/client"
	"ghealth/pkg/config"
)

// miscCharCaptureStdout swaps os.Stdout for the duration of fn and returns
// everything written to it. runAuthStatus writes directly to the os.Stdout
// package variable via fmt.Fprintln, so this is the only way to observe it
// without changing the function's output contract.
func miscCharCaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}

func miscCharDecodeJSONLine(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output not JSON: %v\noutput: %s", err, out)
	}
	return m
}

// miscCharIsolateAuth prevents developer credentials from selecting a
// different source than the branch each characterization test intends.
func miscCharIsolateAuth(t *testing.T) {
	t.Helper()
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	t.Setenv("GHEALTH_ACCESS_TOKEN", "")
	t.Setenv("GHEALTH_CREDENTIALS_FILE", "")
}

// TestMiscCharAuthStatus_EnvToken_NoValidate pins the "GHEALTH_ACCESS_TOKEN
// set, --validate not passed" shape: configured but not authenticated, with
// the note steering toward --validate.
func TestMiscCharAuthStatus_EnvToken_NoValidate(t *testing.T) {
	miscCharIsolateAuth(t)
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	t.Setenv("GHEALTH_ACCESS_TOKEN", "fake-token-value")
	authStatusValidate = false
	defer func() { authStatusValidate = false }()

	out := miscCharCaptureStdout(t, func() {
		if err := runAuthStatus(authStatusCmd, nil); err != nil {
			t.Fatalf("runAuthStatus: %v", err)
		}
	})

	result := miscCharDecodeJSONLine(t, out)
	if result["auth_method"] != "env_token" {
		t.Errorf("auth_method = %v, want env_token", result["auth_method"])
	}
	if result["configured"] != true {
		t.Errorf("configured = %v, want true", result["configured"])
	}
	if result["validated"] != false {
		t.Errorf("validated = %v, want false", result["validated"])
	}
	wantNote := "GHEALTH_ACCESS_TOKEN is set; pass --validate to verify it against Google's tokeninfo endpoint"
	if result["note"] != wantNote {
		t.Errorf("note = %v, want %q", result["note"], wantNote)
	}
	if _, ok := result["authenticated"]; ok {
		t.Errorf("authenticated should be absent without --validate, got %v", result["authenticated"])
	}
}

// TestMiscCharAuthStatus_CredentialsFile_NoValidate pins the
// GHEALTH_CREDENTIALS_FILE-without---validate shape.
func TestMiscCharAuthStatus_CredentialsFile_NoValidate(t *testing.T) {
	miscCharIsolateAuth(t)
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	credPath := t.TempDir() + "/creds.json"
	t.Setenv("GHEALTH_CREDENTIALS_FILE", credPath)
	authStatusValidate = false
	defer func() { authStatusValidate = false }()

	out := miscCharCaptureStdout(t, func() {
		if err := runAuthStatus(authStatusCmd, nil); err != nil {
			t.Fatalf("runAuthStatus: %v", err)
		}
	})

	result := miscCharDecodeJSONLine(t, out)
	if result["auth_method"] != "credentials_file" {
		t.Errorf("auth_method = %v, want credentials_file", result["auth_method"])
	}
	if result["credentials_path"] != credPath {
		t.Errorf("credentials_path = %v, want %q", result["credentials_path"], credPath)
	}
	if result["configured"] != true {
		t.Errorf("configured = %v, want true", result["configured"])
	}
	if result["validated"] != false {
		t.Errorf("validated = %v, want false", result["validated"])
	}
	wantNote := "Credentials file is configured; pass --validate to verify the token"
	if result["note"] != wantNote {
		t.Errorf("note = %v, want %q", result["note"], wantNote)
	}
}

// TestMiscCharAuthStatus_NoCredentials_NoClientSecret pins the fresh-user
// error path: no env vars, no stored credentials, no client_secret.json.
func TestMiscCharAuthStatus_NoCredentials_NoClientSecret(t *testing.T) {
	miscCharIsolateAuth(t)
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	authStatusValidate = false
	defer func() { authStatusValidate = false }()

	err := runAuthStatus(authStatusCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := noClientSecretError()
	if err.Error() != want.Error() {
		t.Errorf("err = %q, want %q", err.Error(), want.Error())
	}
}

// TestMiscCharAuthStatus_NoCredentials_WithClientSecret pins the
// "client_secret present but never logged in" error path.
func TestMiscCharAuthStatus_NoCredentials_WithClientSecret(t *testing.T) {
	miscCharIsolateAuth(t)
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	if err := os.WriteFile(config.ClientSecretPath(), []byte(`{"installed":{}}`), 0o600); err != nil {
		t.Fatalf("write client secret: %v", err)
	}
	authStatusValidate = false
	defer func() { authStatusValidate = false }()

	err := runAuthStatus(authStatusCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := client.NewAuthError("Not authenticated", suggestNotAuthenticated())
	if err.Error() != want.Error() {
		t.Errorf("err = %q, want %q", err.Error(), want.Error())
	}
}

// TestMiscCharAuthStatus_StoredCredentials_NotExpired pins the stored-
// credentials JSON shape (no --validate) when the token has not expired.
func TestMiscCharAuthStatus_StoredCredentials_NotExpired(t *testing.T) {
	miscCharIsolateAuth(t)
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	authStatusValidate = false
	defer func() { authStatusValidate = false }()

	expiry := time.Now().Add(1 * time.Hour)
	creds := auth.StoredCredentials{
		AccessToken:  "at",
		RefreshToken: "rt",
		TokenType:    "Bearer",
		Expiry:       expiry,
		Scopes:       []string{"scope-a", "scope-b"},
		Email:        "user@example.com",
	}
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := auth.WriteSecretFile(config.CredentialsPath(), data); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	out := miscCharCaptureStdout(t, func() {
		if err := runAuthStatus(authStatusCmd, nil); err != nil {
			t.Fatalf("runAuthStatus: %v", err)
		}
	})

	result := miscCharDecodeJSONLine(t, out)
	if result["authenticated"] != true {
		t.Errorf("authenticated = %v, want true", result["authenticated"])
	}
	if result["expired"] != false {
		t.Errorf("expired = %v, want false", result["expired"])
	}
	if result["email"] != "user@example.com" {
		t.Errorf("email = %v, want user@example.com", result["email"])
	}
	if result["auth_method"] != "oauth" {
		t.Errorf("auth_method = %v, want oauth", result["auth_method"])
	}
	if result["credentials_path"] != config.CredentialsPath() {
		t.Errorf("credentials_path = %v, want %q", result["credentials_path"], config.CredentialsPath())
	}
	wantExpiry := expiry.Format(time.RFC3339)
	if result["expiry"] != wantExpiry {
		t.Errorf("expiry = %v, want %q", result["expiry"], wantExpiry)
	}
	scopes, ok := result["scopes"].([]interface{})
	if !ok || len(scopes) != 2 || scopes[0] != "scope-a" || scopes[1] != "scope-b" {
		t.Errorf("scopes = %v, want [scope-a scope-b]", result["scopes"])
	}
}

// TestMiscCharAuthStatus_StoredCredentials_Expired pins the expired-token
// shape (no --validate): authenticated must flip false and expired true.
func TestMiscCharAuthStatus_StoredCredentials_Expired(t *testing.T) {
	miscCharIsolateAuth(t)
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	authStatusValidate = false
	defer func() { authStatusValidate = false }()

	expiry := time.Now().Add(-1 * time.Hour)
	creds := auth.StoredCredentials{
		AccessToken: "at",
		TokenType:   "Bearer",
		Expiry:      expiry,
	}
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := auth.WriteSecretFile(config.CredentialsPath(), data); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	out := miscCharCaptureStdout(t, func() {
		if err := runAuthStatus(authStatusCmd, nil); err != nil {
			t.Fatalf("runAuthStatus: %v", err)
		}
	})

	result := miscCharDecodeJSONLine(t, out)
	if result["authenticated"] != false {
		t.Errorf("authenticated = %v, want false", result["authenticated"])
	}
	if result["expired"] != true {
		t.Errorf("expired = %v, want true", result["expired"])
	}
}
