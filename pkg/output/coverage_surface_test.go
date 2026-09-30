package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoveragePrintTableAndCSV(t *testing.T) {
	data := json.RawMessage(`{"dataPoints":[{"name":"walk","count":12,"active":true,"metrics":{"distance":1.25},"tags":["a","b"]}]}`)
	table := charCaptureStdout(t, func() {
		if err := PrintTable(data); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"ACTIVE", "COUNT", "NAME", "true", "walk", `{"distance":1.25}`} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q: %s", want, table)
		}
	}
	csv := charCaptureStdout(t, func() {
		if err := PrintCSV(data); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(csv, "active,count,metrics.distance,name,tags") || !strings.Contains(csv, "true,12,1.25,walk") {
		t.Fatalf("CSV output: %s", csv)
	}
}

func TestCoveragePrintFallbacksAndDispatcher(t *testing.T) {
	object := json.RawMessage(`{"status":"ok"}`)
	for _, tc := range []struct {
		format string
		want   string
	}{
		{"json", `"status": "ok"`},
		{"table", `"status": "ok"`},
		{"csv", `"status": "ok"`},
	} {
		out := charCaptureStdout(t, func() {
			if err := Print(tc.format, object); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, tc.want) {
			t.Errorf("Print(%s) = %q", tc.format, out)
		}
	}
	unknown := charCaptureStdout(t, func() {
		if err := Print("xml", object); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(unknown, `"status": "ok"`) {
		t.Fatalf("unknown format fallback = %q", unknown)
	}
	invalid := charCaptureStdout(t, func() {
		if err := PrintJSON(json.RawMessage(`not-json`)); err != nil {
			t.Fatal(err)
		}
	})
	if invalid != "not-json\n" {
		t.Fatalf("invalid JSON fallback = %q", invalid)
	}
}

func TestCoverageEmptyTabularOutputAndCounts(t *testing.T) {
	for _, data := range []json.RawMessage{[]byte(`[]`), []byte(`{"dataPoints":[]}`)} {
		out := charCaptureStdout(t, func() {
			if err := PrintCSV(data); err != nil {
				t.Fatal(err)
			}
		})
		if out != "" {
			t.Errorf("empty CSV = %q", out)
		}
	}
	cases := []struct {
		data string
		want int
	}{
		{`[{"x":1},{"x":2}]`, 2},
		{`{"items":[1,2,3]}`, 3},
		{`{"items":"bad"}`, 0},
		{`not-json`, 0},
	}
	for _, tc := range cases {
		if got := countDataPoints(json.RawMessage(tc.data)); got != tc.want {
			t.Errorf("countDataPoints(%s) = %d, want %d", tc.data, got, tc.want)
		}
	}
}

func TestCoverageFormatValueVariants(t *testing.T) {
	cases := []struct {
		value interface{}
		want  string
	}{
		{nil, ""}, {"x", "x"}, {float64(12), "12"}, {1.234, "1.23"},
		{true, "true"}, {false, "false"},
		{map[string]interface{}{"a": float64(1)}, `{"a":1}`},
		{[]interface{}{float64(1), "x"}, `[1,"x"]`},
		{struct{ X int }{2}, "{2}"},
	}
	for _, tc := range cases {
		if got := formatValue(tc.value); got != tc.want {
			t.Errorf("formatValue(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
