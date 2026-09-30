package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func coverageUserGlobals(t *testing.T) {
	t.Helper()
	oldDry, oldJSON, oldID := flagDryRun, flagUserJSON, flagPairedDeviceID
	flagDryRun, flagUserJSON, flagPairedDeviceID = false, "", ""
	t.Cleanup(func() { flagDryRun, flagUserJSON, flagPairedDeviceID = oldDry, oldJSON, oldID })
}

func TestCoverageUserGetCommands(t *testing.T) {
	coverageUserGlobals(t)
	var paths []string
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")

		_, _ = w.Write([]byte(`{"name":"ok"}`))
	})
	flagPairedDeviceID = "device-1"
	commands := []func(*cobra.Command, []string) error{
		runUserIdentity, runUserProfileGet, runUserSettingsGet,
		runUserIrnProfile, runUserPairedDevicesList, runUserPairedDevicesGet,
	}
	for _, run := range commands {
		out, err := dataCharCaptureStdout(t, func() error { return run(nil, nil) })
		if err != nil || !strings.Contains(out, `"name": "ok"`) {
			t.Fatalf("get output=%q err=%v", out, err)
		}
	}
	want := []string{"/users/me/identity", "/users/me/profile", "/users/me/settings", "/users/me/irnProfile", "/users/me/pairedDevices", "/users/me/pairedDevices/device-1"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %#v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestCoverageUserUpdateAndDryRun(t *testing.T) {
	coverageUserGlobals(t)
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s content-type=%q", r.Method, r.Header.Get("Content-Type"))
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"updated":true}`))
	})
	flagUserJSON = `{"height":180}`
	for _, run := range []func(*cobra.Command, []string) error{runUserProfileUpdate, runUserSettingsUpdate} {
		out, err := dataCharCaptureStdout(t, func() error { return run(nil, nil) })
		if err != nil || !strings.Contains(out, `"updated": true`) {
			t.Fatalf("update output=%q err=%v", out, err)
		}
	}

	flagDryRun = true
	out, err := dataCharCaptureStdout(t, func() error { return doUserGet("/users/me/profile") })
	if err != nil || !strings.Contains(out, `"method": "GET"`) {
		t.Fatalf("get dry run=%q err=%v", out, err)
	}
	out, err = dataCharCaptureStdout(t, func() error { return doUserUpdate("/users/me/profile") })
	if err != nil || !strings.Contains(out, `"method": "PATCH"`) {
		t.Fatalf("update dry run=%q err=%v", out, err)
	}
}

func TestCoverageUserUpdateRejectsInvalidJSON(t *testing.T) {
	coverageUserGlobals(t)
	flagUserJSON = `{broken`
	if err := doUserUpdate("/users/me/profile"); err == nil || !strings.Contains(err.Error(), "invalid JSON body") {
		t.Fatalf("invalid JSON error = %v", err)
	}
}
