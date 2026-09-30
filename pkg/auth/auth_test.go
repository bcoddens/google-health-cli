package auth

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScopePreset(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "category prefers readonly", in: "sleep", want: []string{"sleep.readonly"}},
		{name: "multiple categories keep order", in: "sleep, nutrition", want: []string{"sleep.readonly", "nutrition.readonly"}},
		{name: "readonly-only category", in: "ecg", want: []string{"ecg.readonly"}},
		{name: "webhooks has no readonly variant", in: "webhooks", want: []string{"cloud-platform"}},
		{name: "unknown category", in: "bogus", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "only separators", in: " , ,", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScopePreset(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ScopePreset(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScopePreset(%q) error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ScopePreset(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestScopePresetReadonlyOnlyReturnsReadonlyScopes(t *testing.T) {
	got, err := ScopePreset("readonly")
	if err != nil || len(got) == 0 {
		t.Fatalf("readonly preset: %v, %v", got, err)
	}
	for _, s := range got {
		if !strings.HasSuffix(s, ".readonly") {
			t.Errorf("readonly preset contains writable scope %q", s)
		}
	}
}

func TestFullScope(t *testing.T) {
	tests := map[string]string{
		"sleep.readonly":                        ScopePrefix + "sleep.readonly",
		"cloud-platform":                        CloudPlatformScope,
		"https://example.com/auth/custom.scope": "https://example.com/auth/custom.scope",
	}
	for in, want := range tests {
		if got := FullScope(in); got != want {
			t.Errorf("FullScope(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoredCredentialsValidate(t *testing.T) {
	if err := (*StoredCredentials)(nil).Validate(); err == nil {
		t.Error("nil credentials must be invalid")
	}
	if err := (&StoredCredentials{}).Validate(); err == nil {
		t.Error("credentials without any token must be invalid")
	}
	c := &StoredCredentials{RefreshToken: "r"}
	if err := c.Validate(); err != nil {
		t.Fatalf("refresh-token-only credentials must be valid: %v", err)
	}
	if c.TokenType != "Bearer" {
		t.Errorf("TokenType defaulted to %q, want Bearer", c.TokenType)
	}
}

func TestWriteSecretFileLeavesNoTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory at the target path makes the final rename fail.
	target := filepath.Join(dir, "secret.json")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteSecretFile(target, []byte("{}")); err == nil {
		t.Fatal("expected rename over a directory to fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "secret.json" {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestWriteSecretFileCreatesPrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "creds.json")
	if err := WriteSecretFile(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if m := di.Mode().Perm(); m != 0700 {
		t.Errorf("directory mode = %o, want 0700", m)
	}
	if m := modeOf(t, path); m != 0600 {
		t.Errorf("file mode = %o, want 0600", m)
	}
}
