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
	"reflect"
	"testing"
)

// charMustParseObj unmarshals a JSON object literal into a map for feeding
// directly into unexported simplify* helpers under test.
func charMustParseObj(t *testing.T, js string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("bad test JSON: %v", err)
	}
	return m
}

// Full sleep point: every optional section present, includeStages=true.
// Pins the exact shape simplifySleepPoint currently produces.
func TestChar_SimplifySleepPoint_FullDetail(t *testing.T) {
	dp := charMustParseObj(t, `{
		"name": "users/me/dataSources/x/dataPoints/sleep-1",
		"dataSource": {"device": {"displayName": "MyWatch"}},
		"sleep": {
			"interval": {
				"startTime": "2026-06-11T00:00:00Z", "startUtcOffset": "3600s",
				"endTime": "2026-06-11T08:00:00Z", "endUtcOffset": "3600s"
			},
			"type": "SLEEP",
			"metadata": {"nap": false},
			"summary": {
				"minutesAsleep": 400,
				"minutesAwake": 20,
				"minutesInSleepPeriod": 480,
				"minutesToFallAsleep": 10,
				"stagesSummary": [
					{"type": "DEEP", "minutes": 90},
					{"type": "LIGHT", "minutes": 250}
				]
			},
			"stages": [
				{"type": "DEEP", "startTime": "2026-06-11T00:10:00Z", "startUtcOffset": "3600s", "endTime": "2026-06-11T01:00:00Z", "endUtcOffset": "3600s"}
			]
		}
	}`)

	got := simplifySleepPoint(dp, true)

	want := map[string]interface{}{
		"start":               "2026-06-11T01:00:00+01:00",
		"end":                 "2026-06-11T09:00:00+01:00",
		"sleepType":           "SLEEP",
		"isNap":               false,
		"minutesAsleep":       400,
		"minutesAwake":        20,
		"totalMinutes":        480,
		"minutesToFallAsleep": 10,
		"stageMinutes":        map[string]int{"DEEP": 90, "LIGHT": 250},
		"stages": []map[string]interface{}{
			{"type": "DEEP", "start": "2026-06-11T01:10:00+01:00", "end": "2026-06-11T02:00:00+01:00"},
		},
		"source": "MyWatch",
		"id":     "sleep-1",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("simplifySleepPoint mismatch:\ngot:  %#v\nwant: %#v", got, want)
	}
}

// includeStages=false must omit the "stages" key even though the raw data
// carries per-stage detail.
func TestChar_SimplifySleepPoint_NoDetailOmitsStages(t *testing.T) {
	dp := charMustParseObj(t, `{
		"sleep": {
			"interval": {"startTime": "2026-06-11T00:00:00Z", "endTime": "2026-06-11T08:00:00Z"},
			"summary": {"minutesAsleep": 400},
			"stages": [{"type": "DEEP", "startTime": "2026-06-11T00:10:00Z", "endTime": "2026-06-11T01:00:00Z"}]
		}
	}`)

	got := simplifySleepPoint(dp, false)
	if _, ok := got["stages"]; ok {
		t.Errorf("stages must be omitted when includeStages=false, got %v", got["stages"])
	}
	if got["minutesAsleep"] != 400 {
		t.Errorf("minutesAsleep = %v, want 400", got["minutesAsleep"])
	}
}

// includeStages=true but the raw "stages" field is absent: no "stages" key
// is added (not even an empty slice).
func TestChar_SimplifySleepPoint_DetailRequestedButNoStagesField(t *testing.T) {
	dp := charMustParseObj(t, `{"sleep": {"summary": {"minutesAsleep": 10}}}`)

	got := simplifySleepPoint(dp, true)
	if _, ok := got["stages"]; ok {
		t.Errorf("stages must be absent when raw stages field is missing, got %v", got["stages"])
	}
}

// A dp without a "sleep" object is returned unmodified (identity fallback).
func TestChar_SimplifySleepPoint_MissingSleepObjectReturnsInput(t *testing.T) {
	dp := charMustParseObj(t, `{"name": "x", "somethingElse": 1}`)

	got := simplifySleepPoint(dp, true)
	if !reflect.DeepEqual(got, dp) {
		t.Errorf("expected identity fallback, got %#v want %#v", got, dp)
	}
}

