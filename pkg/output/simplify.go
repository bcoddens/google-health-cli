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
	"strconv"
	"strings"
	"time"
)

// JSON field names duplicated across multiple simplify* helpers.
const (
	fieldDate             = "date"
	fieldStart            = "start"
	fieldEnd              = "end"
	fieldStartDate        = "startDate"
	fieldEndDate          = "endDate"
	fieldDataPoints       = "dataPoints"
	fieldRollupDataPoints = "rollupDataPoints"
	fieldNextPageToken    = "nextPageToken"
	fieldType             = "type"
	fieldName             = "name"
	fieldInterval         = "interval"
	fieldStartTime        = "startTime"
	fieldEndTime          = "endTime"
	fieldStartUtcOffset   = "startUtcOffset"
	fieldEndUtcOffset     = "endUtcOffset"

	jsonIndent = "  "
)

// SimplifyResponse transforms a raw Health API response into a compact,
// agent-friendly format. Returns the original data unchanged if --raw is set.
func SimplifyResponse(data json.RawMessage, dataType string, raw bool) json.RawMessage {
	if raw {
		return data
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}

	// Handle list responses (dataPoints array).
	if dpRaw, ok := obj[fieldDataPoints]; ok {
		return simplifyDataPointsResponse(obj, dpRaw, dataType, data)
	}

	// Handle rollup responses (rollupDataPoints array).
	if rpRaw, ok := obj[fieldRollupDataPoints]; ok {
		return simplifyRollupResponse(obj, rpRaw, data)
	}

	// Not a recognized structure, return as-is.
	return data
}

// extractNextPageToken returns the nextPageToken carried by obj, or "" if
// it is absent, unparsable, or empty. Shared by every response branch that
// must not silently drop a pagination continuation token.
func extractNextPageToken(obj map[string]json.RawMessage) string {
	tok, ok := obj[fieldNextPageToken]
	if !ok {
		return ""
	}
	var t string
	_ = json.Unmarshal(tok, &t) // best effort: unparsable token counts as none
	return t
}

// simplifyDataPointsResponse simplifies a {"dataPoints": [...]} response,
// preserving a leftover nextPageToken. fallback is returned verbatim if the
// dataPoints array cannot be parsed.
func simplifyDataPointsResponse(obj map[string]json.RawMessage, dpRaw json.RawMessage, dataType string, fallback json.RawMessage) json.RawMessage {
	var dataPoints []map[string]interface{}
	if err := json.Unmarshal(dpRaw, &dataPoints); err != nil {
		return fallback
	}

	simplified := make([]map[string]interface{}, 0, len(dataPoints))
	for _, dp := range dataPoints {
		simplified = append(simplified, simplifyDataPoint(dp, dataType))
	}

	result := map[string]interface{}{fieldDataPoints: simplified}
	if t := extractNextPageToken(obj); t != "" {
		result[fieldNextPageToken] = t
	}

	out, _ := json.MarshalIndent(result, "", jsonIndent)
	return out
}

// simplifyRollupResponse simplifies a {"rollupDataPoints": [...]} response.
// rollUp paginates server-side (POST-with-body pageToken); a remaining
// continuation token must survive simplification or the result looks
// complete when it is not. fallback is returned verbatim if the
// rollupDataPoints array cannot be parsed.
func simplifyRollupResponse(obj map[string]json.RawMessage, rpRaw json.RawMessage, fallback json.RawMessage) json.RawMessage {
	var rollupPoints []map[string]interface{}
	if err := json.Unmarshal(rpRaw, &rollupPoints); err != nil {
		return fallback
	}

	simplified := make([]map[string]interface{}, 0, len(rollupPoints))
	for _, rp := range rollupPoints {
		s := simplifyRollupPoint(rp)
		// Skip rollup windows that carry only time fields and no metric
		// value. dailyRollUp points have "date" (or "startDate"/"endDate"
		// for multi-day windows); rollUp points have "start"/"end" —
		// count any other key as a real metric.
		if !rollupPointHasMetric(s) {
			continue
		}
		simplified = append(simplified, s)
	}

	if t := extractNextPageToken(obj); t != "" {
		result := map[string]interface{}{
			fieldRollupDataPoints: simplified,
			fieldNextPageToken:    t,
		}
		out, _ := json.MarshalIndent(result, "", jsonIndent)
		return out
	}

	out, _ := json.MarshalIndent(simplified, "", jsonIndent)
	return out
}

