package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveCredentialsTightensExistingFileMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredentials(&StoredCredentials{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if m := modeOf(t, path); m != 0600 {
		t.Fatalf("credentials mode = %o, want 0600", m)
	}
	got, err := LoadCredentials()
	if err != nil || got.RefreshToken != "r" {
		t.Fatalf("round trip failed: %+v, %v", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestSavePendingAuthTightensExistingFileMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	path := PendingAuthPath()
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SavePendingAuth(&PendingAuth{}); err != nil {
		t.Fatal(err)
	}
	if m := modeOf(t, path); m != 0600 {
		t.Fatalf("pending_auth mode = %o, want 0600", m)
	}
}

func TestScopePresetAllExcludesCloudPlatform(t *testing.T) {
	all, err := ScopePreset("all")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s == "cloud-platform" {
			t.Fatal("'all' preset must not include cloud-platform")
		}
	}
	if len(all) != len(AllScopes)-1 {
		t.Fatalf("all preset has %d scopes, want %d", len(all), len(AllScopes)-1)
	}
	web, err := ScopePreset("webhooks")
	if err != nil || len(web) != 1 || web[0] != "cloud-platform" {
		t.Fatalf("webhooks category must still resolve to cloud-platform: %v, %v", web, err)
	}
}

func modeOf(t *testing.T, p string) os.FileMode {
	t.Helper()
	i, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return i.Mode().Perm()
}
