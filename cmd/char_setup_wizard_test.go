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

// Characterization tests for the setup wizard (runSetup, cmd/setup.go). These
// pin CURRENT behavior through the seams introduced for testing
// (setupStdin/setupStdout/setupStderr, setupDoInteractiveLogin,
// setupDoNonInteractiveStart, setupResolveUserEmail) so the function can be
// decomposed without changing observable behavior. Every helper, type, and
// Test function here is prefixed setupChar to avoid colliding with sibling
// slices' tests in this package.
package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ghealth/pkg/auth"
	"ghealth/pkg/client"
	"ghealth/pkg/config"

	"golang.org/x/oauth2"
)

// ─── Harness ─────────────────────────────────────────────────────

// setupCharReset snapshots every setup* package-level flag/seam var, resets
// the flags to their zero value, and restores everything on test cleanup.
// These are shared globals with the real `ghealth setup` command, so tests
// must not leak state between each other.
func setupCharReset(t *testing.T) {
	t.Helper()
	origProjectID := setupProjectID
	origClientSecret := setupClientSecret
	origScopes := setupScopes
	origScopesPreset := setupScopesPreset
	origNoPrompt := setupNoPrompt
	origSkipEnable := setupSkipEnable
	origNonInteractiveAuth := setupNonInteractiveAuth
	origInstructions := setupInstructions
	origStdin := setupStdin
	origStdout := setupStdout
	origStderr := setupStderr
	origInteractiveLogin := setupDoInteractiveLogin
	origNonInteractiveStart := setupDoNonInteractiveStart
	origResolveUserEmail := setupResolveUserEmail

	setupProjectID = ""
	setupClientSecret = ""
	setupScopes = ""
	setupScopesPreset = ""
	setupNoPrompt = false
	setupSkipEnable = false
	setupNonInteractiveAuth = false
	setupInstructions = false

	t.Cleanup(func() {
		setupProjectID = origProjectID
		setupClientSecret = origClientSecret
		setupScopes = origScopes
		setupScopesPreset = origScopesPreset
		setupNoPrompt = origNoPrompt
		setupSkipEnable = origSkipEnable
		setupNonInteractiveAuth = origNonInteractiveAuth
		setupInstructions = origInstructions
		setupStdin = origStdin
		setupStdout = origStdout
		setupStderr = origStderr
		setupDoInteractiveLogin = origInteractiveLogin
		setupDoNonInteractiveStart = origNonInteractiveStart
		setupResolveUserEmail = origResolveUserEmail
	})
}

// setupCharConfigDir isolates the wizard's config directory (client secret,
// credentials, config.toml, pending auth) to a fresh temp dir.
func setupCharConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	return dir
}

// setupCharRun points the reader/writer seams at buffers/a fixed script and
// invokes runSetup directly (cmd/args are unused by runSetup).
func setupCharRun(t *testing.T, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	setupStdin = strings.NewReader(stdin)
	setupStdout = &outBuf
	setupStderr = &errBuf
	err = runSetup(nil, nil)
	return outBuf.String(), errBuf.String(), err
}

// setupCharFakeNonInteractiveStart stubs the non-interactive OAuth start so
// tests never hit the network; it mimics auth.NonInteractiveStart's success
// signature without touching disk state beyond what the caller controls.
func setupCharFakeNonInteractiveStart(url string, err error) {
	setupDoNonInteractiveStart = func(_ *auth.ClientSecret, _ []string) (string, *auth.PendingAuth, error) {
		return url, nil, err
	}
}

// setupCharFakeInteractiveLogin stubs the browser-based OAuth login.
func setupCharFakeInteractiveLogin(tok *oauth2.Token, err error) {
	setupDoInteractiveLogin = func(_ *auth.ClientSecret, _ []string) (*oauth2.Token, error) {
		return tok, err
	}
}

// setupCharValidClientSecretJSON is a minimal, well-formed OAuth
// client_secret.json (Desktop app / "installed" shape).
const setupCharValidClientSecretJSON = `{"installed":{"client_id":"id-123","client_secret":"secret-123","redirect_uris":["http://localhost"],"auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token"}}`