// rollupPointHasMetric reports whether s carries any key besides the
// time-window fields, i.e. whether it has a real metric value.
func rollupPointHasMetric(s map[string]interface{}) bool {
	for k := range s {
		switch k {
		case fieldDate, fieldStart, fieldEnd, fieldStartDate, fieldEndDate:
		default:
			return true
		}
	}
	return false
}

// SimplifySleepResponse handles sleep-specific simplification with optional stage detail.
func SimplifySleepResponse(data json.RawMessage, includeStages bool, raw bool) json.RawMessage {
	if raw {
		return data
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}

	dpRaw, ok := obj[fieldDataPoints]
	if !ok {
		return data
	}

	var dataPoints []map[string]interface{}
	if err := json.Unmarshal(dpRaw, &dataPoints); err != nil {
		return data
	}

	simplified := make([]map[string]interface{}, 0, len(dataPoints))
	for _, dp := range dataPoints {
		simplified = append(simplified, simplifySleepPoint(dp, includeStages))
	}

	result := map[string]interface{}{fieldDataPoints: simplified}
	if t := extractNextPageToken(obj); t != "" {
		result[fieldNextPageToken] = t
	}

	out, _ := json.MarshalIndent(result, "", jsonIndent)
	return out
}

func simplifyDataPoint(dp map[string]interface{}, dataType string) map[string]interface{} {
	result := make(map[string]interface{})

	// Find the type-specific data object (e.g., "heartRate", "weight", "steps").
	typeKey := findTypeKey(dp)
	if typeKey == "" {
		return dp
	}

	typeData, ok := dp[typeKey].(map[string]interface{})
	if !ok {
		return dp
	}

	// Extract timestamps based on structure.
	if iv, ok := typeData[fieldInterval].(map[string]interface{}); ok {
		// Interval type: has startTime/endTime
		result[fieldStart] = formatTimeWithOffset(iv, fieldStartTime, fieldStartUtcOffset)
		result[fieldEnd] = formatTimeWithOffset(iv, fieldEndTime, fieldEndUtcOffset)
	} else if st, ok := typeData["sampleTime"].(map[string]interface{}); ok {
		// Sample type: has physicalTime
		result["time"] = formatTimeWithOffset(st, "physicalTime", "utcOffset")
	} else if d, ok := typeData[fieldDate].(map[string]interface{}); ok {
		// Daily type: has date object {year, month, day} → flatten to "YYYY-MM-DD"
		result[fieldDate] = formatCivilDate(d)
	}

	// Extract all value fields (skip time-related ones).
	for k, v := range typeData {
		switch k {
		case fieldInterval, "sampleTime", fieldDate, "createTime", "updateTime":
			continue
		default:
			result[k] = v
		}
	}

	// Compact source.
	result["source"] = extractSource(dp)

	// Include ID if present (needed for update/delete).
	if name, ok := dp[fieldName].(string); ok {
		parts := strings.Split(name, "/")
		result["id"] = parts[len(parts)-1]
	}

	return result
}

// formatCivilDate converts {year, month, day} to "YYYY-MM-DD".
func formatCivilDate(d map[string]interface{}) string {
	return fmt.Sprintf("%d-%02d-%02d", toInt(d["year"]), toInt(d["month"]), toInt(d["day"]))
}

// civilDateTime converts {year, month, day} to a time.Time (midnight UTC,
// for date arithmetic only).
func civilDateTime(d map[string]interface{}) time.Time {
	return time.Date(toInt(d["year"]), time.Month(toInt(d["month"])), toInt(d["day"]), 0, 0, 0, 0, time.UTC)
}

