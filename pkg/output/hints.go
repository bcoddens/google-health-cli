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
	"fmt"
	"strings"
	"time"
)

// hintContext bundles the parsed request/response state that each hint
// generator needs, so GenerateHints can dispatch through a uniform slice
// of generator functions instead of one large branching body.
type hintContext struct {
	obj          map[string]json.RawMessage
	dataPoints   []json.RawMessage
	rollupPoints []json.RawMessage
	nPoints      int
	dataType     string
	operation    string
	from         string
	to           string
	detail       bool
}

// hintGenerators produces, in order, every hint category GenerateHints can
// emit. Each function returns "" when its condition does not apply.
var hintGenerators = []func(hintContext) string{
	wrongResolutionHint,
	sleepDetailHint,
	exerciseCorrelationHint,
	sleepVitalsHint,
	emptyResultsHint,
}

// GenerateHints produces contextual hints based on the response data, command, and data type.
// Hints help agents make better use of the CLI without being opinionated about the task.
// detail reports whether the caller already requested the detailed view (sleep --detail),
// so the hint suggesting it is not emitted redundantly.
func GenerateHints(data json.RawMessage, dataType, operation string, limit int, from, to string, detail bool) []string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}

	// Count data points.
	var dataPoints []json.RawMessage
	if raw, ok := obj["dataPoints"]; ok {
		_ = json.Unmarshal(raw, &dataPoints) // best effort: malformed value counts as no points
	}
	var rollupPoints []json.RawMessage
	if raw, ok := obj["rollupDataPoints"]; ok {
		_ = json.Unmarshal(raw, &rollupPoints) // best effort: malformed value counts as no points
	}

	ctx := hintContext{
		obj:          obj,
		dataPoints:   dataPoints,
		rollupPoints: rollupPoints,
		nPoints:      len(dataPoints) + len(rollupPoints),
		dataType:     dataType,
		operation:    operation,
		from:         from,
		to:           to,
		detail:       detail,
	}

	var hints []string
	for _, gen := range hintGenerators {
		if h := gen(ctx); h != "" {
			hints = append(hints, h)
		}
	}
	return hints
}

// wrongResolutionHint (Hint 1) fires for a short daily-rollup range,
// suggesting the finer-grained list command.
func wrongResolutionHint(ctx hintContext) string {
	if ctx.operation != "daily-rollup" || ctx.nPoints == 0 || ctx.nPoints > 3 {
		return ""
	}
	switch ctx.dataType {
	case "heart-rate", "oxygen-saturation", "heart-rate-variability":
		return fmt.Sprintf(
			"For %d days of data, 'ghealth data %s list --from %s --limit 100' gives individual readings with timestamps.",
			ctx.nPoints, ctx.dataType, ctx.from)
	case "steps", "distance", "swim-lengths-data":
		return fmt.Sprintf(
			"For detailed per-interval data, 'ghealth data %s list --from %s' shows individual records.",
			ctx.dataType, ctx.from)
	}
	return ""
}

// sleepDetailHint (Hint 2) suggests --detail for a sleep list that did not
// request it.
func sleepDetailHint(ctx hintContext) string {
	if ctx.operation != "list" || len(ctx.dataPoints) == 0 {
		return ""
	}
	if ctx.dataType == "sleep" && !ctx.detail {
		return "Add --detail for per-stage sleep breakdown (AWAKE, LIGHT, DEEP, REM timestamps)."
	}
	return ""
}

// exerciseCorrelationHint (Hint 3a) suggests a correlated heart-rate query
// using the first exercise session's time window.
func exerciseCorrelationHint(ctx hintContext) string {
	if ctx.operation != "list" || ctx.dataType != "exercise" || len(ctx.dataPoints) == 0 {
		return ""
	}
	var dp map[string]interface{}
	_ = json.Unmarshal(ctx.dataPoints[0], &dp) // best effort: hint is skipped when unparsable
	start, ok := dp["start"].(string)
	if !ok {
		return ""
	}
	end, ok := dp["end"].(string)
	if !ok {
		return ""
	}
	return fmt.Sprintf(
		"For heart rate during this exercise, use: ghealth data heart-rate list --filter 'heart_rate.sample_time.physical_time >= \"%s\" AND heart_rate.sample_time.physical_time < \"%s\"'",
		toUTC(start), toUTC(end))
}

// sleepVitalsHint (Hint 3b) suggests correlated overnight-vitals queries
// using the first sleep session's start date.
func sleepVitalsHint(ctx hintContext) string {
	if ctx.operation != "list" || ctx.dataType != "sleep" || len(ctx.dataPoints) == 0 {
		return ""
	}
	var dp map[string]interface{}
	if json.Unmarshal(ctx.dataPoints[0], &dp) != nil {
		return ""
	}
	start, ok := dp["start"].(string)
	if !ok || start == "" {
		return ""
	}
	utcStart := toUTC(start)
	return fmt.Sprintf(
		"For overnight vitals: ghealth data heart-rate-variability list --from %s and ghealth data oxygen-saturation list --from %s",
		utcStart[:10], utcStart[:10])
}

// emptyResultsHint (Hint 4) distinguishes an empty page with more data
// pending from a genuinely empty range.
func emptyResultsHint(ctx hintContext) string {
	if ctx.nPoints != 0 {
		return ""
	}
	if hasNextPage(ctx.obj) {
		return "This page is empty but more data exists. The CLI auto-paginates — try increasing --limit."
	}
	if ctx.from != "" {
		return fmt.Sprintf("No %s data found for this range. Try a wider date range or check 'ghealth auth status' for scope coverage.", ctx.dataType)
	}
	return ""
}

// toUTC converts a local time string like "2026-03-29T14:18:32+01:00" to UTC "2026-03-29T13:18:32Z".
func toUTC(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return strings.TrimSuffix(s, "Z") + "Z"
	}
	return t.UTC().Format(time.RFC3339)
}

func hasNextPage(obj map[string]json.RawMessage) bool {
	if tok, ok := obj["nextPageToken"]; ok {
		var t string
		_ = json.Unmarshal(tok, &t) // best effort: unparsable token counts as none
		return t != ""
	}
	return false
}

// InjectHints adds a _hints field to a JSON response if there are hints.
func InjectHints(data json.RawMessage, hints []string) json.RawMessage {
	if len(hints) == 0 {
		return data
	}

	// Try to add _hints to an object.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err == nil {
		hintsJSON, _ := json.Marshal(hints)
		obj["_hints"] = hintsJSON
		out, _ := json.MarshalIndent(obj, "", "  ")
		return out
	}

	// For arrays (rollup), wrap in an object.
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		hintsJSON, _ := json.Marshal(hints)
		wrapper := map[string]json.RawMessage{
			"data":   data,
			"_hints": hintsJSON,
		}
		out, _ := json.MarshalIndent(wrapper, "", "  ")
		return out
	}

	return data
}
