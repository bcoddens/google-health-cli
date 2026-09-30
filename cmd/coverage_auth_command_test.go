package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ghealth/pkg/auth"
	"ghealth/pkg/config"
)

func coverageAuthCommandGlobals(t *testing.T) {
	t.Helper()
	miscCharIsolateAuth(t)
	oldScopes, oldPreset := authLoginScopes, authLoginScopesPreset
	oldImport, oldValidate := authImportFile, authStatusValidate
	authLoginScopes, authLoginScopesPreset, authImportFile, authStatusValidate = "", "", "", false
	t.Cleanup(func() {
		authLoginScopes, authLoginScopesPreset = oldScopes, oldPreset
		authImportFile, authStatusValidate = oldImport, oldValidate
	})
}

func TestCoverageResolveScopesPrecedence(t *testing.T) {
	coverageAuthCommandGlobals(t)
	authLoginScopes = " sleep.readonly, ,steps.readonly "
	got, err := resolveScopes()
	if err != nil || strings.Join(got, ",") != "sleep.readonly,steps.readonly" {
		t.Fatalf("literal scopes = %#v, %v", got, err)
	}
	authLoginScopes = ""
	authLoginScopesPreset = "readonly"
	got, err = resolveScopes()
	if err != nil || len(got) == 0 {
		t.Fatalf("preset scopes = %#v, %v", got, err)
	}
	authLoginScopesPreset = "bogus"
	if _, err := resolveScopes(); err == nil {
		t.Fatal("unknown preset unexpectedly succeeded")
	}

	authLoginScopesPreset = ""
	cfg := &config.Config{Default: config.ProfileConfig{Scopes: []string{"profile.scope"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got, err = resolveScopes()
	if err != nil || len(got) != 1 || got[0] != "profile.scope" {
		t.Fatalf("profile scopes = %#v, %v", got, err)
	}
}

func TestCoverageAuthImportExportAndLogout(t *testing.T) {
	coverageAuthCommandGlobals(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	input := `{"access_token":"imported-token","token_type":"Bearer","expiry":"` + expiry.Format(time.RFC3339) + `","email":"person@example.com","scopes":["sleep.readonly"]}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	authImportFile = path
	out, err := dataCharCaptureStdout(t, func() error { return runAuthImport(nil, nil) })
	if err != nil || !strings.Contains(out, `"status": "imported"`) || !strings.Contains(out, "no refresh_token") {
		t.Fatalf("import output=%q err=%v", out, err)
	}
	out, err = dataCharCaptureStdout(t, func() error { return runAuthExport(nil, nil) })
	if err != nil || !strings.Contains(out, `"access_token": "imported-token"`) {
		t.Fatalf("export output=%q err=%v", out, err)
	}
	out, err = dataCharCaptureStdout(t, func() error { return runAuthLogout(nil, nil) })
	if err != nil || !strings.Contains(out, `"status": "logged_out"`) {
		t.Fatalf("logout output=%q err=%v", out, err)
	}
	out, err = dataCharCaptureStdout(t, func() error { return runAuthLogout(nil, nil) })
	if err != nil || !strings.Contains(out, `"status": "not_authenticated"`) {
		t.Fatalf("second logout output=%q err=%v", out, err)
	}
}

func TestCoverageAuthImportValidationFailures(t *testing.T) {
	coverageAuthCommandGlobals(t)
	missing := filepath.Join(t.TempDir(), "missing.json")
	authImportFile = missing
	if err := runAuthImport(nil, nil); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("missing import error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	authImportFile = path
	if err := runAuthImport(nil, nil); err == nil || !strings.Contains(err.Error(), "invalid credentials JSON") {
		t.Fatalf("invalid import error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"token_type":"Bearer"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runAuthImport(nil, nil); err == nil || !strings.Contains(err.Error(), "access_token or refresh_token") {
		t.Fatalf("empty credentials error = %v", err)
	}

	refreshOnly := `{"refresh_token":"refresh","token_type":"Bearer"}`
	if err := os.WriteFile(path, []byte(refreshOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runAuthImport(nil, nil); err == nil || !strings.Contains(err.Error(), "no client_secret.json") {
		t.Fatalf("refresh-only import error = %v", err)
	}
}

func TestCoverageAuthStatusOfflineModes(t *testing.T) {
	coverageAuthCommandGlobals(t)
	t.Setenv("GHEALTH_ACCESS_TOKEN", "env-token")
	out, err := dataCharCaptureStdout(t, func() error { return runAuthStatus(nil, nil) })
	if err != nil || !strings.Contains(out, `"auth_method": "env_token"`) || !strings.Contains(out, `"validated": false`) {
		t.Fatalf("env status=%q err=%v", out, err)
	}
	t.Setenv("GHEALTH_ACCESS_TOKEN", "")
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("GHEALTH_CREDENTIALS_FILE", path)
	out, err = dataCharCaptureStdout(t, func() error { return runAuthStatus(nil, nil) })
	if err != nil || !strings.Contains(out, `"auth_method": "credentials_file"`) {
		t.Fatalf("file status=%q err=%v", out, err)
	}

	t.Setenv("GHEALTH_CREDENTIALS_FILE", "")
	creds := &auth.StoredCredentials{AccessToken: "stored", Expiry: time.Now().Add(time.Hour), Email: "person@example.com"}
	if err := auth.SaveCredentials(creds); err != nil {
		t.Fatal(err)
	}
	out, err = dataCharCaptureStdout(t, func() error { return runAuthStatus(nil, nil) })
	if err != nil || !strings.Contains(out, `"authenticated": true`) || !strings.Contains(out, `"auth_method": "oauth"`) {
		t.Fatalf("stored status=%q err=%v", out, err)
	}
}
