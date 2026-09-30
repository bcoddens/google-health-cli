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

package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PrintToFile with format=csv and non-empty rows writes the CSV file and
// prints a "Wrote N rows" summary with a Columns/Preview section to stdout.
func TestChar_PrintToFileCSV_WritesRowsAndPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	data := json.RawMessage(`{"dataPoints": [{"a": 1, "b": "x"}, {"a": 2, "b": "y"}]}`)

	stdout := charCaptureStdout(t, func() {
		if err := PrintToFile("csv", data, path); err != nil {
			t.Fatal(err)
		}
	})

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a,b\n1,x\n2,y\n") {
		t.Fatalf("unexpected CSV file content: %q", got)
	}
	if !strings.Contains(stdout, "Wrote 2 rows to "+path) {
		t.Errorf("expected row-count summary, got: %q", stdout)
	}
	if !strings.Contains(stdout, "Columns: a, b") {
		t.Errorf("expected sorted column list, got: %q", stdout)
	}
	if !strings.Contains(stdout, "Preview:\na,b\n1,x\n2,y\n") {
		t.Errorf("expected a full preview (<=3 rows), got: %q", stdout)
	}
}

// PrintToFile with format=csv caps the preview at 3 rows even when the
// file itself contains every row.
func TestChar_PrintToFileCSV_PreviewCapsAtThreeRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	data := json.RawMessage(`{"dataPoints": [{"a":1},{"a":2},{"a":3},{"a":4},{"a":5}]}`)

	stdout := charCaptureStdout(t, func() {
		if err := PrintToFile("csv", data, path); err != nil {
			t.Fatal(err)
		}
	})

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(got), "\n") != 6 {
		t.Errorf("expected all 5 rows + header in the file, got: %q", got)
	}
	preview := stdout[strings.Index(stdout, "Preview:\n")+len("Preview:\n"):]
	if strings.Count(preview, "\n") != 4 {
		t.Errorf("expected header + 3 rows in the preview, got: %q", preview)
	}
}

// PrintToFile with format=csv and a list-shaped-but-empty result writes an
// empty CSV file (never a JSON dump) and emits a plain "Wrote" summary.
func TestChar_PrintToFileCSV_EmptyListShapedWritesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	data := json.RawMessage(`{"dataPoints": []}`)

	stdout := charCaptureStdout(t, func() {
		if err := PrintToFile("csv", data, path); err != nil {
			t.Fatal(err)
		}
	})

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected an empty CSV file, got %q", got)
	}
	if !strings.Contains(stdout, "Wrote "+path) || strings.Contains(stdout, "rows to") {
		t.Errorf("expected the plain 'Wrote <path>' summary, got %q", stdout)
	}
}

// PrintToFile with format=csv and a non-tabular payload (no row-array key)
// falls back to writing the raw JSON to the file.
func TestChar_PrintToFileCSV_NonTabularFallsBackToJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	data := json.RawMessage(`{"status": "ok"}`)

	if err := PrintToFile("csv", data, path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("expected raw JSON fallback, got %q want %q", got, data)
	}
}

// charCaptureStdout runs fn with os.Stdout redirected and returns what it wrote.
func charCaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	return string(buf[:n])
}
