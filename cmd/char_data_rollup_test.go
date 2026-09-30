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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"ghealth/pkg/client"
)

func dataCharRollupPoint(i int) string {
	return fmt.Sprintf(`{"start":{"year":2026,"month":3,"day":%d},"end":{"year":2026,"month":3,"day":%d},"stepsRollup":{"countSum":"%d"}}`,
		i+1, i+2, i)
}

func dataCharRollupPage(n int, next string) string {
	pts := make([]string, n)
	for i := range n {
		pts[i] = dataCharRollupPoint(i)
	}
	body := `{"rollupDataPoints":[` + strings.Join(pts, ",") + `]`
	if next != "" {
		body += fmt.Sprintf(`,"nextPageToken":%q`, next)
	}
	return body + "}"
}

// A single-page POST rollup response (no nextPageToken) passes straight
// through collectRollupPages: one HTTP request, output merges cleanly.
func TestDataCharExecuteDataRollup_SinglePagePOST(t *testing.T) {
	dataCharSetRaw(t, true)
	requests := 0
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(dataCharRollupPage(3, "")))
	})

	baseBody := []byte(`{"range":{"startTime":"2026-03-01T00:00:00Z","endTime":"2026-03-08T00:00:00Z"},"windowSize":"86400s"}`)
	req := &client.Request{Method: "POST", Path: "/users/me/dataTypes/steps/dataPoints:rollUp", Body: baseBody}
	opts := dataListOpts{dataType: "steps", operation: "rollup"}
	out, err := dataCharCaptureStdout(t, func() error {
		return executeDataRollup(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataRollup error: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (no pagination needed)", requests)
	}
	var got map[string]json.RawMessage
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	var points []json.RawMessage
	if uerr := json.Unmarshal(got["rollupDataPoints"], &points); uerr != nil || len(points) != 3 {
		t.Errorf("rollupDataPoints = %s (err=%v), want 3 points", got["rollupDataPoints"], uerr)
	}
	if _, ok := got["nextPageToken"]; ok {
		t.Errorf("nextPageToken present for a single-page result: %s", out)
	}
	// req.Body must be left exactly as the caller supplied it.
	if !bytes.Equal(req.Body, baseBody) {
		t.Errorf("req.Body mutated: got %s, want %s", req.Body, baseBody)
	}
}

// A POST rollup that paginates across multiple nextPageTokens must merge all
// pages, thread the original request body's other fields into each
// follow-up page via pageToken, and restore req.Body afterward.
func TestDataCharExecuteDataRollup_PaginatesAcrossPages(t *testing.T) {
	dataCharSetRaw(t, true)
	var seenTokens []string
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		tok, _ := body["pageToken"].(string)
		seenTokens = append(seenTokens, tok)
		w.Header().Set("Content-Type", "application/json")
		switch tok {
		case "":
			_, _ = w.Write([]byte(dataCharRollupPage(2, "tok2")))
		case "tok2":
			_, _ = w.Write([]byte(dataCharRollupPage(1, "")))
		default:
			t.Fatalf("unexpected pageToken %q", tok)
		}
	})

	baseBody := []byte(`{"range":{"startTime":"2026-03-01T00:00:00Z","endTime":"2026-03-08T00:00:00Z"},"windowSize":"86400s"}`)
	req := &client.Request{Method: "POST", Path: "/users/me/dataTypes/steps/dataPoints:rollUp", Body: append([]byte(nil), baseBody...)}
	opts := dataListOpts{dataType: "steps", operation: "rollup"}
	out, err := dataCharCaptureStdout(t, func() error {
		return executeDataRollup(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataRollup error: %v", err)
	}
	if len(seenTokens) != 2 {
		t.Fatalf("requests = %d, want 2; tokens=%v", len(seenTokens), seenTokens)
	}
	var got map[string]json.RawMessage
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	var points []json.RawMessage
	if uerr := json.Unmarshal(got["rollupDataPoints"], &points); uerr != nil || len(points) != 3 {
		t.Errorf("rollupDataPoints = %s (err=%v), want 3 merged points", got["rollupDataPoints"], uerr)
	}
	if _, ok := got["nextPageToken"]; ok {
		t.Errorf("nextPageToken present after full pagination: %s", out)
	}
	if !bytes.Equal(req.Body, baseBody) {
		t.Errorf("req.Body not restored: got %s, want %s", req.Body, baseBody)
	}
}

// reconcile issues a GET with no body: executeDataRollup must not attempt
// POST-body pagination even if the response happens to carry a
// nextPageToken — it passes the response through untouched.
func TestDataCharExecuteDataRollup_ReconcileGETNoPagination(t *testing.T) {
	dataCharSetRaw(t, true)
	requests := 0
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dataPoints":[{"steps":{"countValue":{"value":"1"}}}],"nextPageToken":"should-be-ignored"}`))
	})

	req := &client.Request{Method: "GET", Path: "/users/me/dataTypes/steps/dataPoints:reconcile"}
	opts := dataListOpts{dataType: "steps", operation: "reconcile"}
	out, err := dataCharCaptureStdout(t, func() error {
		return executeDataRollup(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataRollup error: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (GET reconcile never paginates)", requests)
	}
	var got map[string]json.RawMessage
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	var token string
	if uerr := json.Unmarshal(got["nextPageToken"], &token); uerr != nil || token != "should-be-ignored" {
		t.Errorf("nextPageToken = %q (err=%v), want the raw passthrough value", token, uerr)
	}
}

// A 4xx API error from the initial rollup request is surfaced as a
// structured CLIError.
func TestDataCharExecuteDataRollup_APIErrorPassthrough(t *testing.T) {
	dataCharSetRaw(t, true)
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad rollup","status":"INVALID_ARGUMENT"}}`))
	})

	req := &client.Request{Method: "POST", Path: "/users/me/dataTypes/steps/dataPoints:rollUp", Body: []byte(`{"range":{}}`)}
	opts := dataListOpts{dataType: "steps", operation: "rollup"}
	_, err := dataCharCaptureStdout(t, func() error {
		return executeDataRollup(req, opts)
	})
	if err == nil {
		t.Fatal("executeDataRollup error = nil, want API error")
	}
	cliErr, ok := client.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a *CLIError: %v (%T)", err, err)
	}
	if cliErr.Message != "bad rollup" {
		t.Errorf("cliErr.Message = %q, want %q", cliErr.Message, "bad rollup")
	}
}

// An error mid-pagination (second page fails) is surfaced, and req.Body is
// still restored to the caller's original value.
func TestDataCharExecuteDataRollup_PaginationErrorRestoresBody(t *testing.T) {
	dataCharSetRaw(t, true)
	calls := 0
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(dataCharRollupPage(1, "tok2")))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"page 2 failed","status":"INVALID_ARGUMENT"}}`))
	})

	baseBody := []byte(`{"range":{"startTime":"2026-03-01T00:00:00Z","endTime":"2026-03-08T00:00:00Z"},"windowSize":"86400s"}`)
	req := &client.Request{Method: "POST", Path: "/users/me/dataTypes/steps/dataPoints:rollUp", Body: append([]byte(nil), baseBody...)}
	opts := dataListOpts{dataType: "steps", operation: "rollup"}
	_, err := dataCharCaptureStdout(t, func() error {
		return executeDataRollup(req, opts)
	})
	if err == nil {
		t.Fatal("executeDataRollup error = nil, want the page-2 API error")
	}
	if !bytes.Equal(req.Body, baseBody) {
		t.Errorf("req.Body not restored after a mid-pagination error: got %s, want %s", req.Body, baseBody)
	}
}
