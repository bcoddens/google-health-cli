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

// GenerateHints produces contextual hints based on the response data, command, and data type.
// Hints help agents make better use of the CLI without being opinionated about the task.
// detail reports whether the caller already requested the detailed view (sleep --detail),
// so the hint suggesting it is not emitted redundantly.
func GenerateHints(data json.RawMessage, dataType, operation string, limit int, from, to string, detail bool) []string {
	var hints []string

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

	nPoints := len(dataPoints) + len(rollupPoints)

	// ── Hint 1: Wrong resolution ─────────────────────────────────

	if operation == "daily-rollup" && nPoints > 0 && nPoints <= 3 {
		// Short range daily rollup — suggest list for more detail.
		switch dataType {
		case "heart-rate", "oxygen-saturation", "heart-rate-variability":
			hints = append(hints, fmt.Sprintf(
				"For %d days of data, 'ghealth data %s list --from %s --limit 100' gives individual readings with timestamps.",
				nPoints, dataType, from))
		case "steps", "distance", "swim-lengths-data":
			hints = append(hints, fmt.Sprintf(
				"For detailed per-interval data, 'ghealth data %s list --from %s' shows individual records.",
				dataType, from))
		}
	}

	// ── Hint 2: Sleep detail ─────────────────────────────────────

	if operation == "list" && len(dataPoints) > 0 {
		// Sleep without --detail.
		if dataType == "sleep" && !detail {
			hints = append(hints, "Add --detail for per-stage sleep breakdown (AWAKE, LIGHT, DEEP, REM timestamps).")
		}
	}

	// ── Hint 3: Related data ─────────────────────────────────────

	if operation == "list" && dataType == "exercise" && len(dataPoints) > 0 {
		// Parse first exercise to suggest HR correlation.
		var dp map[string]interface{}
		_ = json.Unmarshal(dataPoints[0], &dp) // best effort: hint is skipped when unparsable
		if start, ok := dp["start"].(string); ok {
			if end, ok := dp["end"].(string); ok {
				hints = append(hints, fmt.Sprintf(
					"For heart rate during this exercise, use: ghealth data heart-rate list --filter 'heart_rate.sample_time.physical_time >= \"%s\" AND heart_rate.sample_time.physical_time < \"%s\"'",
					toUTC(start), toUTC(end)))
			}
		}
	}

	if operation == "list" && dataType == "sleep" && len(dataPoints) > 0 {
		// Try to extract the start time from the first sleep session for a concrete hint.
		var dp map[string]interface{}
		if json.Unmarshal(dataPoints[0], &dp) == nil {
			if start, ok := dp["start"].(string); ok && start != "" {
				utcStart := toUTC(start)
				hints = append(hints, fmt.Sprintf(
					"For overnight vitals: ghealth data heart-rate-variability list --from %s and ghealth data oxygen-saturation list --from %s",
					utcStart[:10], utcStart[:10]))
			}
		}
	}

	// ── Hint 4: Empty results ────────────────────────────────────

	if nPoints == 0 {
		if hasNextPage(obj) {
			hints = append(hints, "This page is empty but more data exists. The CLI auto-paginates — try increasing --limit.")
		} else if from != "" {
			hints = append(hints, fmt.Sprintf("No %s data found for this range. Try a wider date range or check 'ghealth auth status' for scope coverage.", dataType))
		}
	}

	return hints
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