func simplifySleepPoint(dp map[string]interface{}, includeStages bool) map[string]interface{} {
	sleepData, ok := dp["sleep"].(map[string]interface{})
	if !ok {
		return dp
	}

	result := make(map[string]interface{})
	applySleepTimestamps(result, sleepData)

	if t, ok := sleepData[fieldType]; ok {
		result["sleepType"] = t
	}

	if meta, ok := sleepData["metadata"].(map[string]interface{}); ok {
		if nap, ok := meta["nap"]; ok {
			result["isNap"] = nap
		}
	}

	if summary, ok := sleepData["summary"].(map[string]interface{}); ok {
		applySleepSummary(result, summary)
	}

	// Detailed stages (only with --detail).
	if includeStages {
		if stages, ok := sleepData["stages"].([]interface{}); ok {
			result["stages"] = compactSleepStages(stages)
		}
	}

	result["source"] = extractSource(dp)

	if name, ok := dp[fieldName].(string); ok {
		parts := strings.Split(name, "/")
		result["id"] = parts[len(parts)-1]
	}

	return result
}

// applySleepTimestamps copies interval start/end (local time) from
// sleepData into result, if present.
func applySleepTimestamps(result, sleepData map[string]interface{}) {
	if iv, ok := sleepData[fieldInterval].(map[string]interface{}); ok {
		result[fieldStart] = formatTimeWithOffset(iv, fieldStartTime, fieldStartUtcOffset)
		result[fieldEnd] = formatTimeWithOffset(iv, fieldEndTime, fieldEndUtcOffset)
	}
}

// applySleepSummary copies the always-included summary fields (minutes
// asleep/awake/total/toFallAsleep and the per-stage minute map) into result.
func applySleepSummary(result, summary map[string]interface{}) {
	if v, ok := summary["minutesAsleep"]; ok {
		result["minutesAsleep"] = toInt(v)
	}
	if v, ok := summary["minutesAwake"]; ok {
		result["minutesAwake"] = toInt(v)
	}
	if v, ok := summary["minutesInSleepPeriod"]; ok {
		result["totalMinutes"] = toInt(v)
	}
	if v, ok := summary["minutesToFallAsleep"]; ok {
		result["minutesToFallAsleep"] = toInt(v)
	}
	if stages, ok := summary["stagesSummary"].([]interface{}); ok {
		result["stageMinutes"] = sleepStageMinutes(stages)
	}
}

// sleepStageMinutes flattens a stagesSummary array into a {stageType:
// minutes} map, skipping entries without a stage type.
func sleepStageMinutes(stages []interface{}) map[string]int {
	stageMap := make(map[string]int)
	for _, s := range stages {
		sm, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		sType, _ := sm[fieldType].(string)
		if sType == "" {
			continue
		}
		stageMap[sType] = toInt(sm["minutes"])
	}
	return stageMap
}

// compactSleepStages converts the raw per-stage detail array (only emitted
// with --detail) into {type, start, end} entries.
func compactSleepStages(stages []interface{}) []map[string]interface{} {
	compactStages := make([]map[string]interface{}, 0, len(stages))
	for _, s := range stages {
		sm, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		compactStages = append(compactStages, map[string]interface{}{
			fieldType:  sm[fieldType],
			fieldStart: formatTimeWithOffset(sm, fieldStartTime, fieldStartUtcOffset),
			fieldEnd:   formatTimeWithOffset(sm, fieldEndTime, fieldEndUtcOffset),
		})
	}
	return compactStages
}

func simplifyRollupPoint(rp map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	applyRollupWindow(result, rp)

	// Extract all value fields from the type-specific object.
	if typeKey := findTypeKey(rp); typeKey != "" {
		if typeData, ok := rp[typeKey].(map[string]interface{}); ok {
			for k, v := range typeData {
				result[k] = v
			}
		}
	}

	return result
}

// applyRollupWindow sets the date/start/end fields describing rp's time
// window. dailyRollUp points carry a civil date; rollUp points carry
// physical startTime/endTime instead.
func applyRollupWindow(result, rp map[string]interface{}) {
	cst, ok := rp["civilStartTime"].(map[string]interface{})
	if !ok {
		applyRollupPhysicalTimes(result, rp)
		return
	}
	d, ok := cst[fieldDate].(map[string]interface{})
	if !ok {
		return
	}
	start := civilDateTime(d)
	result[fieldDate] = formatCivilDate(d)
	applyRollupMultiDayDates(result, rp, d, start)
}

