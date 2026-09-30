package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ghealth/pkg/client"
	"ghealth/pkg/config"
)

func coverageConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	t.Setenv("GHEALTH_PROFILE", "")
	oldFormat, oldProfile := flagFormat, flagProfile
	flagFormat, flagProfile = "json", ""
	t.Cleanup(func() { flagFormat, flagProfile = oldFormat, oldProfile })
}

func coverageRunStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var runErr error
	out := miscCharCaptureStdout(t, func() { runErr = fn() })
	return out, runErr
}

func TestCoverageConfigShowJSONAndTOML(t *testing.T) {
	coverageConfigEnv(t)
	cfg := &config.Config{Default: config.ProfileConfig{ProjectID: "project-1", Format: "json"}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	out, err := coverageRunStdout(t, func() error { return runConfigShow(configShowCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	var got config.Config
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, out)
	}
	if got.Default.ProjectID != "project-1" {
		t.Fatalf("project_id = %q", got.Default.ProjectID)
	}

	flagFormat = "table"
	out, err = coverageRunStdout(t, func() error { return runConfigShow(configShowCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `project_id = "project-1"`) {
		t.Fatalf("TOML output missing project: %s", out)
	}
}

func TestCoverageConfigSetValuesAndValidation(t *testing.T) {
	coverageConfigEnv(t)
	cases := []struct{ key, value string }{
		{"project_id", "project-2"},
		{"format", "CSV"},
		{"timezone", "Europe/London"},
	}
	for _, tc := range cases {
		out, err := coverageRunStdout(t, func() error { return runConfigSet(configSetCmd, []string{tc.key, tc.value}) })
		if err != nil {
			t.Fatalf("set %s: %v", tc.key, err)
		}
		if !strings.Contains(out, `"status": "updated"`) {
			t.Fatalf("set output: %s", out)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.ProjectID != "project-2" || cfg.Default.Format != "csv" || cfg.Default.Timezone != "Europe/London" {
		t.Fatalf("saved config = %+v", cfg.Default)
	}

	for _, args := range [][]string{{"format", "xml"}, {"timezone", "Not/AZone"}, {"unknown", "x"}} {
		_, err := coverageRunStdout(t, func() error { return runConfigSet(configSetCmd, args) })
		var cliErr *client.CLIError
		if !clientErrAs(err, &cliErr) || cliErr.Code != client.ExitValidation {
			t.Fatalf("set %v error = %#v", args, err)
		}
	}
}

func TestCoverageConfigNamedProfilesListAndSwitch(t *testing.T) {
	coverageConfigEnv(t)
	flagProfile = "work"
	if _, err := coverageRunStdout(t, func() error { return runConfigSet(configSetCmd, []string{"project_id", "work-project"}) }); err != nil {
		t.Fatal(err)
	}

	out, err := coverageRunStdout(t, func() error { return runConfigProfilesList(configProfilesListCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name": "work"`) || !strings.Contains(out, `"active": true`) {
		t.Fatalf("profiles output: %s", out)
	}

	out, err = coverageRunStdout(t, func() error { return runConfigProfilesSwitch(configProfilesSwitchCmd, []string{"work"}) })
	if err != nil || !strings.Contains(out, `"profile": "work"`) {
		t.Fatalf("switch output=%s err=%v", out, err)
	}
	_, err = coverageRunStdout(t, func() error { return runConfigProfilesSwitch(configProfilesSwitchCmd, []string{"missing"}) })
	if err == nil || !strings.Contains(err.Error(), "profile 'missing' not found") {
		t.Fatalf("missing profile error = %v", err)
	}
}

// clientErrAs keeps the tests focused on the stable CLI error contract.
func clientErrAs(err error, target **client.CLIError) bool {
	got, ok := client.AsCLIError(err)
	if ok {
		*target = got
	}
	return ok
}
