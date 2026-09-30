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
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"ghealth/pkg/auth"
	"github.com/spf13/cobra"
)

var (
	setupProjectID          string
	setupClientSecret       string
	setupScopes             string
	setupScopesPreset       string
	setupNoPrompt           bool
	setupSkipEnable         bool
	setupNonInteractiveAuth bool
	setupInstructions       bool
)

// Seams for testing: production defaults route through the real OS streams
// and the real OAuth implementations; tests substitute fakes so the wizard's
// prompts, imports, and login step can be exercised without a terminal,
// network access, or a real browser. gcloud invocation is not seamed here:
// tests control it via PATH (a fake `gcloud` script ahead of the real one).
var (
	setupStdin  io.Reader = os.Stdin
	setupStdout io.Writer = os.Stdout
	setupStderr io.Writer = os.Stderr

	setupDoInteractiveLogin    = auth.InteractiveLogin
	setupDoNonInteractiveStart = auth.NonInteractiveStart
	setupResolveUserEmail      = fetchUserEmail
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Guided first-time setup wizard",
	Long: `Interactive wizard that takes you from zero to authenticated in one command.

Steps:
  1. GCP project ID (create at https://console.cloud.google.com/projectcreate)
  2. OAuth client_secret JSON (Desktop application; download from the GCP Console
     credentials page). Stored at ~/.config/ghealth/client_secret.json.
  3. Enable the Google Health API (via gcloud if available, otherwise a manual link).
  4. Scope selection — defaults to all readonly scopes.
  5. Browser-based OAuth login — binds an ephemeral loopback port; tokens stored
     at ~/.config/ghealth/credentials.json (plaintext JSON, mode 0600).
  6. Save the active profile to ~/.config/ghealth/config.toml.

Flags let agents drive the wizard non-interactively:

  ghealth setup \
    --project-id my-project \
    --client-secret ~/Downloads/client_secret_123.json \
    --scopes-preset readonly \
    --skip-enable-api \
    --no-prompt

With --no-prompt the wizard fails on any missing input rather than asking, so it
can run inside automation. Use --non-interactive-auth to additionally skip the
browser step (you'll then need to run 'ghealth auth login --complete <code>').

For agents that want to fetch the bootstrap checklist deliberately (before any
auth call would fail), use --instructions:

  ghealth setup --instructions
    # exit 0; prints a JSON object with the same next_steps that auth errors emit`,
	RunE: runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupProjectID, "project-id", "", "GCP project ID (skips the prompt)")
	setupCmd.Flags().StringVar(&setupClientSecret, "client-secret", "", "Path to the downloaded OAuth client_secret JSON (skips the prompt)")
	setupCmd.Flags().StringVar(&setupScopes, "scopes", "", "Comma-separated scope suffixes (overrides --scopes-preset)")
	setupCmd.Flags().StringVar(&setupScopesPreset, "scopes-preset", "", "Scope preset: readonly | all | comma-separated category names")
	setupCmd.Flags().BoolVar(&setupNoPrompt, "no-prompt", false, "Fail if any required input is missing instead of prompting")
	setupCmd.Flags().BoolVar(&setupSkipEnable, "skip-enable-api", false, "Skip the 'Enable Health API' step (assume it's already enabled)")
	setupCmd.Flags().BoolVar(&setupNonInteractiveAuth, "non-interactive-auth", false, "Skip browser-based OAuth login; complete later with 'ghealth auth login --complete <code>'")
	setupCmd.Flags().BoolVar(&setupInstructions, "instructions", false, "Print the OAuth client_secret bootstrap checklist as JSON and exit 0 (no wizard)")
}

// ─── UI helpers ──────────────────────────────────────────────────

