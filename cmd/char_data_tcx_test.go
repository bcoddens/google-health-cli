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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ghealth/pkg/types"
)

const dataCharValidTCX = `<?xml version="1.0" encoding="UTF-8"?>
<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2">
  <Activities>
    <Activity Sport="Running">
      <Id>2026-03-01T10:00:00Z</Id>
      <Lap StartTime="2026-03-01T10:00:00Z">
        <Track>
          <Trackpoint>
            <Time>2026-03-01T10:00:00Z</Time>
            <Position><LatitudeDegrees>1.5</LatitudeDegrees><LongitudeDegrees>2.5</LongitudeDegrees></Position>
            <AltitudeMeters>10.5</AltitudeMeters>
            <DistanceMeters>0</DistanceMeters>
            <HeartRateBpm><Value>120</Value></HeartRateBpm>
          </Trackpoint>
        </Track>
      </Lap>
    </Activity>
  </Activities>
</TrainingCenterDatabase>`

const dataCharLaplessTCX = `<?xml version="1.0" encoding="UTF-8"?>
<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2">
  <Activities>
    <Activity Sport="Running">
      <Id>2026-03-01T10:00:00Z</Id>
    </Activity>
  </Activities>
</TrainingCenterDatabase>`

// runExportTCX sets the flags on a freshly built export-tcx command and
// invokes its RunE directly (bypassing cobra arg parsing, which the target
// functions don't depend on).
func dataCharRunExportTCX(t *testing.T, dt *types.DataType, id, output, as string) error {
	t.Helper()
	cmd := newExportTCXCommand(dt)
	if id != "" {
		if err := cmd.Flags().Set("id", id); err != nil {
			t.Fatalf("set --id: %v", err)
		}
	}
	if output != "" {
		if err := cmd.Flags().Set("output", output); err != nil {
			t.Fatalf("set --output: %v", err)
		}
	}
	if as != "" {
		if err := cmd.Flags().Set("as", as); err != nil {
			t.Fatalf("set --as: %v", err)
		}
	}
	return cmd.RunE(cmd, nil)
}

// Happy path, --as tcx (default): the raw server payload is written
// byte-for-byte to the output file, and a one-line summary is printed.
func TestDataCharExportTCX_RawHappyPath(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("alt"); got != "media" {
			t.Errorf("alt query = %q, want media", got)
		}
		if !strings.HasSuffix(r.URL.Path, "/dataTypes/exercise/dataPoints/abc123:exportExerciseTcx") {
			t.Errorf("path = %s, want .../exercise/dataPoints/abc123:exportExerciseTcx", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(dataCharValidTCX))
	})

	dir := t.TempDir()
	out := filepath.Join(dir, "activity.tcx")
	stderr, err := dataCharCaptureStdout(t, func() error {
		return dataCharRunExportTCX(t, types.Get("exercise"), "abc123", out, "tcx")
	})
	if err != nil {
		t.Fatalf("export-tcx error: %v", err)
	}
	_ = stderr // RunE writes its summary to os.Stderr, not stdout; captured separately below if needed.

	got, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("read output file: %v", rerr)
	}
	if string(got) != dataCharValidTCX {
		t.Errorf("file content mismatch:\ngot:  %s\nwant: %s", got, dataCharValidTCX)
	}
}

// Happy path, --as csv: the TCX payload is converted, and the file gets one
// data row for the single trackpoint plus a header.
func TestDataCharExportTCX_CSVHappyPath(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(dataCharValidTCX))
	})

	dir := t.TempDir()
	out := filepath.Join(dir, "activity.csv")
	err := dataCharRunExportTCX(t, types.Get("exercise"), "abc123", out, "csv")
	if err != nil {
		t.Fatalf("export-tcx error: %v", err)
	}

	got, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("read output file: %v", rerr)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv lines = %d, want 2 (header + 1 row):\n%s", len(lines), got)
	}
	wantHeader := "time,activity,lap,sport,latitude_deg,longitude_deg,altitude_m,distance_m,heart_rate_bpm,cadence_rpm,speed_mps,watts"
	if lines[0] != wantHeader {
		t.Errorf("csv header = %q, want %q", lines[0], wantHeader)
	}
	if !strings.HasPrefix(lines[1], "2026-03-01T10:00:00Z,1,1,Running,1.5,2.5,10.5,0,120,,,") {
		t.Errorf("csv row = %q", lines[1])
	}
}

// A lap-less activity converts to a header-only CSV (0 data rows), which is
// not an error.
func TestDataCharExportTCX_CSVLapless(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(dataCharLaplessTCX))
	})

	dir := t.TempDir()
	out := filepath.Join(dir, "activity.csv")
	err := dataCharRunExportTCX(t, types.Get("exercise"), "abc123", out, "csv")
	if err != nil {
		t.Fatalf("export-tcx error: %v", err)
	}
	got, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("read output file: %v", rerr)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("csv lines = %d, want 1 (header only):\n%s", len(lines), got)
	}
}

// --output - writes the payload to stdout instead of a file.
func TestDataCharExportTCX_StdoutOutput(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(dataCharValidTCX))
	})

	stdout, err := dataCharCaptureStdout(t, func() error {
		return dataCharRunExportTCX(t, types.Get("exercise"), "abc123", "-", "tcx")
	})
	if err != nil {
		t.Fatalf("export-tcx error: %v", err)
	}
	if stdout != dataCharValidTCX {
		t.Errorf("stdout = %q, want the raw TCX payload", stdout)
	}
}

// Missing --id fails validation before any HTTP call.
func TestDataCharExportTCX_MissingID(t *testing.T) {
	called := false
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	err := dataCharRunExportTCX(t, types.Get("exercise"), "", "/tmp/x.tcx", "tcx")
	assertValidationError(t, err, "--id is required")
	if called {
		t.Error("HTTP request made despite missing --id")
	}
}

// Missing --output fails validation before any HTTP call.
func TestDataCharExportTCX_MissingOutput(t *testing.T) {
	called := false
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	err := dataCharRunExportTCX(t, types.Get("exercise"), "abc123", "", "tcx")
	assertValidationError(t, err, "--output is required")
	if called {
		t.Error("HTTP request made despite missing --output")
	}
}

// An invalid --as value fails validation before any HTTP call.
func TestDataCharExportTCX_InvalidAsValue(t *testing.T) {
	called := false
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	err := dataCharRunExportTCX(t, types.Get("exercise"), "abc123", "/tmp/x.tcx", "xml")
	assertValidationError(t, err, "invalid --as value: xml")
	if called {
		t.Error("HTTP request made despite invalid --as value")
	}
}

// A 4xx API error is surfaced as a structured CLIError.
func TestDataCharExportTCX_APIErrorPassthrough(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"data point not found","status":"NOT_FOUND"}}`))
	})
	err := dataCharRunExportTCX(t, types.Get("exercise"), "missing-id", "/tmp/x.tcx", "tcx")
	if err == nil {
		t.Fatal("export-tcx error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "data point not found") {
		t.Errorf("error = %v, want it to mention the API message", err)
	}
}

// A CSV conversion failure (malformed TCX/XML) surfaces a validation error
// naming the failure, not a raw XML parser panic/error.
func TestDataCharExportTCX_CSVConversionError(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte("not valid xml <<<"))
	})
	err := dataCharRunExportTCX(t, types.Get("exercise"), "abc123", "/tmp/x.csv", "csv")
	assertValidationError(t, err, "convert TCX to CSV")
}
