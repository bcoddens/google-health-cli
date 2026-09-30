package output

import (
	"encoding/json"
	"io"
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

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestPrintToFileTableKeepsHintsAndPageTokenOnStderr(t *testing.T) {
	data := json.RawMessage(`{"dataPoints":[{"a":1}],"nextPageToken":"tok-2","_hints":["more data available"]}`)
	path := filepath.Join(t.TempDir(), "out.txt")
	var err error
	stderr := captureStderr(t, func() { err = PrintToFile("table", data, path) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hint: more data available", "nextPageToken: tok-2"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q, got %q", want, stderr)
		}
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "tok-2") {
		t.Errorf("table file must contain rows only, got %q", body)
	}
}

func TestJSONOutputPreservesNumbersAndKeyOrder(t *testing.T) {
	// 9007199254740993 (2^53+1) is not representable as a float64, and "z"
	// precedes "a" in the source; re-encoding through interface{} would turn
	// the former into 9007199254740992 and reorder the latter.
	raw := json.RawMessage(`{"z":9007199254740993,"a":1.10}`)
	wantOrder := `"z": 9007199254740993,` + "\n" + `  "a": 1.10`

	path := filepath.Join(t.TempDir(), "out.json")
	if err := PrintToFile("json", raw, path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), wantOrder) {
		t.Errorf("PrintToFile json = %q, want it to contain %q", got, wantOrder)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	printErr := PrintJSON(raw)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if printErr != nil {
		t.Fatal(printErr)
	}
	if !strings.Contains(string(out), wantOrder) {
		t.Errorf("PrintJSON = %q, want it to contain %q", out, wantOrder)
	}
}
