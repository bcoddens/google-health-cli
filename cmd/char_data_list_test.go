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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"ghealth/pkg/client"
)

// dataCharListPoint builds a minimal but realistic "steps" data point.
func dataCharListPoint(i int) string {
	return fmt.Sprintf(
		`{"steps":{"interval":{"startTime":"2026-03-01T%02d:00:00Z","endTime":"2026-03-01T%02d:00:00Z"},"countValue":{"value":"%d"}}}`,
		i, i+1, i)
}

func dataCharListPage(n, startAt int) string {
	pts := make([]string, n)
	for i := range n {
		pts[i] = dataCharListPoint(startAt + i)
	}
	return "[" + strings.Join(pts, ",") + "]"
}

// dataCharPagedListServer serves dataPoints in fixed chunks keyed by the
// incoming pageToken, regardless of the requested pageSize (fetchPage's own
// pageSize computation is characterized separately). Reports the pageSize
// query value seen on each request via sawPageSize.
func dataCharPagedListServer(t *testing.T, chunks map[string]struct {
	n    int
	next string
}, sawPageSize *[]string) http.HandlerFunc {
	t.Helper()
	offsets := map[string]int{"": 0}
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("pageToken")
		if sawPageSize != nil {
			*sawPageSize = append(*sawPageSize, r.URL.Query().Get("pageSize"))
		}
		c, ok := chunks[tok]
		if !ok {
			t.Fatalf("unexpected pageToken %q", tok)
		}
		start := offsets[tok]
		body := map[string]interface{}{
			"dataPoints": json.RawMessage(dataCharListPage(c.n, start)),
		}
		if c.next != "" {
			body["nextPageToken"] = c.next
			offsets[c.next] = start + c.n
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}

func dataCharListRequest() *client.Request {
	return &client.Request{
		Method: "GET",
		Path:   "/users/me/dataTypes/steps/dataPoints",
		Query:  url.Values{},
	}
}

// executeDataList exhausts pagination before the limit is reached: all
// points across every page are returned and no nextPageToken survives.
func TestDataCharExecuteDataList_ExhaustsBeforeLimit(t *testing.T) {
	dataCharSetRaw(t, true)
	chunks := map[string]struct {
		n    int
		next string
	}{
		"":     {3, "tok2"},
		"tok2": {3, "tok3"},
		"tok3": {2, ""},
	}
	dataCharWithServer(t, dataCharPagedListServer(t, chunks, nil))

	req := dataCharListRequest()
	opts := dataListOpts{dataType: "steps", operation: "list", limit: 1000}
	out, err := dataCharCaptureStdout(t, func() error {
		return executeDataList(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataList error: %v", err)
	}
	var got map[string]json.RawMessage
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	var points []json.RawMessage
	if uerr := json.Unmarshal(got["dataPoints"], &points); uerr != nil {
		t.Fatalf("dataPoints not an array: %v", uerr)
	}
	if len(points) != 8 {
		t.Errorf("dataPoints length = %d, want 8", len(points))
	}
	if _, ok := got["nextPageToken"]; ok {
		t.Errorf("nextPageToken present in exhausted result: %s", out)
	}
}

// --limit truncates a merged result to exactly the limit, and (in non-raw
// mode) injects a hint carrying the surfaced nextPageToken.
func TestDataCharExecuteDataList_LimitTruncatesAndHints(t *testing.T) {
	dataCharSetRaw(t, false)
	chunks := map[string]struct {
		n    int
		next string
	}{
		"":     {3, "tok2"},
		"tok2": {3, "tok3"},
		"tok3": {2, ""},
	}
	dataCharWithServer(t, dataCharPagedListServer(t, chunks, nil))

	req := dataCharListRequest()
	opts := dataListOpts{dataType: "steps", operation: "list", limit: 5}
	out, err := dataCharCaptureStdout(t, func() error {
		return executeDataList(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataList error: %v", err)
	}
	var got map[string]json.RawMessage
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	var points []json.RawMessage
	if uerr := json.Unmarshal(got["dataPoints"], &points); uerr != nil {
		t.Fatalf("dataPoints not an array: %v", uerr)
	}
	if len(points) != 5 {
		t.Fatalf("dataPoints length = %d, want 5 (capped to --limit)", len(points))
	}
	var token string
	if uerr := json.Unmarshal(got["nextPageToken"], &token); uerr != nil || token != "tok3" {
		t.Errorf("nextPageToken = %q (err=%v), want %q", token, uerr, "tok3")
	}
	var hints []string
	if uerr := json.Unmarshal(got["_hints"], &hints); uerr != nil {
		t.Fatalf("_hints missing/invalid: %v\n%s", uerr, out)
	}
	want := "returned 5 rows = --limit; more data exists — fetch the next page with --page-token tok3, " +
		"or raise --limit / narrow --from/--to"
	found := false
	for _, h := range hints {
		if h == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("_hints = %v, missing truncation hint %q", hints, want)
	}
}

// The per-request pageSize is capped at the data type's MaxPageSize even
// when --limit asks for far more (sleep is capped at 25).
func TestDataCharExecuteDataList_PageSizeCappedByType(t *testing.T) {
	dataCharSetRaw(t, true)
	var sawPageSize []string
	chunks := map[string]struct {
		n    int
		next string
	}{"": {1, ""}}
	dataCharWithServer(t, dataCharPagedListServer(t, chunks, &sawPageSize))

	req := &client.Request{Method: "GET", Path: "/users/me/dataTypes/sleep/dataPoints", Query: url.Values{}}
	opts := dataListOpts{dataType: "sleep", operation: "list", limit: 1000}
	_, err := dataCharCaptureStdout(t, func() error {
		return executeDataList(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataList error: %v", err)
	}
	if len(sawPageSize) != 1 || sawPageSize[0] != "25" {
		t.Errorf("requested pageSize = %v, want [\"25\"] (sleep's MaxPageSize cap)", sawPageSize)
	}
}

// An empty result set in --raw mode currently serializes dataPoints as JSON
// null (the underlying []json.RawMessage stays nil through collectPages),
// not an empty array — pinning that quirk, not endorsing it.
func TestDataCharExecuteDataList_EmptyResult(t *testing.T) {
	dataCharSetRaw(t, true)
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dataPoints":[]}`))
	})

	req := dataCharListRequest()
	opts := dataListOpts{dataType: "steps", operation: "list", limit: 500}
	out, err := dataCharCaptureStdout(t, func() error {
		return executeDataList(req, opts)
	})
	if err != nil {
		t.Fatalf("executeDataList error: %v", err)
	}
	var got map[string]json.RawMessage
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	dp, ok := got["dataPoints"]
	if !ok {
		t.Fatalf("dataPoints key missing: %s", out)
	}
	if strings.TrimSpace(string(dp)) != "null" {
		t.Errorf("dataPoints = %s, want null (raw-mode empty-result quirk)", dp)
	}
	if _, ok := got["nextPageToken"]; ok {
		t.Errorf("nextPageToken present for empty/exhausted result: %s", out)
	}
}

// A 4xx API error is surfaced as a structured CLIError, not swallowed.
func TestDataCharExecuteDataList_APIErrorPassthrough(t *testing.T) {
	dataCharSetRaw(t, true)
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"boom","status":"INVALID_ARGUMENT"}}`))
	})

	req := dataCharListRequest()
	opts := dataListOpts{dataType: "steps", operation: "list", limit: 500}
	_, err := dataCharCaptureStdout(t, func() error {
		return executeDataList(req, opts)
	})
	if err == nil {
		t.Fatal("executeDataList error = nil, want API error")
	}
	cliErr, ok := client.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a *CLIError: %v (%T)", err, err)
	}
	if cliErr.Message != "boom" {
		t.Errorf("cliErr.Message = %q, want %q", cliErr.Message, "boom")
	}
	if cliErr.Status != 400 {
		t.Errorf("cliErr.Status = %d, want 400", cliErr.Status)
	}
	if cliErr.Code != client.ExitAPIError {
		t.Errorf("cliErr.Code = %d, want %d", cliErr.Code, client.ExitAPIError)
	}
}