// setupCharWriteFile writes content to a new file under t.TempDir() and
// returns its path.
func setupCharWriteFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

// setupCharCLIError asserts err is a *client.CLIError with the given fields.
func setupCharCLIError(t *testing.T, err error, wantType string, wantCode int, wantMessage, wantHint string) *client.CLIError {
	t.Helper()
	var cliErr *client.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v (%T), want *client.CLIError", err, err)
	}
	if cliErr.Type != wantType {
		t.Errorf("CLIError.Type = %q, want %q", cliErr.Type, wantType)
	}
	if cliErr.Code != wantCode {
		t.Errorf("CLIError.Code = %d, want %d", cliErr.Code, wantCode)
	}
	if cliErr.Message != wantMessage {
		t.Errorf("CLIError.Message = %q, want %q", cliErr.Message, wantMessage)
	}
	if cliErr.Hint != wantHint {
		t.Errorf("CLIError.Hint = %q, want %q", cliErr.Hint, wantHint)
	}
	return cliErr
}

// ─── --instructions ─────────────────────────────────────────────

func TestSetupChar_Instructions(t *testing.T) {
	setupCharReset(t)
	setupCharConfigDir(t)
	setupInstructions = true

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty (--instructions writes to stdout only)", stderr)
	}

	want := map[string]interface{}{
		"status":             "instructions",
		"message":            auth.ClientSecretSetupMessage + " (or none yet); follow next_steps to obtain one.",
		"next_steps":         auth.ClientSecretSetupSteps(),
		"client_secret_path": config.ClientSecretPath(),
		"docs":               "https://console.cloud.google.com/apis/credentials",
		"oauth_client_type":  "Desktop app",
		"complete_command":   "ghealth setup --client-secret /path/to/client_secret.json",
	}
	wantJSON, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if got, want := stdout, string(wantJSON)+"\n"; got != want {
		t.Errorf("stdout =\n%s\nwant:\n%s", got, want)
	}
}

// ─── Step 1: GCP project ID ──────────────────────────────────────

func TestSetupChar_ProjectID_NoPromptMissing(t *testing.T) {
	setupCharReset(t)
	setupCharConfigDir(t)
	setupNoPrompt = true

	_, _, err := setupCharRun(t, "")
	_ = setupCharCLIError(t, err, "validation", client.ExitValidation,
		"--no-prompt set but no --project-id provided",
		"Pass --project-id <id> or run setup interactively")
}

func TestSetupChar_ProjectID_PromptEmpty(t *testing.T) {
	setupCharReset(t)
	setupCharConfigDir(t)

	_, _, err := setupCharRun(t, "\n")
	_ = setupCharCLIError(t, err, "validation", client.ExitValidation,
		"project ID is required",
		"Create a project at https://console.cloud.google.com/projectcreate")
}

