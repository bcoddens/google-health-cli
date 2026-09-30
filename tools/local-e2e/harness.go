//go:build local_e2e

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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	commandTimeout  = 20 * time.Second
	blockedEndpoint = "http://127.0.0.1:1"
)

type harness struct {
	repoRoot string
	workDir  string
	binary   string
	verbose  bool
}

type commandResult struct {
	args     []string
	stdout   []byte
	stderr   []byte
	exitCode int
	duration time.Duration
}

func newHarness(repoRoot, workDir string, verbose bool) *harness {
	return &harness{
		repoRoot: repoRoot,
		workDir:  workDir,
		binary:   filepath.Join(workDir, "ghealth"),
		verbose:  verbose,
	}
}

func (h *harness) build() error {
	//nolint:gosec // The output path is created by this harness, not user input.
	cmd := exec.Command("go", "build", "-o", h.binary, ".")
	cmd.Dir = h.repoRoot
	cmd.Env = os.Environ()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if h.verbose {
		fmt.Printf("BUILD go build -o %s .\n", h.binary)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build ghealth: %w\n%s", err, output.String())
	}
	return nil
}

func (h *harness) scenarioEnv(name string) (map[string]string, string, error) {
	dir := filepath.Join(h.workDir, "scenarios", sanitizeName(name))
	configDir := filepath.Join(dir, "config")
	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create scenario config directory: %w", err)
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create scenario home directory: %w", err)
	}
	//nolint:gosec // This is an explicit non-secret fixture token for a local server.
	env := map[string]string{
		"GHEALTH_ACCESS_TOKEN":           "local-e2e-token",
		"GHEALTH_CONFIG_DIR":             configDir,
		"GHEALTH_FORMAT":                 "json",
		"GOOGLE_APPLICATION_CREDENTIALS": filepath.Join(configDir, "missing-adc.json"),
		"HOME":                           homeDir,
		"XDG_CONFIG_HOME":                filepath.Join(homeDir, ".config"),
		"TZ":                             "UTC",
		"HTTP_PROXY":                     blockedEndpoint,
		"HTTPS_PROXY":                    blockedEndpoint,
		"ALL_PROXY":                      blockedEndpoint,
		"NO_PROXY":                       "127.0.0.1,localhost",
		"GCE_METADATA_HOST":              "127.0.0.1:1",
	}
	return env, configDir, nil
}

func (h *harness) run(env map[string]string, args ...string) commandResult {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	//nolint:gosec // The executable and scenario arguments are defined by this harness.
	cmd := exec.CommandContext(ctx, h.binary, args...)
	cmd.Dir = h.repoRoot
	cmd.Env = controlledEnvironment(env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if h.verbose {
		fmt.Printf("RUN   ghealth %s\n", strings.Join(args, " "))
	}
	started := time.Now()
	err := cmd.Run()
	result := commandResult{
		args:     append([]string(nil), args...),
		stdout:   stdout.Bytes(),
		stderr:   stderr.Bytes(),
		exitCode: processExitCode(err),
		duration: time.Since(started),
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.exitCode = -1
		result.stderr = append(result.stderr, []byte("\ncommand exceeded local E2E timeout")...)
	}
	if h.verbose {
		fmt.Printf("      exit=%d duration=%s\n", result.exitCode, result.duration.Round(time.Millisecond))
		if len(result.stdout) > 0 {
			fmt.Printf("STDOUT\n%s", result.stdout)
		}
		if len(result.stderr) > 0 {
			fmt.Printf("STDERR\n%s", result.stderr)
		}
	}
	return result
}

func controlledEnvironment(overrides map[string]string) []string {
	drop := map[string]bool{
		"ALL_PROXY": true, "CLOUDSDK_CONFIG": true, "GCE_METADATA_HOST": true,
		"GOOGLE_APPLICATION_CREDENTIALS": true, "HOME": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
		"XDG_CONFIG_HOME": true,
	}
	base := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || drop[key] || strings.HasPrefix(key, "GHEALTH_") {
			continue
		}
		base[key] = value
	}
	for key, value := range overrides {
		if value == "" {
			delete(base, key)
			continue
		}
		base[key] = value
	}
	keys := make([]string, 0, len(base))
	for key := range base {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+base[key])
	}
	return env
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-")
	return replacer.Replace(strings.ToLower(name))
}

func (r commandResult) failure(message string) error {
	return fmt.Errorf("%s\ncommand: ghealth %s\nexit: %d\nstdout:\n%s\nstderr:\n%s",
		message, strings.Join(r.args, " "), r.exitCode, r.stdout, r.stderr)
}

func requireExit(r commandResult, want int) error {
	if r.exitCode != want {
		return r.failure(fmt.Sprintf("unexpected exit code: got %d, want %d", r.exitCode, want))
	}
	return nil
}

func requireEmpty(label string, data []byte, r commandResult) error {
	if len(bytes.TrimSpace(data)) != 0 {
		return r.failure(fmt.Sprintf("expected empty %s", label))
	}
	return nil
}

func requireContains(label string, data []byte, want string, r commandResult) error {
	if !bytes.Contains(data, []byte(want)) {
		return r.failure(fmt.Sprintf("%s does not contain %q", label, want))
	}
	return nil
}

func decodeJSONObject(data []byte) (map[string]interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return value, nil
}

func requireJSONField(obj map[string]interface{}, path string, want interface{}) error {
	var current interface{} = obj
	for _, part := range strings.Split(path, ".") {
		mapped, ok := current.(map[string]interface{})
		if !ok {
			return fmt.Errorf("JSON path %q: %q is not an object", path, part)
		}
		current, ok = mapped[part]
		if !ok {
			return fmt.Errorf("JSON path %q is missing %q", path, part)
		}
	}
	if fmt.Sprint(current) != fmt.Sprint(want) {
		return fmt.Errorf("JSON path %q = %v, want %v", path, current, want)
	}
	return nil
}