// Minimal sleep object: no interval, type, metadata, or summary. Only
// source and (if name present) id are added.
func TestChar_SimplifySleepPoint_MinimalSleepObject(t *testing.T) {
	dp := charMustParseObj(t, `{"sleep": {}}`)

	got := simplifySleepPoint(dp, true)
	want := map[string]interface{}{"source": "unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// A dp without "name" produces no "id" field.
func TestChar_SimplifySleepPoint_NoNameOmitsID(t *testing.T) {
	dp := charMustParseObj(t, `{"sleep": {"type": "SLEEP"}}`)

	got := simplifySleepPoint(dp, false)
	if _, ok := got["id"]; ok {
		t.Errorf("id must be absent without a name field, got %v", got["id"])
	}
	if got["sleepType"] != "SLEEP" {
		t.Errorf("sleepType = %v, want SLEEP", got["sleepType"])
	}
}

// An empty stagesSummary array still produces an (empty) stageMinutes map,
// distinguishing "summary present but no stages" from "no summary at all".
func TestChar_SimplifySleepPoint_EmptyStagesSummaryYieldsEmptyMap(t *testing.T) {
	dp := charMustParseObj(t, `{"sleep": {"summary": {"stagesSummary": []}}}`)

	got := simplifySleepPoint(dp, false)
	sm, ok := got["stageMinutes"].(map[string]int)
	if !ok {
		t.Fatalf("stageMinutes missing or wrong type: %#v", got["stageMinutes"])
	}
	if len(sm) != 0 {
		t.Errorf("expected empty stageMinutes map, got %v", sm)
	}
}

// stagesSummary entries with an empty/missing "type" are skipped, not
// recorded under a blank key.
func TestChar_SimplifySleepPoint_StageSummaryEntryWithoutTypeSkipped(t *testing.T) {
	dp := charMustParseObj(t, `{"sleep": {"summary": {"stagesSummary": [{"minutes": 5}, {"type": "REM", "minutes": 60}]}}}`)

	got := simplifySleepPoint(dp, false)
	sm := got["stageMinutes"].(map[string]int)
	want := map[string]int{"REM": 60}
	if !reflect.DeepEqual(sm, want) {
		t.Errorf("stageMinutes = %v, want %v", sm, want)
	}
}

// SimplifyResponse dataPoints branch, end to end via SimplifyResponse
// (not just SimplifySleepResponse), with a nextPageToken present.
func TestChar_SimplifyResponse_DataPointsKeepsNextPageToken(t *testing.T) {
	raw := json.RawMessage(`{
		"dataPoints": [
			{"name": "x/1", "steps": {"count": 10, "startTime": {"year":2026,"month":1,"day":1}}}
		],
		"nextPageToken": "tok-xyz"
	}`)

	out := SimplifyResponse(raw, "steps", false)
	var obj struct {
		DataPoints    []map[string]interface{} `json:"dataPoints"`
		NextPageToken string                   `json:"nextPageToken"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("simplified output should be an object: %v\n%s", err, out)
	}
	if obj.NextPageToken != "tok-xyz" {
		t.Errorf("nextPageToken = %q, want tok-xyz", obj.NextPageToken)
	}
	if len(obj.DataPoints) != 1 {
		t.Errorf("got %d points, want 1", len(obj.DataPoints))
	}
}

// Without a token, the dataPoints branch omits nextPageToken entirely
// (not an empty string field).
func TestChar_SimplifyResponse_DataPointsNoTokenOmitsField(t *testing.T) {
	raw := json.RawMessage(`{"dataPoints": [{"name": "x/1", "weight": {"value": 70}}]}`)

	out := SimplifyResponse(raw, "weight", false)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["nextPageToken"]; ok {
		t.Errorf("nextPageToken must be omitted when absent from input, got %s", out)
	}
}

// raw=true bypasses simplification entirely and returns the original bytes.
func TestChar_SimplifyResponse_RawBypassesSimplification(t *testing.T) {
	raw := json.RawMessage(`{"dataPoints": [{"weird": true}]}`)
	out := SimplifyResponse(raw, "steps", true)
	if string(out) != string(raw) {
		t.Errorf("raw=true must return input unchanged: got %s want %s", out, raw)
	}
}

// An unrecognized top-level structure (no dataPoints/rollupDataPoints) is
// returned unchanged.
func TestChar_SimplifyResponse_UnrecognizedStructureReturnsInput(t *testing.T) {
	raw := json.RawMessage(`{"status": "ok"}`)
	out := SimplifyResponse(raw, "steps", false)
	if string(out) != string(raw) {
		t.Errorf("unrecognized structure must pass through unchanged: got %s want %s", out, raw)
	}
}