func TestSetupChar_ProjectID_FromFlagSkipsPrompt(t *testing.T) {
	setupCharReset(t)
	setupCharConfigDir(t)
	setupProjectID = "my-proj"
	setupNoPrompt = true // would fail on any other missing input
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(stderr, "Project: ") || !strings.Contains(stderr, "my-proj") {
		t.Errorf("stderr missing project confirmation: %q", stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if result["project_id"] != "my-proj" {
		t.Errorf("project_id = %v, want my-proj", result["project_id"])
	}
}

// ─── Step 2: OAuth credentials ───────────────────────────────────

func TestSetupChar_ClientSecret_MissingFile(t *testing.T) {
	setupCharReset(t)
	setupCharConfigDir(t)
	setupProjectID = "proj"
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	setupClientSecret = missing

	_, wantReadErr := os.ReadFile(missing)

	_, _, err := setupCharRun(t, "")
	_ = setupCharCLIError(t, err, "validation", client.ExitValidation,
		"cannot read file: "+wantReadErr.Error(),
		"Check the file path and try again")
}

func TestSetupChar_ClientSecret_NoPromptMissing(t *testing.T) {
	setupCharReset(t)
	setupCharConfigDir(t)
	setupProjectID = "proj"
	setupNoPrompt = true
	// setupClientSecret left empty and no client_secret.json in the config
	// dir: this is the documented bootstrap state, not a validation error.

	_, _, err := setupCharRun(t, "")
	cliErr := setupCharCLIError(t, err, "config", client.ExitConfigError,
		auth.ClientSecretSetupMessage, auth.ClientSecretSetupHint)
	if !reflect.DeepEqual(cliErr.NextSteps, auth.ClientSecretSetupSteps()) {
		t.Errorf("NextSteps = %v, want %v", cliErr.NextSteps, auth.ClientSecretSetupSteps())
	}
}

func TestSetupChar_ClientSecret_NoPromptUsesExisting(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	setupNoPrompt = true
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)

	// Pre-existing client_secret.json satisfies --no-prompt without
	// --client-secret being passed.
	existing := filepath.Join(dir, "client_secret.json")
	if err := os.WriteFile(existing, []byte(setupCharValidClientSecretJSON), 0600); err != nil {
		t.Fatalf("seed client secret: %v", err)
	}

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(stderr, "Using existing client secret at") {
		t.Errorf("stderr missing existing-secret confirmation: %q", stderr)
	}
	if !strings.Contains(stdout, "setup_pending_auth") {
		t.Errorf("stdout missing setup_pending_auth: %q", stdout)
	}
	// The file must be untouched (still exactly what we seeded).
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != setupCharValidClientSecretJSON {
		t.Errorf("existing client secret was modified: %s", got)
	}
}

func TestSetupChar_ClientSecret_ImportNewFile(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)

	src := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = src

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "setup_pending_auth") {
		t.Errorf("stdout missing setup_pending_auth: %s", stdout)
	}

	destPath := filepath.Join(dir, "client_secret.json")
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("Stat(destPath): %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("dest mode = %o, want 0600", perm)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile(destPath): %v", err)
	}
	if string(got) != setupCharValidClientSecretJSON {
		t.Errorf("dest content = %s, want %s", got, setupCharValidClientSecretJSON)
	}
}

func TestSetupChar_ClientSecret_ImportOverExisting0644File(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)

	destPath := filepath.Join(dir, "client_secret.json")
	if err := os.WriteFile(destPath, []byte(`{"stale":true}`), 0644); err != nil {
		t.Fatalf("seed stale dest: %v", err)
	}

	src := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = src

	if _, _, err := setupCharRun(t, ""); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("Stat(destPath): %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("dest mode = %o, want 0600 (existing 0644 file must be overwritten with 0600)", perm)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile(destPath): %v", err)
	}
	if string(got) != setupCharValidClientSecretJSON {
		t.Errorf("dest content = %s, want the newly imported content", got)
	}
}

func TestSetupChar_ClientSecret_TildeExpansion(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "client_secret.json"), []byte(setupCharValidClientSecretJSON), 0644); err != nil {
		t.Fatalf("seed home file: %v", err)
	}
	setupClientSecret = "~/client_secret.json"

	if _, stderr, err := setupCharRun(t, ""); err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}

	got, err := os.ReadFile(filepath.Join(dir, "client_secret.json"))
	if err != nil {
		t.Fatalf("ReadFile(dest): %v", err)
	}
	if string(got) != setupCharValidClientSecretJSON {
		t.Errorf("dest content = %s, want tilde-expanded source content", got)
	}
}

func TestSetupChar_ClientSecret_InteractivePrompt(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)

	src := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	// setupClientSecret left empty: the wizard must print the numbered
	// instructions and prompt for a path on stdin.

	stdout, stderr, err := setupCharRun(t, src+"\n")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Create OAuth credentials in your GCP project:") {
		t.Errorf("stderr missing prompt instructions: %q", stderr)
	}
	if !strings.Contains(stderr, "Application type: ") {
		t.Errorf("stderr missing numbered instructions: %q", stderr)
	}
	if !strings.Contains(stdout, "setup_pending_auth") {
		t.Errorf("stdout missing setup_pending_auth: %s", stdout)
	}
	got, err := os.ReadFile(filepath.Join(dir, "client_secret.json"))
	if err != nil {
		t.Fatalf("ReadFile(dest): %v", err)
	}
	if string(got) != setupCharValidClientSecretJSON {
		t.Errorf("dest content = %s, want the prompted source content", got)
	}
}

