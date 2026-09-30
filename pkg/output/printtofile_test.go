package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintToFileRejectsInvalidJSON(t *testing.T) {
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out.txt")
			err := PrintToFile(format, json.RawMessage(`{not json`), path)
			if err == nil {
				t.Fatal("expected an error for invalid JSON, got nil")
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("no file must be written for invalid input (stat err: %v)", statErr)
			}
		})
	}
}

func TestPrintToFileJSONIsIndented(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	if err := PrintToFile("json", json.RawMessage(`{"dataPoints":[{"a":1}]}`), path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\n  \"dataPoints\"") {
		t.Fatalf("output not indented: %q", got)
	}
}
