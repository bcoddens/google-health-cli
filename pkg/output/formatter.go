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
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Output format names, shared across Print/PrintToFile's format switches.
const (
	formatJSON  = "json"
	formatTable = "table"
	formatCSV   = "csv"
	rowDataKey  = "data"
)

// rowArrayKeys lists, in priority order, the object keys that may hold a
// tabular row array in a Health API response (raw or simplified).
var rowArrayKeys = []string{"dataPoints", "rollupDataPoints", rowDataKey, "items"}

// Print outputs data in the specified format to stdout.
func Print(format string, data json.RawMessage) error {
	switch strings.ToLower(format) {
	case formatJSON:
		return PrintJSON(data)
	case formatTable:
		return PrintTable(data)
	case formatCSV:
		return PrintCSV(data)
	default:
		return PrintJSON(data)
	}
}

// PrintToFile writes the formatted data to a file and prints a summary to stdout.
// The summary includes row count, columns (for CSV), and a preview of the first few rows.
func PrintToFile(format string, data json.RawMessage, filePath string) error {
	switch strings.ToLower(format) {
	case formatCSV:
		return printToFileCSV(data, filePath)
	case formatTable:
		return printToFileTable(data, filePath)
	default: // json
		var buf bytes.Buffer
		if err := writeIndentedJSON(&buf, data); err != nil {
			return err
		}
		return writeFileWithSummary(filePath, buf.Bytes(), format, countDataPoints(data), nil)
	}
}

// printToFileCSV implements PrintToFile's "csv" branch: an empty tabular
// result writes an empty CSV file (never a JSON dump); a non-tabular
// response falls back to a JSON file so it stays readable. A non-empty
// result is written in full, then a <=3-row preview is printed to stdout
// alongside a row-count/columns summary.
func printToFileCSV(data json.RawMessage, filePath string) error {
	rows := flattenRows(extractRows(data))
	if len(rows) == 0 {
		if isListShaped(data) {
			fwarnDroppedSignals(os.Stderr, data)
			return writeFileWithSummary(filePath, nil, formatCSV, 0, nil)
		}
		return writeFileWithSummary(filePath, data, formatCSV, 0, nil)
	}

	fwarnDroppedSignals(os.Stderr, data)
	var buf bytes.Buffer
	if err := printRowsAsCSV(&buf, rows); err != nil {
		return err
	}
	if err := os.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filePath, err)
	}

	keys := sortedKeys(rows)
	fmt.Fprintf(os.Stdout, "Wrote %d rows to %s\n\nColumns: %s\nPreview:\n", len(rows), filePath, strings.Join(keys, ", "))
	previewRows := rows
	if len(previewRows) > 3 {
		previewRows = previewRows[:3]
	}
	preview := &bytes.Buffer{}
	if err := printRowsAsCSV(preview, previewRows); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, preview.String())
	return nil
}

// printToFileTable implements PrintToFile's "table" branch: rows render as
// an aligned table (surfacing _hints/nextPageToken on stderr, matching the
// CSV/stdout table contract); a non-tabular response falls back to JSON.
func printToFileTable(data json.RawMessage, filePath string) error {
	var buf bytes.Buffer
	rows := extractRows(data)
	if len(rows) > 0 {
		fwarnDroppedSignals(os.Stderr, data)
		if err := printRowsAsTable(&buf, rows); err != nil {
			return err
		}
	} else if err := writeIndentedJSON(&buf, data); err != nil {
		return err
	}
	return writeFileWithSummary(filePath, buf.Bytes(), formatTable, countDataPoints(data), nil)
}

// writeIndentedJSON pretty-prints data into buf, failing on invalid JSON
// instead of silently writing "null". It indents the original bytes rather
// than decoding into interface{} and re-encoding, which would round integers
// above 2^53 through float64, rewrite 1.10 as 1.1, and reorder object keys.
func writeIndentedJSON(buf *bytes.Buffer, data json.RawMessage) error {
	if err := json.Indent(buf, data, "", "  "); err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}
	buf.WriteByte('\n')
	return nil
}

func writeFileWithSummary(filePath string, content []byte, format string, count int, keys []string) error {
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filePath, err)
	}
	if count > 0 {
		fmt.Fprintf(os.Stdout, "Wrote %d data points to %s\n", count, filePath)
	} else {
		fmt.Fprintf(os.Stdout, "Wrote %s\n", filePath)
	}
	return nil
}

func extractRows(data json.RawMessage) []map[string]interface{} {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		var rows []map[string]interface{}
		_ = json.Unmarshal(data, &rows) // best effort: non-JSON input yields no rows
		return rows
	}
	for _, key := range rowArrayKeys {
		if raw, ok := obj[key]; ok {
			var rows []map[string]interface{}
			if err := json.Unmarshal(raw, &rows); err == nil && len(rows) > 0 {
				return rows
			}
		}
	}
	var rows []map[string]interface{}
	_ = json.Unmarshal(data, &rows) // best effort: non-JSON input yields no rows
	return rows
}

func sortedKeys(rows []map[string]interface{}) []string {
	keySet := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			keySet[k] = true
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func countDataPoints(data json.RawMessage) int {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		var arr []json.RawMessage
		if json.Unmarshal(data, &arr) == nil {
			return len(arr)
		}
		return 0
	}
	for _, key := range rowArrayKeys {
		if raw, ok := obj[key]; ok {
			var arr []json.RawMessage
			if json.Unmarshal(raw, &arr) == nil {
				return len(arr)
			}
		}
	}
	return 0
}

