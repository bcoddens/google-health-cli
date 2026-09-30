package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadAndWriteCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "discovery.json")
	data := json.RawMessage(`{"schemas":{"X":{}}}`)
	if err := writeCache(path, data); err != nil {
		t.Fatal(err)
	}
	got, err := readCache(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("readCache = %s, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode = %o", info.Mode().Perm())
	}
}

func TestReadCacheFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := readCache(path); !os.IsNotExist(err) {
		t.Fatalf("missing cache error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-cacheTTL - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := readCache(path); err == nil || !strings.Contains(err.Error(), "cache expired") {
		t.Fatalf("stale cache error = %v", err)
	}
}