func TestSetupChar_ClientSecret_InteractivePromptEmptyFails(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"

	_, _, err := setupCharRun(t, "\n")
	_ = setupCharCLIError(t, err, "validation", client.ExitValidation,
		"client secret file path is required",
		"Download the OAuth client secret JSON from the GCP Console")
}

// ─── Step 3: Enable Health API ────────────────────────────────────

// setupCharFakeGcloud installs a fake `gcloud` script (exiting with
// exitCode) as the only entry on PATH, or (if present=false) an empty PATH so
// exec.LookPath("gcloud") fails to find any binary.
func setupCharFakeGcloud(t *testing.T, present bool, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	if present {
		script := filepath.Join(dir, "gcloud")
		content := "#!/bin/sh\nexit " + itoaSetupChar(exitCode) + "\n"
		if err := os.WriteFile(script, []byte(content), 0755); err != nil {
			t.Fatalf("write fake gcloud: %v", err)
		}
	}
	t.Setenv("PATH", dir)
}

func itoaSetupChar(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

// setupCharRunToEnableAPI drives the wizard through steps 1-2 with flags,
// leaving step 3 (Enable Health API) to prompt/behave per PATH/setupNoPrompt,
// then completes via --scopes + --non-interactive-auth so no further stdin is
// required beyond what step 3 itself consumes.
func setupCharRunToEnableAPI(t *testing.T, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)
	return setupCharRun(t, stdin)
}

func TestSetupChar_EnableAPI_Skip(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupSkipEnable = true

	_, stderr, err := setupCharRunToEnableAPI(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "--skip-enable-api set; assuming Health API is already enabled") {
		t.Errorf("stderr missing skip message: %q", stderr)
	}
}

func TestSetupChar_EnableAPI_GcloudMissing_Prompts(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupCharFakeGcloud(t, false, 0)

	_, stderr, err := setupCharRunToEnableAPI(t, "\n")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "gcloud not found. Enable the Health API manually:") {
		t.Errorf("stderr missing manual-enable message: %q", stderr)
	}
	if !strings.Contains(stderr, "Press Enter when done") {
		t.Errorf("stderr missing press-enter prompt: %q", stderr)
	}
	if !strings.Contains(stderr, "Continuing") {
		t.Errorf("stderr missing Continuing confirmation: %q", stderr)
	}
}

func TestSetupChar_EnableAPI_GcloudMissing_NoPromptWarns(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupNoPrompt = true
	setupCharFakeGcloud(t, false, 0)

	_, stderr, err := setupCharRunToEnableAPI(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "--no-prompt set; continuing without confirmation (re-run with --skip-enable-api to silence this)") {
		t.Errorf("stderr missing no-prompt warning: %q", stderr)
	}
	if strings.Contains(stderr, "Press Enter when done") {
		t.Errorf("stderr should not prompt under --no-prompt: %q", stderr)
	}
}

func TestSetupChar_EnableAPI_GcloudFails_Prompts(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupCharFakeGcloud(t, true, 1)

	_, stderr, err := setupCharRunToEnableAPI(t, "\n")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Could not enable via gcloud: exit status 1") {
		t.Errorf("stderr missing gcloud failure warning: %q", stderr)
	}
	if !strings.Contains(stderr, "Enable manually: ") {
		t.Errorf("stderr missing manual-enable link: %q", stderr)
	}
	if !strings.Contains(stderr, "Press Enter when done") {
		t.Errorf("stderr missing press-enter prompt: %q", stderr)
	}
}

func TestSetupChar_EnableAPI_GcloudFails_NoPromptSkipsPress(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupNoPrompt = true
	setupCharFakeGcloud(t, true, 1)

	_, stderr, err := setupCharRunToEnableAPI(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Could not enable via gcloud: exit status 1") {
		t.Errorf("stderr missing gcloud failure warning: %q", stderr)
	}
	if strings.Contains(stderr, "Press Enter when done") {
		t.Errorf("stderr should not prompt under --no-prompt: %q", stderr)
	}
}