const (
	dim    = "\033[2m"
	bold   = "\033[1m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	reset  = "\033[0m"
	check  = "\033[32m✓\033[0m"
	arrow  = "\033[36m→\033[0m"
)

func header(step int, total int, title string) {
	fmt.Fprintf(setupStderr, "\n%s[%d/%d]%s %s%s%s\n", dim, step, total, reset, bold, title, reset)
	fmt.Fprintf(setupStderr, "%s%s%s\n", dim, strings.Repeat("─", 50), reset)
}

func info(msg string) {
	fmt.Fprintf(setupStderr, "  %s %s\n", arrow, msg)
}

func success(msg string) {
	fmt.Fprintf(setupStderr, "  %s %s\n", check, msg)
}

func warn(msg string) {
	fmt.Fprintf(setupStderr, "  %s!%s %s\n", yellow, reset, msg)
}

func blank() {
	fmt.Fprintln(setupStderr)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func promptInput(reader *bufio.Reader, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(setupStderr, "  %s [%s%s%s]: ", prompt, dim, defaultVal, reset)
	} else {
		fmt.Fprintf(setupStderr, "  %s: ", prompt)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// ─── Setup wizard ────────────────────────────────────────────────
//
// runSetup orchestrates the wizard's six steps (see setup_steps.go); it holds
// no logic of its own beyond sequencing and threading state (projectID,
// selectedScopes, email, pendingAuthURL) between steps.

func runSetup(cmd *cobra.Command, args []string) error {
	// --instructions: deliberate discovery path for agents, handled before
	// anything else touches stdin/output.
	if setupInstructions {
		return printSetupInstructions()
	}

	reader := bufio.NewReader(setupStdin)
	printSetupBanner()

	projectID, err := setupStepProjectID(reader)
	if err != nil {
		return err
	}

	if err := setupStepOAuthCredentials(reader, projectID); err != nil {
		return err
	}

	setupStepEnableAPI(reader, projectID)

	selectedScopes, err := setupStepSelectScopes(reader)
	if err != nil {
		return err
	}
	printSelectedScopes(selectedScopes)

	email, pendingAuthURL, err := setupStepAuthenticate(selectedScopes)
	if err != nil {
		return err
	}

	if err := setupStepSaveConfig(projectID, selectedScopes); err != nil {
		return err
	}

	printSetupComplete(projectID, email, selectedScopes, pendingAuthURL)
	return nil
}

// ─── Step 3: Enable Health API ─────────────────────────────────────
//
// Kept in this file (rather than setup_steps.go) so the existing
// .golangci.yml gosec G204 exclusion for cmd/setup.go — "runs gcloud with
// fixed arguments plus the user's own project ID; no shell involved" —
// still applies without widening that exclusion's scope.

func setupStepEnableAPI(reader *bufio.Reader, projectID string) {
	header(3, setupTotalSteps, "Enable Health API")

	if setupSkipEnable {
		info("--skip-enable-api set; assuming Health API is already enabled")
		return
	}

	if _, err := exec.LookPath("gcloud"); err != nil {
		promptManualEnableAPI(reader, projectID)
		return
	}
	enableHealthAPIViaGcloud(reader, projectID)
}

func enableHealthAPIViaGcloud(reader *bufio.Reader, projectID string) {
	info("Found gcloud, enabling Health API...")
	enableCmd := exec.Command("gcloud", "services", "enable", "health.googleapis.com", "--project", projectID)
	enableCmd.Stderr = setupStderr
	if err := enableCmd.Run(); err != nil {
		warn(fmt.Sprintf("Could not enable via gcloud: %v", err))
		info(fmt.Sprintf("Enable manually: %shttps://console.cloud.google.com/apis/api/health.googleapis.com?project=%s%s", cyan, projectID, reset))
		if !setupNoPrompt {
			blank()
			promptInput(reader, "Press Enter when done", "")
		}
		return
	}
	success("Health API enabled")
}

func promptManualEnableAPI(reader *bufio.Reader, projectID string) {
	info("gcloud not found. Enable the Health API manually:")
	info(fmt.Sprintf("%shttps://console.cloud.google.com/apis/api/health.googleapis.com?project=%s%s", cyan, projectID, reset))
	if setupNoPrompt {
		warn("--no-prompt set; continuing without confirmation (re-run with --skip-enable-api to silence this)")
	} else {
		blank()
		promptInput(reader, "Press Enter when done", "")
	}
	success("Continuing")
}
