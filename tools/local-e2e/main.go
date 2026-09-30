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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type scenario struct {
	name string
	run  func(*harness) error
}

func main() {
	os.Exit(run())
}

func run() int {
	var runFilter string
	var verbose bool
	var keep bool
	flag.StringVar(&runFilter, "run", "", "run scenarios whose names contain this text")
	flag.BoolVar(&verbose, "verbose", false, "print commands and captured output")
	flag.BoolVar(&keep, "keep", false, "keep the temporary workspace")
	flag.Parse()

	repoRoot, err := findRepoRoot()
	if err != nil {
		return fatal(err)
	}
	workDir, err := os.MkdirTemp("", "ghealth-local-e2e-")
	if err != nil {
		return fatal(fmt.Errorf("create temporary workspace: %w", err))
	}
	cleanup := true
	defer func() {
		if keep || !cleanup {
			fmt.Printf("Workspace: %s\n", workDir)
			return
		}
		_ = os.RemoveAll(workDir)
	}()

	h := newHarness(repoRoot, workDir, verbose)
	if err := h.build(); err != nil {
		cleanup = false
		return fatal(err)
	}

	selected := selectScenarios(allScenarios(), runFilter)
	if len(selected) == 0 {
		cleanup = false
		return fatal(fmt.Errorf("no scenario name contains %q", runFilter))
	}

	started := time.Now()
	failures := 0
	for _, current := range selected {
		if err := current.run(h); err != nil {
			failures++
			cleanup = false
			fmt.Printf("FAIL %-30s %v\n", current.name, err)
			continue
		}
		fmt.Printf("PASS %s\n", current.name)
	}
	fmt.Printf("\n%d passed, %d failed (%s)\n", len(selected)-failures, failures, time.Since(started).Round(time.Millisecond))
	if failures > 0 {
		return 1
	}
	return 0
}

func selectScenarios(scenarios []scenario, filter string) []scenario {
	if filter == "" {
		return scenarios
	}
	filter = strings.ToLower(filter)
	selected := make([]scenario, 0, len(scenarios))
	for _, current := range scenarios {
		if strings.Contains(strings.ToLower(current.name), filter) {
			selected = append(selected, current)
		}
	}
	return selected
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root containing go.mod and main.go not found")
		}
		dir = parent
	}
}

func fatal(err error) int {
	fmt.Fprintf(os.Stderr, "local E2E: %v\n", err)
	return 2
}