func TestSetupChar_EnableAPI_GcloudSucceeds(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupCharFakeGcloud(t, true, 0)

	_, stderr, err := setupCharRunToEnableAPI(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Found gcloud, enabling Health API...") {
		t.Errorf("stderr missing found-gcloud message: %q", stderr)
	}
	if !strings.Contains(stderr, "Health API enabled") {
		t.Errorf("stderr missing success message: %q", stderr)
	}
	if strings.Contains(stderr, "Press Enter when done") {
		t.Errorf("stderr should not prompt on gcloud success: %q", stderr)
	}
}

// ─── Step 4: Scope selection ──────────────────────────────────────

// setupCharRunToScopes drives the wizard through steps 1-3 via flags, leaving
// step 4 (interactive scope prompt) to read scopeInput from stdin, then
// completes via --non-interactive-auth so no further stdin is required.
func setupCharRunToScopes(t *testing.T, scopeInput string) (stdout, stderr string, err error) {
	t.Helper()
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)
	return setupCharRun(t, scopeInput+"\n")
}

func setupCharResultScopes(t *testing.T, stdout string) []string {
	t.Helper()
	var result struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	return result.Scopes
}

func setupCharReadonlyScopeSuffixes() []string {
	var out []string
	for _, s := range auth.AllScopes {
		if strings.HasSuffix(s.Suffix, ".readonly") {
			out = append(out, s.Suffix)
		}
	}
	return out
}

func TestSetupChar_ScopeSelection(t *testing.T) {
	readonly := setupCharReadonlyScopeSuffixes()
	allPreset, err := auth.ScopePreset("all")
	if err != nil {
		t.Fatalf("ScopePreset(all): %v", err)
	}

	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"Empty", "", readonly},
		{"Default", "default", readonly},
		{"Star", "*", allPreset},
		{"All", "all", allPreset},
		{"NumberedFirstAndTenth", "1,10", []string{"activity_and_fitness.readonly", "activity_and_fitness"}},
		{"InvalidNumberFallsBackToReadonly", "999", readonly},
		{"NonNumericFallsBackToReadonly", "not-a-number", readonly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupCharConfigDir(t)
			setupCharReset(t)
			stdout, stderr, err := setupCharRunToScopes(t, c.input)
			if err != nil {
				t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
			}
			got := setupCharResultScopes(t, stdout)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("scopes = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSetupChar_ScopeSelection_ExplicitFlagParsing(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)
	setupScopes = "sleep.readonly, ,nutrition.readonly,"

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	got := setupCharResultScopes(t, stdout)
	want := []string{"sleep.readonly", "nutrition.readonly"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopes = %v, want %v (blank segments trimmed/dropped)", got, want)
	}
}

func TestSetupChar_ScopeSelection_PresetValid(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)
	setupScopesPreset = "readonly"

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	got := setupCharResultScopes(t, stdout)
	want := setupCharReadonlyScopeSuffixes()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopes = %v, want %v", got, want)
	}
	if !strings.Contains(stderr, "Using --scopes-preset readonly") {
		t.Errorf("stderr missing preset confirmation: %q", stderr)
	}
}

func TestSetupChar_ScopeSelection_PresetInvalid(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopesPreset = "no-such-category"

	_, _, err := setupCharRun(t, "")
	wantErr, presetErr := auth.ScopePreset("no-such-category")
	if presetErr == nil {
		t.Fatalf("test setup: ScopePreset(no-such-category) unexpectedly succeeded: %v", wantErr)
	}
	_ = setupCharCLIError(t, err, "validation", client.ExitValidation,
		presetErr.Error(), "Try --scopes-preset readonly | all | <category,...>")
}

func TestSetupChar_ScopeSelection_NoPromptDefaultsToReadonly(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupNoPrompt = true
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url", nil)
	// setupScopes and setupScopesPreset left empty: --no-prompt must default
	// to the readonly preset without prompting.

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "--no-prompt set; defaulting to readonly preset") {
		t.Errorf("stderr missing no-prompt default message: %q", stderr)
	}
	got := setupCharResultScopes(t, stdout)
	want := setupCharReadonlyScopeSuffixes()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopes = %v, want %v", got, want)
	}
}

