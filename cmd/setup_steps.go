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
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ghealth/pkg/auth"
	"ghealth/pkg/client"
	"ghealth/pkg/config"
)

// setupTotalSteps is the number of steps shown in each "[n/total]" header.
const setupTotalSteps = 6

// printSetupInstructions implements `ghealth setup --instructions`: the
// deliberate discovery path for agents. Emits the same next_steps that auth
// errors would emit, on stdout, with exit 0.
func printSetupInstructions() error {
	result := map[string]interface{}{
		authFieldStatus:      "instructions",
		"message":            auth.ClientSecretSetupMessage + " (or none yet); follow next_steps to obtain one.",
		"next_steps":         auth.ClientSecretSetupSteps(),
		"client_secret_path": config.ClientSecretPath(),
		"docs":               "https://console.cloud.google.com/apis/credentials",
		"oauth_client_type":  "Desktop app",
		"complete_command":   "ghealth setup --client-secret /path/to/client_secret.json",
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(setupStdout, string(data))
	return nil
}

func printSetupBanner() {
	fmt.Fprintf(setupStderr, "\n  %sghealth Setup%s\n", bold, reset)
	fmt.Fprintf(setupStderr, "  %s%s%s\n", dim, "Configure GCP project, OAuth credentials, and authenticate", reset)
	blank()
}

// ─── Step 1: GCP Project ID ────────────────────────────────────────

func setupStepProjectID(reader *bufio.Reader) (string, error) {
	header(1, setupTotalSteps, "GCP Project")

	projectID := setupProjectID
	if projectID != "" {
		success(fmt.Sprintf("Project: %s%s%s", bold, projectID, reset))
		return projectID, nil
	}

	if setupNoPrompt {
		return "", client.NewValidationError("--no-prompt set but no --project-id provided", "Pass --project-id <id> or run setup interactively")
	}

	info("You need a Google Cloud project with the Health API enabled.")
	info(fmt.Sprintf("Create one at: %shttps://console.cloud.google.com/projectcreate%s", cyan, reset))
	blank()
	projectID = promptInput(reader, "GCP project ID", "")
	if projectID == "" {
		return "", client.NewValidationError("project ID is required", "Create a project at https://console.cloud.google.com/projectcreate")
	}
	success(fmt.Sprintf("Project: %s%s%s", bold, projectID, reset))
	return projectID, nil
}

// ─── Step 2: OAuth credentials ─────────────────────────────────────

func setupStepOAuthCredentials(reader *bufio.Reader, projectID string) error {
	header(2, setupTotalSteps, "OAuth Credentials")

	secretPath, useExisting, err := resolveClientSecretPath(reader, projectID)
	if err != nil {
		return err
	}
	if useExisting {
		success(fmt.Sprintf("Using existing client secret at %s%s%s", dim, config.ClientSecretPath(), reset))
		return nil
	}
	return importClientSecretFile(secretPath)
}

// resolveClientSecretPath decides where to read the client_secret JSON from:
// the --client-secret flag, an already-installed file (only accepted under
// --no-prompt, where a missing one is the documented bootstrap state rather
// than a validation error), or an interactive prompt.
func resolveClientSecretPath(reader *bufio.Reader, projectID string) (secretPath string, useExisting bool, err error) {
	if setupClientSecret != "" {
		return setupClientSecret, false, nil
	}
	if setupNoPrompt {
		if !auth.HasClientSecret() {
			return "", false, noClientSecretError()
		}
		return "", true, nil
	}

	printClientSecretInstructions(projectID)
	secretPath = promptInput(reader, "Path to downloaded client_secret JSON", "")
	if secretPath == "" {
		return "", false, client.NewValidationError("client secret file path is required", "Download the OAuth client secret JSON from the GCP Console")
	}
	return secretPath, false, nil
}

func printClientSecretInstructions(projectID string) {
	info("Create OAuth credentials in your GCP project:")
	blank()
	fmt.Fprintf(setupStderr, "  %s1.%s Open %s%shttps://console.cloud.google.com/apis/credentials?project=%s%s\n", bold, reset, cyan, dim, projectID, reset)
	fmt.Fprintf(setupStderr, "  %s2.%s Click %sCreate Credentials%s > %sOAuth client ID%s\n", bold, reset, bold, reset, bold, reset)
	fmt.Fprintf(setupStderr, "  %s3.%s Application type: %sDesktop app%s\n", bold, reset, bold, reset)
	fmt.Fprintf(setupStderr, "  %s4.%s Download the JSON file\n", bold, reset)
	blank()
}

func importClientSecretFile(secretPath string) error {
	if strings.HasPrefix(secretPath, "~/") {
		home, _ := os.UserHomeDir()
		secretPath = filepath.Join(home, secretPath[2:])
	}

	srcData, err := os.ReadFile(secretPath)
	if err != nil {
		return client.NewValidationError(fmt.Sprintf("cannot read file: %v", err), "Check the file path and try again")
	}

	destPath := config.ClientSecretPath()
	if err := auth.WriteSecretFile(destPath, srcData); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to copy client secret: %v", err), "")
	}
	success(fmt.Sprintf("Credentials saved to %s%s%s", dim, destPath, reset))
	return nil
}

