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
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ghealth/pkg/config"
)

// TestMiscCharSplitTypes pins the comma-splitting/trimming/empty-input
// behaviour used by the subscriber/subscription create/update handlers.
func TestMiscCharSplitTypes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , , b ", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitTypes(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitTypes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestMiscCharProjectPath_NoProjectID pins the validation error returned
// when no project ID is configured.
func TestMiscCharProjectPath_NoProjectID(t *testing.T) {
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	_, err := projectPath()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no project ID configured") {
		t.Errorf("err = %q, want to contain 'no project ID configured'", err.Error())
	}
}

// miscCharWebhookDryRunURL runs fn under --dry-run and returns the "url"
// field from the resulting DryRun JSON, pinning the exact request path
// built by the handler (including the "/subscribers/" path segments).
func miscCharWebhookDryRunURL(t *testing.T, fn func() error) map[string]interface{} {
	t.Helper()
	flagDryRun = true
	defer func() { flagDryRun = false }()

	out := miscCharCaptureStdout(t, func() {
		if err := fn(); err != nil {
			t.Fatalf("handler: %v", err)
		}
	})
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output not JSON: %v\noutput: %s", err, out)
	}
	return m
}

func miscCharSetProjectID(t *testing.T, projectID string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	tomlContent := "[default]\nproject_id = \"" + projectID + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestMiscCharRunSubscribersCreate_DryRunURL pins the create-subscriber
// request path and content type.
func TestMiscCharRunSubscribersCreate_DryRunURL(t *testing.T) {
	miscCharSetProjectID(t, "proj-123")
	flagWhEndpointURI = "https://example.com/hook"
	flagWhSecret = "shh"
	flagWhTypes = ""
	flagWhPolicy = "AUTOMATIC"
	flagWhSubscriberID = ""
	defer func() {
		flagWhEndpointURI, flagWhSecret, flagWhTypes, flagWhPolicy, flagWhSubscriberID = "", "", "", "AUTOMATIC", ""
	}()

	m := miscCharWebhookDryRunURL(t, func() error {
		return runSubscribersCreate(webhooksSubscribersCreateCmd, nil)
	})
	wantURLSuffix := "/projects/proj-123/subscribers"
	if url, _ := m["url"].(string); !strings.HasSuffix(url, wantURLSuffix) {
		t.Errorf("url = %v, want suffix %q", m["url"], wantURLSuffix)
	}
	headers, _ := m["headers"].(map[string]interface{})
	if headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %v, want application/json", headers["Content-Type"])
	}
}

// TestMiscCharRunSubscribersUpdate_DryRunURL pins the
// "/subscribers/{id}" update path.
func TestMiscCharRunSubscribersUpdate_DryRunURL(t *testing.T) {
	miscCharSetProjectID(t, "proj-123")
	flagWhSubscriberID = "sub-1"
	flagWhJSON = `{"endpointUri":"https://x"}`
	flagWhUpdateMask = ""
	defer func() { flagWhSubscriberID, flagWhJSON, flagWhUpdateMask = "", "", "" }()

	m := miscCharWebhookDryRunURL(t, func() error {
		return runSubscribersUpdate(webhooksSubscribersUpdateCmd, nil)
	})
	wantURLSuffix := "/projects/proj-123/subscribers/sub-1"
	if url, _ := m["url"].(string); !strings.HasSuffix(url, wantURLSuffix) {
		t.Errorf("url = %v, want suffix %q", m["url"], wantURLSuffix)
	}
}

// TestMiscCharRunSubscriptionsCreate_DryRunURL pins the
// "/subscribers/{id}/subscriptions" create path.
func TestMiscCharRunSubscriptionsCreate_DryRunURL(t *testing.T) {
	miscCharSetProjectID(t, "proj-123")
	flagWhSubscriberID = "sub-1"
	flagWhTypes = "steps"
	flagWhUser = "users/me"
	flagWhSubscriptionID = ""
	defer func() {
		flagWhSubscriberID, flagWhTypes, flagWhUser, flagWhSubscriptionID = "", "", "users/me", ""
	}()

	m := miscCharWebhookDryRunURL(t, func() error {
		return runSubscriptionsCreate(webhooksSubscriptionsCreateCmd, nil)
	})
	wantURLSuffix := "/projects/proj-123/subscribers/sub-1/subscriptions"
	if url, _ := m["url"].(string); !strings.HasSuffix(url, wantURLSuffix) {
		t.Errorf("url = %v, want suffix %q", m["url"], wantURLSuffix)
	}
}

// TestMiscCharRunSubscriptionsDelete_DryRunURL pins the
// "/subscribers/{id}/subscriptions/{id}" delete path.
func TestMiscCharRunSubscriptionsDelete_DryRunURL(t *testing.T) {
	miscCharSetProjectID(t, "proj-123")
	flagWhSubscriberID = "sub-1"
	flagWhSubscriptionID = "subn-2"
	defer func() { flagWhSubscriberID, flagWhSubscriptionID = "", "" }()

	m := miscCharWebhookDryRunURL(t, func() error {
		return runSubscriptionsDelete(webhooksSubscriptionsDeleteCmd, nil)
	})
	wantURLSuffix := "/projects/proj-123/subscribers/sub-1/subscriptions/subn-2"
	if url, _ := m["url"].(string); !strings.HasSuffix(url, wantURLSuffix) {
		t.Errorf("url = %v, want suffix %q", m["url"], wantURLSuffix)
	}
}