// ─── Step 5: OAuth login ──────────────────────────────────────────

func TestSetupChar_LoadClientSecret_InvalidJSON(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", `{not valid json`)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"

	_, _, err := setupCharRun(t, "")
	_, wantErr := auth.LoadClientSecret()
	if wantErr == nil {
		t.Fatalf("test setup: LoadClientSecret unexpectedly succeeded")
	}
	_ = setupCharCLIError(t, err, "config", client.ExitConfigError,
		wantErr.Error(), "Check the client secret file")
}

func TestSetupChar_NonInteractiveAuth_StartFails(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("", errors.New("boom"))

	_, _, err := setupCharRun(t, "")
	_ = setupCharCLIError(t, err, "config", client.ExitConfigError, "boom", "")
}

func TestSetupChar_NonInteractiveAuth_Success(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupNonInteractiveAuth = true
	setupCharFakeNonInteractiveStart("https://accounts.google.com/fake-auth-url?state=xyz", nil)

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Skipping browser login (--non-interactive-auth).") {
		t.Errorf("stderr missing skip-browser message: %q", stderr)
	}
	if !strings.Contains(stderr, "https://accounts.google.com/fake-auth-url?state=xyz") {
		t.Errorf("stderr missing auth URL: %q", stderr)
	}
	// "Credentials saved to" is also the Step 2 client-secret-import success
	// message; it must appear exactly once (from Step 2) and not a second
	// time for the Step 6 credentials.json save, which --non-interactive-auth
	// skips.
	if n := strings.Count(stderr, "Credentials saved to"); n != 1 {
		t.Errorf("stderr contains %d occurrences of \"Credentials saved to\", want exactly 1 (Step 2 import only): %q", n, stderr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if result["status"] != "setup_pending_auth" {
		t.Errorf("status = %v, want setup_pending_auth", result["status"])
	}
	if result["auth_url"] != "https://accounts.google.com/fake-auth-url?state=xyz" {
		t.Errorf("auth_url = %v", result["auth_url"])
	}
	if result["complete_command"] != "ghealth auth login --complete <code-or-url>" {
		t.Errorf("complete_command = %v", result["complete_command"])
	}
	if result["pending_auth_path"] != auth.PendingAuthPath() {
		t.Errorf("pending_auth_path = %v, want %v", result["pending_auth_path"], auth.PendingAuthPath())
	}
	if result["email"] != "" {
		t.Errorf("email = %v, want empty (no login performed)", result["email"])
	}
	// Credentials file must not have been written.
	if _, err := os.Stat(config.CredentialsPath()); !os.IsNotExist(err) {
		t.Errorf("credentials.json should not exist, stat err = %v", err)
	}
}

func TestSetupChar_InteractiveLogin_Fails(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupCharFakeInteractiveLogin(nil, errors.New("network unreachable"))

	_, _, err := setupCharRun(t, "")
	_ = setupCharCLIError(t, err, "auth", client.ExitAuthError,
		"login failed: network unreachable",
		"Check your OAuth credentials and try again")
}

func TestSetupChar_InteractiveLogin_Success(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly,sleep.readonly"
	expiry := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setupCharFakeInteractiveLogin(&oauth2.Token{
		AccessToken:  "access-tok",
		RefreshToken: "refresh-tok",
		TokenType:    "Bearer",
		Expiry:       expiry,
	}, nil)
	setupDoInteractiveLoginResolveEmail := "person@example.com"
	setupResolveUserEmail = func(accessToken string) string {
		if accessToken != "access-tok" {
			t.Errorf("setupResolveUserEmail called with %q, want access-tok", accessToken)
		}
		return setupDoInteractiveLoginResolveEmail
	}

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Authenticated as ") || !strings.Contains(stderr, "person@example.com") {
		t.Errorf("stderr missing authenticated-as message: %q", stderr)
	}
	if !strings.Contains(stderr, "Config saved to ") {
		t.Errorf("stderr missing config-saved message: %q", stderr)
	}
	if !strings.Contains(stderr, "Credentials saved to ") {
		t.Errorf("stderr missing credentials-saved message: %q", stderr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if result["status"] != "setup_complete" {
		t.Errorf("status = %v, want setup_complete", result["status"])
	}
	if result["email"] != "person@example.com" {
		t.Errorf("email = %v", result["email"])
	}
	if _, ok := result["auth_url"]; ok {
		t.Errorf("result should not contain auth_url for interactive login: %v", result)
	}

	// Credentials persisted with mode 0600 and the expected fields.
	credPath := filepath.Join(dir, "credentials.json")
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("Stat(credentials.json): %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("credentials.json mode = %o, want 0600", perm)
	}
	var creds auth.StoredCredentials
	raw, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("ReadFile(credentials.json): %v", err)
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		t.Fatalf("Unmarshal credentials.json: %v", err)
	}
	if creds.AccessToken != "access-tok" || creds.RefreshToken != "refresh-tok" || creds.Email != "person@example.com" {
		t.Errorf("stored credentials = %+v", creds)
	}
	if !reflect.DeepEqual(creds.Scopes, []string{"profile.readonly", "sleep.readonly"}) {
		t.Errorf("stored scopes = %v", creds.Scopes)
	}

	// config.toml reflects the project id and scopes.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Default.ProjectID != "proj" {
		t.Errorf("config project_id = %q, want proj", cfg.Default.ProjectID)
	}
	if !reflect.DeepEqual(cfg.Default.Scopes, []string{"profile.readonly", "sleep.readonly"}) {
		t.Errorf("config scopes = %v", cfg.Default.Scopes)
	}
}