func applyRollupPhysicalTimes(result, rp map[string]interface{}) {
	if st, ok := rp[fieldStartTime].(string); ok && st != "" {
		result[fieldStart] = st
	}
	if et, ok := rp[fieldEndTime].(string); ok && et != "" {
		result[fieldEnd] = et
	}
}

// applyRollupMultiDayDates replaces the single "date" field with explicit
// startDate/endDate when the civil window spans more than one day.
// Multi-day windows (--window-days > 1) must not be mislabeled as a single
// day. The civil interval is closed-open, so the inclusive last day is
// civilEndTime minus one day; 1-day buckets keep the single "date" field
// for backward compatibility.
func applyRollupMultiDayDates(result, rp map[string]interface{}, d map[string]interface{}, start time.Time) {
	cet, ok := rp["civilEndTime"].(map[string]interface{})
	if !ok {
		return
	}
	ed, ok := cet[fieldDate].(map[string]interface{})
	if !ok {
		return
	}
	end := civilDateTime(ed)
	if end.Sub(start) <= 24*time.Hour {
		return
	}
	delete(result, fieldDate)
	result[fieldStartDate] = formatCivilDate(d)
	last := end.AddDate(0, 0, -1)
	result[fieldEndDate] = fmt.Sprintf("%d-%02d-%02d", last.Year(), last.Month(), last.Day())
}

// formatTimeWithOffset converts a UTC timestamp + offset into a local ISO 8601 string.
// The timestamp is a real UTC instant; the offset is added to it.
// Input: physicalTime "2026-03-29T16:05:00Z", utcOffset "3600s"
// Output: "2026-03-29T17:05:00+01:00"
func formatTimeWithOffset(obj map[string]interface{}, timeKey, offsetKey string) string {
	ts, _ := obj[timeKey].(string)
	if ts == "" {
		return ""
	}

	offsetStr, _ := obj[offsetKey].(string)
	offsetSec := parseOffsetSeconds(offsetStr)

	if offsetSec == 0 {
		return ts // Already UTC, keep Z suffix.
	}

	// The Health API serializes interval/sample times as real UTC instants
	// (the trailing "Z"). To render local wall-clock time the offset must be
	// ADDED to that instant — not merely appended to the same clock reading.
	// e.g. 08:29:48Z + 7200s → 10:29:48+02:00, not 08:29:48+02:00.
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts // Not parseable; leave untouched.
	}

	local := t.In(time.FixedZone("", offsetSec))

	// Preserve sub-second precision only when the input carried it.
	layout := "2006-01-02T15:04:05Z07:00"
	if strings.Contains(ts, ".") {
		layout = "2006-01-02T15:04:05.999999999Z07:00"
	}
	return local.Format(layout)
}

func parseOffsetSeconds(s string) int {
	s = strings.TrimSuffix(s, "s")
	n, _ := strconv.Atoi(s)
	return n
}

func findTypeKey(dp map[string]interface{}) string {
	for k := range dp {
		switch k {
		case "dataSource", fieldName, "civilStartTime", "civilEndTime":
			continue
		default:
			if _, ok := dp[k].(map[string]interface{}); ok {
				return k
			}
		}
	}
	return ""
}

func extractSource(dp map[string]interface{}) string {
	ds, ok := dp["dataSource"].(map[string]interface{})
	if !ok {
		return "unknown"
	}

	// Prefer device displayName.
	if dev, ok := ds["device"].(map[string]interface{}); ok {
		if name, ok := dev["displayName"].(string); ok && name != "" {
			return name
		}
	}

	// Fall back to app packageName.
	if app, ok := ds["application"].(map[string]interface{}); ok {
		if pkg, ok := app["packageName"].(string); ok && pkg != "" {
			return pkg
		}
	}

	// Fall back to platform.
	if p, ok := ds["platform"].(string); ok {
		return p
	}

	return "unknown"
}

func toInt(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	case int:
		return val
	}
	return 0
}