// ─── Step 4: Scope selection ───────────────────────────────────────

func setupStepSelectScopes(reader *bufio.Reader) ([]string, error) {
	header(4, setupTotalSteps, "Select Scopes")

	switch {
	case setupScopes != "":
		return parseExplicitScopes(setupScopes), nil
	case setupScopesPreset != "":
		return resolveScopesPreset(setupScopesPreset)
	case setupNoPrompt:
		return defaultReadonlyScopes(), nil
	default:
		return promptForScopes(reader), nil
	}
}

func parseExplicitScopes(raw string) []string {
	var scopes []string
	for _, part := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			scopes = append(scopes, s)
		}
	}
	info(fmt.Sprintf("Using --scopes (%d scope%s)", len(scopes), plural(len(scopes))))
	return scopes
}

func resolveScopesPreset(name string) ([]string, error) {
	preset, err := auth.ScopePreset(name)
	if err != nil {
		return nil, client.NewValidationError(err.Error(), "Try --scopes-preset readonly | all | <category,...>")
	}
	info(fmt.Sprintf("Using --scopes-preset %s (%d scope%s)", name, len(preset), plural(len(preset))))
	return preset, nil
}

func defaultReadonlyScopes() []string {
	preset, _ := auth.ScopePreset("readonly")
	info(fmt.Sprintf("--no-prompt set; defaulting to readonly preset (%d scope%s)", len(preset), plural(len(preset))))
	return preset
}

// scopeMenu splits the known OAuth scopes into the read-only and read/write
// groups shown to the user, in the order they appear in auth.AllScopes.
type scopeMenu struct {
	readOnly  []auth.ScopeInfo
	readWrite []auth.ScopeInfo
}

func buildScopeMenu() scopeMenu {
	var m scopeMenu
	for _, s := range auth.AllScopes {
		if strings.HasSuffix(s.Suffix, ".readonly") {
			m.readOnly = append(m.readOnly, s)
		} else {
			m.readWrite = append(m.readWrite, s)
		}
	}
	return m
}

// all returns the read-only scopes followed by the read/write scopes, i.e.
// the numbering order shown in the menu (1-based).
func (m scopeMenu) all() []auth.ScopeInfo {
	return append(append([]auth.ScopeInfo{}, m.readOnly...), m.readWrite...)
}

func printScopeMenu(m scopeMenu) {
	info("Choose which health data categories to authorize.")
	info(fmt.Sprintf("Default: %sall readonly scopes%s (recommended)", bold, reset))
	blank()

	fmt.Fprintf(setupStderr, "  %sRead-only scopes:%s\n", bold, reset)
	for j, opt := range m.readOnly {
		fmt.Fprintf(setupStderr, "    %s%2d.%s %s\n", green, j+1, reset, opt.Label)
	}
	blank()
	fmt.Fprintf(setupStderr, "  %sRead/write scopes:%s\n", bold, reset)
	for j, opt := range m.readWrite {
		fmt.Fprintf(setupStderr, "    %s%2d.%s %s\n", yellow, j+len(m.readOnly)+1, reset, opt.Label)
	}
	blank()

	fmt.Fprintf(setupStderr, "  %sOptions:%s\n", dim, reset)
	fmt.Fprintf(setupStderr, "    %sEnter%s     = All readonly scopes (recommended)\n", bold, reset)
	fmt.Fprintf(setupStderr, "    %s*%s         = All data scopes (read + write; excludes cloud-platform)\n", bold, reset)
	fmt.Fprintf(setupStderr, "    %s1,2,5%s     = Specific scope numbers\n", bold, reset)
	blank()
}

func promptForScopes(reader *bufio.Reader) []string {
	menu := buildScopeMenu()
	printScopeMenu(menu)
	scopeInput := promptInput(reader, "Select scopes", "")
	return parseScopeSelection(scopeInput, menu)
}

func parseScopeSelection(scopeInput string, menu scopeMenu) []string {
	switch strings.TrimSpace(scopeInput) {
	case "", profileDefault:
		return readonlySuffixes(menu.readOnly)
	case "*", "all":
		scopes, _ := auth.ScopePreset("all")
		return scopes
	default:
		return parseNumberedScopeSelection(scopeInput, menu)
	}
}

func readonlySuffixes(scopes []auth.ScopeInfo) []string {
	var out []string
	for _, opt := range scopes {
		out = append(out, opt.Suffix)
	}
	return out
}

func parseNumberedScopeSelection(scopeInput string, menu scopeMenu) []string {
	allOptions := menu.all()
	var selected []string
	for _, part := range strings.Split(scopeInput, ",") {
		part = strings.TrimSpace(part)
		var idx int
		if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(allOptions) {
			selected = append(selected, allOptions[idx-1].Suffix)
		}
	}
	if len(selected) == 0 {
		return readonlySuffixes(menu.readOnly)
	}
	return selected
}