// PrintJSON outputs pretty-printed JSON.
func PrintJSON(data json.RawMessage) error {
	var buf bytes.Buffer
	if err := writeIndentedJSON(&buf, data); err != nil {
		// If it's not valid JSON, print raw.
		fmt.Println(string(data))
		return nil
	}
	_, err := os.Stdout.Write(buf.Bytes())
	return err
}

// PrintTable outputs data as an aligned table. extractRows handles both
// object responses (dataPoints/rollupDataPoints/...) and the bare arrays
// that simplified rollup output produces; anything without rows falls back
// to JSON.
func PrintTable(data json.RawMessage) error {
	rows := extractRows(data)
	if len(rows) == 0 {
		// Non-tabular or empty: table mode is for humans, so the JSON fallback
		// stays readable and keeps _hints visible.
		return PrintJSON(data)
	}
	fwarnDroppedSignals(os.Stderr, data)
	return printRowsAsTable(os.Stdout, rows)
}

func printRowsAsTable(w io.Writer, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}

	keys := sortedKeys(rows)
	widths := tableColumnWidths(keys, rows)

	writeTableRow(w, keys, widths, func(k string) string { return strings.ToUpper(k) })
	for _, row := range rows {
		writeTableRow(w, keys, widths, func(k string) string { return formatValue(row[k]) })
	}

	return nil
}

// tableColumnWidths computes the print width of each column: at least the
// uppercased header length, widened to fit the longest formatted value.
func tableColumnWidths(keys []string, rows []map[string]interface{}) map[string]int {
	widths := make(map[string]int)
	for _, k := range keys {
		widths[k] = len(strings.ToUpper(k))
	}
	for _, row := range rows {
		for _, k := range keys {
			if val := formatValue(row[k]); len(val) > widths[k] {
				widths[k] = len(val)
			}
		}
	}
	return widths
}

// writeTableRow prints one space-padded row (header or data), using cell
// to render each column's value for the given key.
func writeTableRow(w io.Writer, keys []string, widths map[string]int, cell func(string) string) {
	for i, k := range keys {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		fmt.Fprintf(w, "%-*s", widths[k], cell(k))
	}
	fmt.Fprintln(w)
}

func formatValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case map[string]interface{}:
		b, _ := json.Marshal(val)
		return string(b)
	case []interface{}:
		b, _ := json.Marshal(val)
		return string(b)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// PrintCSV outputs data as CSV, keeping the stream pure for dataframe readers:
// an empty list-shaped result emits nothing (never a JSON object dumped into the
// CSV stream), and a non-tabular response (e.g. auth status) falls back to JSON.
// _hints and a leftover nextPageToken are surfaced on stderr, not in the data.
func PrintCSV(data json.RawMessage) error {
	rows := extractRows(data)
	if len(rows) == 0 {
		if isListShaped(data) {
			fwarnDroppedSignals(os.Stderr, data)
			return nil // empty tabular result → empty CSV, not a JSON dump
		}
		return PrintJSON(data) // non-tabular: nothing to tabulate
	}
	fwarnDroppedSignals(os.Stderr, data)
	return printRowsAsCSV(os.Stdout, flattenRows(rows))
}

// isListShaped reports whether data is a tabular response — a top-level array,
// or an object carrying a row-array key — even when that array is empty. It
// distinguishes an empty result (emit an empty CSV) from a non-tabular object
// such as auth status (fall back to JSON).
func isListShaped(data json.RawMessage) bool {
	var arr []json.RawMessage
	if json.Unmarshal(data, &arr) == nil {
		return true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return false
	}
	for _, key := range rowArrayKeys {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

// fwarnDroppedSignals surfaces _hints and a leftover nextPageToken on w when
// tabular output renders only the rows. Without this, CSV/table mode silently
// hides the truncation signal that JSON mode carries in-band — an agent reading
// the CSV stream would misreport a --limit-capped result as complete.
func fwarnDroppedSignals(w io.Writer, data json.RawMessage) {
	var obj struct {
		Hints         []string `json:"_hints"`
		NextPageToken string   `json:"nextPageToken"`
	}
	if json.Unmarshal(data, &obj) != nil {
		return
	}
	for _, h := range obj.Hints {
		fmt.Fprintf(w, "hint: %s\n", h)
	}
	if obj.NextPageToken != "" {
		fmt.Fprintf(w, "nextPageToken: %s\n", obj.NextPageToken)
	}
}

// flattenRows expands nested objects into dot-separated column names
// (e.g., metricsSummary.caloriesKcal) and removes internal fields like _hints.
func flattenRows(rows []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		flat := make(map[string]interface{})
		flattenMap("", row, flat)
		result = append(result, flat)
	}
	return result
}

func flattenMap(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		if strings.HasPrefix(k, "_") {
			continue // skip _hints and other internal fields
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			flattenMap(key, val, out)
		case []interface{}:
			// Keep arrays as JSON strings — can't flatten variable-length arrays into columns.
			b, _ := json.Marshal(val)
			out[key] = string(b)
		default:
			out[key] = v
		}
	}
}

func printRowsAsCSV(w io.Writer, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}

	keys := sortedKeys(rows)

	cw := csv.NewWriter(w)
	defer cw.Flush()

	// Header.
	if err := cw.Write(keys); err != nil {
		return err
	}

	// Rows.
	for _, row := range rows {
		record := make([]string, len(keys))
		for i, k := range keys {
			record[i] = formatValuePrecise(row[k])
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}

	return nil
}

// formatValuePrecise is formatValue without the 2-decimal rounding: CSV is a
// data-export format, so float values must round-trip at full precision.
func formatValuePrecise(v interface{}) string {
	if f, ok := v.(float64); ok && f != float64(int64(f)) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return formatValue(v)
}