func TestSetupChar_InteractiveLogin_NoEmailStillSucceeds(t *testing.T) {
	setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupCharFakeInteractiveLogin(&oauth2.Token{AccessToken: "tok", TokenType: "Bearer"}, nil)
	setupResolveUserEmail = func(string) string { return "" }

	stdout, stderr, err := setupCharRun(t, "")
	if err != nil {
		t.Fatalf("runSetup error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Authenticated successfully") {
		t.Errorf("stderr missing generic success message: %q", stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if result["email"] != "" {
		t.Errorf("email = %v, want empty string", result["email"])
	}
}

// ─── Step 6 / config save error path ──────────────────────────────

func TestSetupChar_SaveConfig_Fails(t *testing.T) {
	dir := setupCharConfigDir(t)
	setupCharReset(t)
	setupProjectID = "proj"
	secretPath := setupCharWriteFile(t, "client_secret.json", setupCharValidClientSecretJSON)
	setupClientSecret = secretPath
	setupSkipEnable = true
	setupScopes = "profile.readonly"
	setupCharFakeInteractiveLogin(&oauth2.Token{AccessToken: "tok", TokenType: "Bearer"}, nil)
	setupResolveUserEmail = func(string) string { return "" }

	// Make config.Save() fail without disturbing client_secret.json /
	// credentials.json (steps 2 and 5 must still succeed): pre-create
	// config.toml as a directory, so config.Load()'s read fails (ignored;
	// runSetup falls back to a fresh in-memory config) and the subsequent
	// os.OpenFile(..., O_CREATE|O_TRUNC) in config.Save() fails too.
	if err := os.Mkdir(filepath.Join(dir, "config.toml"), 0755); err != nil {
		t.Fatalf("seed config.toml as directory: %v", err)
	}

	_, stderr, err := setupCharRun(t, "")
	if err == nil {
		t.Fatalf("expected an error when config.toml cannot be opened for writing\nstderr:\n%s", stderr)
	}
	var cliErr *client.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v (%T), want *client.CLIError", err, err)
	}
	if cliErr.Type != "config" || cliErr.Code != client.ExitConfigError {
		t.Errorf("CLIError = %+v, want type=config code=%d", cliErr, client.ExitConfigError)
	}
	if !strings.HasPrefix(cliErr.Message, "failed to save config: ") {
		t.Errorf("Message = %q, want prefix %q", cliErr.Message, "failed to save config: ")
	}
	// Steps before Save() must have completed: credentials.json exists.
	if _, statErr := os.Stat(filepath.Join(dir, "credentials.json")); statErr != nil {
		t.Errorf("credentials.json should have been written before the config-save failure: %v", statErr)
	}
}