func printSelectedScopes(selectedScopes []string) {
	blank()
	fmt.Fprintf(setupStderr, "  %sSelected:%s\n", bold, reset)
	for _, s := range selectedScopes {
		label := s
		for _, si := range auth.AllScopes {
			if si.Suffix == s {
				label = si.Label
				break
			}
		}
		fmt.Fprintf(setupStderr, "    %s %s\n", check, label)
	}
}

// ─── Step 5: OAuth login ────────────────────────────────────────────

func setupStepAuthenticate(selectedScopes []string) (email string, pendingAuthURL string, err error) {
	header(5, setupTotalSteps, "Authenticate")

	cs, err := auth.LoadClientSecret()
	if err != nil {
		return "", "", client.NewConfigError(err.Error(), "Check the client secret file")
	}

	if setupNonInteractiveAuth {
		pendingAuthURL, err = startNonInteractiveAuth(cs, selectedScopes)
		return "", pendingAuthURL, err
	}

	email, err = performInteractiveLogin(cs, selectedScopes)
	return email, "", err
}

func startNonInteractiveAuth(cs *auth.ClientSecret, selectedScopes []string) (string, error) {
	authURL, _, err := setupDoNonInteractiveStart(cs, selectedScopes)
	if err != nil {
		return "", client.NewConfigError(err.Error(), "")
	}
	info("Skipping browser login (--non-interactive-auth).")
	info("Open this URL on any browser, authorize, then paste the redirected URL (or just the 'code' query parameter):")
	fmt.Fprintf(setupStderr, "\n  %s\n\n", authURL)
	info("Then run: ghealth auth login --complete <code-or-url>")
	return authURL, nil
}

func performInteractiveLogin(cs *auth.ClientSecret, selectedScopes []string) (string, error) {
	info("Opening browser for Google OAuth consent...")
	blank()

	tok, err := setupDoInteractiveLogin(cs, selectedScopes)
	if err != nil {
		return "", client.NewAuthError(fmt.Sprintf("login failed: %v", err), "Check your OAuth credentials and try again")
	}

	email := setupResolveUserEmail(tok.AccessToken)

	creds := &auth.StoredCredentials{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
		Scopes:       selectedScopes,
		Email:        email,
	}
	if err := auth.SaveCredentials(creds); err != nil {
		return "", client.NewConfigError(fmt.Sprintf("failed to save credentials: %v", err), "")
	}

	if email != "" {
		success(fmt.Sprintf("Authenticated as %s%s%s", bold, email, reset))
	} else {
		success("Authenticated successfully")
	}
	return email, nil
}

// ─── Step 6: Save config ────────────────────────────────────────────

func setupStepSaveConfig(projectID string, selectedScopes []string) error {
	header(6, setupTotalSteps, "Save Configuration")

	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{
			Default:  config.ProfileConfig{},
			Profiles: make(map[string]config.ProfileConfig),
		}
	}
	cfg.Default.ProjectID = projectID
	cfg.Default.Scopes = selectedScopes

	if err := cfg.Save(); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to save config: %v", err), "")
	}
	success(fmt.Sprintf("Config saved to %s%s%s", dim, config.ConfigPath(), reset))
	if !setupNonInteractiveAuth {
		success(fmt.Sprintf("Credentials saved to %s%s%s", dim, config.CredentialsPath(), reset))
	}
	return nil
}

// ─── Done ────────────────────────────────────────────────────────────

func printSetupComplete(projectID, email string, selectedScopes []string, pendingAuthURL string) {
	blank()
	fmt.Fprintf(setupStderr, "  %s%s Setup Complete %s\n", bold, check, reset)
	blank()
	if !setupNonInteractiveAuth {
		fmt.Fprintf(setupStderr, "  %sTry it out:%s\n", dim, reset)
		fmt.Fprintf(setupStderr, "    %s$ ghealth data steps list --from 2026-03-28%s\n", cyan, reset)
		fmt.Fprintf(setupStderr, "    %s$ ghealth data sleep list --from 2026-03-22%s\n", cyan, reset)
		fmt.Fprintf(setupStderr, "    %s$ ghealth schema types%s\n", cyan, reset)
		blank()
	}

	status := "setup_complete"
	if setupNonInteractiveAuth {
		status = "setup_pending_auth"
	}
	result := map[string]interface{}{
		authFieldStatus:      status,
		"project_id":         projectID,
		"email":              email,
		"scopes":             selectedScopes,
		"config_dir":         config.ConfigDir(),
		"client_secret_path": config.ClientSecretPath(),
		"credentials_path":   config.CredentialsPath(),
	}
	if setupNonInteractiveAuth {
		// Surface everything an agent needs to finish the flow on stdout — the
		// browser URL on stderr is for humans only and is not machine-readable.
		result["auth_url"] = pendingAuthURL
		result["complete_command"] = "ghealth auth login --complete <code-or-url>"
		result["pending_auth_path"] = auth.PendingAuthPath()
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(setupStdout, string(data))
}
