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
	"strings"
	"testing"
)

// charContainsHint reports whether any hint in hints contains substr.
func charContainsHint(hints []string, substr string) bool {
	for _, h := range hints {
		if strings.Contains(h, substr) {
			return true
		}
	}
	return false
}

// daily-rollup with 1-3 points and a "heart-rate"-family dataType hints at
// the --limit-100 list form.
func TestChar_GenerateHints_DailyRollupHRFamilySuggestsList(t *testing.T) {
	data := json.RawMessage(`{"rollupDataPoints": [{"date": "2026-06-01"}, {"date": "2026-06-02"}]}`)
	hints := GenerateHints(data, "heart-rate", "daily-rollup", 0, "2026-06-01", "", false)
	if !charContainsHint(hints, "--limit 100") {
		t.Errorf("expected a --limit 100 hint, got %v", hints)
	}
}

// daily-rollup with 1-3 points and a "steps"-family dataType hints at the
// plain list form (no --limit 100).
func TestChar_GenerateHints_DailyRollupStepsFamilySuggestsList(t *testing.T) {
	data := json.RawMessage(`{"rollupDataPoints": [{"date": "2026-06-01"}]}`)
	hints := GenerateHints(data, "steps", "daily-rollup", 0, "2026-06-01", "", false)
	if !charContainsHint(hints, "shows individual records") {
		t.Errorf("expected an individual-records hint, got %v", hints)
	}
	if charContainsHint(hints, "--limit 100") {
		t.Errorf("steps family must not use the HR-family --limit 100 wording, got %v", hints)
	}
}

// daily-rollup with a dataType outside both switch cases produces no
// resolution hint at all.
func TestChar_GenerateHints_DailyRollupUnknownDataTypeNoHint(t *testing.T) {
	data := json.RawMessage(`{"rollupDataPoints": [{"date": "2026-06-01"}]}`)
	hints := GenerateHints(data, "weight", "daily-rollup", 0, "2026-06-01", "", false)
	if len(hints) != 0 {
		t.Errorf("expected no hints for an unmapped dataType, got %v", hints)
	}
}

// daily-rollup with more than 3 points suppresses the resolution hint
// (already reasonably granular).
func TestChar_GenerateHints_DailyRollupTooManyPointsNoHint(t *testing.T) {
	data := json.RawMessage(`{"rollupDataPoints": [{"date":"1"},{"date":"2"},{"date":"3"},{"date":"4"}]}`)
	hints := GenerateHints(data, "heart-rate", "daily-rollup", 0, "2026-06-01", "", false)
	if charContainsHint(hints, "--limit 100") {
		t.Errorf("expected no resolution hint above 3 points, got %v", hints)
	}
}

// A sleep list response with a parsable "start" time hints at correlated
// overnight vitals commands, converted to UTC and truncated to a date.
func TestChar_GenerateHints_SleepListSuggestsOvernightVitals(t *testing.T) {
	data := json.RawMessage(`{"dataPoints": [{"start": "2026-06-11T00:42:00+01:00", "end": "2026-06-11T08:00:00+01:00"}]}`)
	hints := GenerateHints(data, "sleep", "list", 0, "", "", true)
	if !charContainsHint(hints, "heart-rate-variability list --from 2026-06-10") {
		t.Errorf("expected an overnight-vitals hint with UTC date, got %v", hints)
	}
}

// A sleep list response whose first point has no "start" field skips the
// overnight-vitals hint (but not the whole function).
func TestChar_GenerateHints_SleepListNoStartFieldSkipsVitalsHint(t *testing.T) {
	data := json.RawMessage(`{"dataPoints": [{"end": "2026-06-11T08:00:00Z"}]}`)
	hints := GenerateHints(data, "sleep", "list", 0, "", "", true)
	if charContainsHint(hints, "overnight vitals") {
		t.Errorf("expected no overnight-vitals hint without a start field, got %v", hints)
	}
}

// An exercise list response whose first point is missing "end" (only
// "start" present) skips the correlation hint.
func TestChar_GenerateHints_ExerciseListMissingEndSkipsCorrelationHint(t *testing.T) {
	data := json.RawMessage(`{"dataPoints": [{"start": "2026-06-11T00:00:00Z"}]}`)
	hints := GenerateHints(data, "exercise", "list", 0, "", "", false)
	if charContainsHint(hints, "heart-rate list --filter") {
		t.Errorf("expected no correlation hint without an end field, got %v", hints)
	}
}

// Zero points with a pending nextPageToken hints at auto-pagination rather
// than the "no data found" message.
func TestChar_GenerateHints_EmptyWithNextPageHintsPagination(t *testing.T) {
	data := json.RawMessage(`{"dataPoints": [], "nextPageToken": "tok"}`)
	hints := GenerateHints(data, "steps", "list", 0, "2026-06-01", "", false)
	if !charContainsHint(hints, "auto-paginates") {
		t.Errorf("expected an auto-pagination hint, got %v", hints)
	}
	if charContainsHint(hints, "No steps data found") {
		t.Errorf("must not also emit the no-data hint when more pages exist, got %v", hints)
	}
}

// Zero points, no next page, and a "from" date hints at widening the range.
func TestChar_GenerateHints_EmptyNoNextPageWithFromHintsWiderRange(t *testing.T) {
	data := json.RawMessage(`{"dataPoints": []}`)
	hints := GenerateHints(data, "steps", "list", 0, "2026-06-01", "", false)
	if !charContainsHint(hints, "No steps data found for this range") {
		t.Errorf("expected a no-data hint, got %v", hints)
	}
}

// Zero points, no next page, and no "from" date: no empty-results hint at
// all (nothing concrete to suggest).
func TestChar_GenerateHints_EmptyNoFromNoHint(t *testing.T) {
	data := json.RawMessage(`{"dataPoints": []}`)
	hints := GenerateHints(data, "steps", "list", 0, "", "", false)
	if len(hints) != 0 {
		t.Errorf("expected no hints with no from date and no next page, got %v", hints)
	}
}

// Malformed top-level JSON returns a nil hint slice rather than panicking.
func TestChar_GenerateHints_UnparsableDataReturnsNil(t *testing.T) {
	hints := GenerateHints(json.RawMessage(`not json`), "steps", "list", 0, "", "", false)
	if hints != nil {
		t.Errorf("expected nil hints for unparsable data, got %v", hints)
	}
}
