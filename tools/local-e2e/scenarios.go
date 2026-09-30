//go:build local_e2e

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

package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	heartRatePath   = "/users/me/dataTypes/heart-rate/dataPoints"
	stepsRollupPath = "/users/me/dataTypes/steps/dataPoints:dailyRollUp"
)

func allScenarios() []scenario {
	return []scenario{
		{name: "binary-version-and-help", run: scenarioBinaryVersionAndHelp},
		{name: "config-persistence", run: scenarioConfigPersistence},
		{name: "validation-before-network", run: scenarioValidationBeforeNetwork},
		{name: "dry-run-without-network", run: scenarioDryRun},
		{name: "list-request-and-output", run: scenarioListRequest},
		{name: "list-pagination", run: scenarioListPagination},
		{name: "daily-rollup-post", run: scenarioDailyRollup},
		{name: "csv-file-output", run: scenarioCSVFileOutput},
		{name: "api-error-exit-1", run: scenarioAPIError},
		{name: "auth-error-exit-2", run: scenarioAuthError},
		{name: "network-error-exit-4", run: scenarioNetworkError},
		{name: "config-error-exit-5", run: scenarioConfigError},
		{name: "401-refresh-and-retry", run: scenarioUnauthorizedRetry},
		{name: "503-retry-exhaustion", run: scenarioServerRetryExhaustion},
		{name: "malformed-server-response", run: scenarioMalformedResponse},
	}
}

func scenarioBinaryVersionAndHelp(h *harness) error {
	env, _, err := h.scenarioEnv("binary-version-and-help")
	if err != nil {
		return err
	}
	version := h.run(env, "--version")
	if err := requireExit(version, 0); err != nil {
		return err
	}
	if err := requireContains("stdout", version.stdout, "ghealth version", version); err != nil {
		return err
	}
	help := h.run(env, "data", "--help")
	if err := requireExit(help, 0); err != nil {
		return err
	}
	for _, command := range []string{"heart-rate", "daily-rollup", "exercise"} {
		if err := requireContains("stdout", help.stdout, command, help); err != nil {
			return err
		}
	}
	return nil
}

func scenarioConfigPersistence(h *harness) error {
	env, configDir, err := h.scenarioEnv("config-persistence")
	if err != nil {
		return err
	}
	set := h.run(env, "config", "set", "timezone", "UTC")
	if err := requireExit(set, 0); err != nil {
		return err
	}
	setJSON, err := decodeJSONObject(set.stdout)
	if err != nil {
		return set.failure(fmt.Sprintf("config set stdout is not JSON: %v", err))
	}
	if err := requireJSONField(setJSON, "status", "updated"); err != nil {
		return set.failure(err.Error())
	}

	show := h.run(env, "config", "show")
	if err := requireExit(show, 0); err != nil {
		return err
	}
	showJSON, err := decodeJSONObject(show.stdout)
	if err != nil {
		return show.failure(fmt.Sprintf("config show stdout is not JSON: %v", err))
	}
	if err := requireJSONField(showJSON, "Default.timezone", "UTC"); err != nil {
		return show.failure(err.Error())
	}
	info, err := os.Stat(filepath.Join(configDir, "config.toml"))
	if err != nil {
		return fmt.Errorf("stat persisted config: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("persisted config mode = %o, want 600", info.Mode().Perm())
	}
	return nil
}

func scenarioValidationBeforeNetwork(h *harness) error {
	env, _, err := h.scenarioEnv("validation-before-network")
	if err != nil {
		return err
	}
	api := newFakeAPI()
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "steps", "daily-rollup", "--from", "2026-09-01")
	if err := requireCLIError(result, 3, "validation"); err != nil {
		return err
	}
	return api.Verify(0)
}

func scenarioDryRun(h *harness) error {
	env, _, err := h.scenarioEnv("dry-run-without-network")
	if err != nil {
		return err
	}
	api := newFakeAPI()
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "--dry-run", "data", "heart-rate", "list", "--from", "2026-09-01", "--to", "2026-09-02", "--limit", "2")
	if err := requireExit(result, 0); err != nil {
		return err
	}
	body, err := decodeJSONObject(result.stdout)
	if err != nil {
		return result.failure(fmt.Sprintf("dry-run stdout is not JSON: %v", err))
	}
	if err := requireJSONField(body, "method", http.MethodGet); err != nil {
		return result.failure(err.Error())
	}
	if err := requireJSONField(body, "headers.Authorization", "Bearer [REDACTED]"); err != nil {
		return result.failure(err.Error())
	}
	if err := requireContains("stdout", result.stdout, heartRatePath, result); err != nil {
		return err
	}
	return api.Verify(0)
}

func scenarioListRequest(h *harness) error {
	env, _, err := h.scenarioEnv("list-request-and-output")
	if err != nil {
		return err
	}
	response := jsonResponse(http.StatusOK, heartRateResponse("one", 72, ""))
	response.check = checkListRequest
	api := newFakeAPI(response)
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--to", "2026-09-02", "--limit", "2")
	if err := checkListResult(result); err != nil {
		return err
	}
	return api.Verify(1)
}

func checkListRequest(request recordedRequest) error {
	if err := requireRequest(request, http.MethodGet, heartRatePath); err != nil {
		return err
	}
	if request.query.Get("pageSize") != "2" {
		return fmt.Errorf("pageSize = %q, want 2", request.query.Get("pageSize"))
	}
	filter := request.query.Get("filter")
	if !strings.Contains(filter, "heart_rate.sample_time.physical_time") ||
		!strings.Contains(filter, "2026-09-01T00:00:00Z") ||
		!strings.Contains(filter, "2026-09-03T00:00:00Z") {
		return fmt.Errorf("unexpected filter %q", filter)
	}
	if request.header.Get("Authorization") != "Bearer local-e2e-token" {
		return fmt.Errorf("authorization = %q", request.header.Get("Authorization"))
	}
	if !strings.HasPrefix(request.header.Get("x-goog-api-client"), "ghealth/") {
		return fmt.Errorf("x-goog-api-client = %q", request.header.Get("x-goog-api-client"))
	}
	return nil
}

func checkListResult(result commandResult) error {
	if err := requireExit(result, 0); err != nil {
		return err
	}
	if err := requireEmpty("stderr", result.stderr, result); err != nil {
		return err
	}
	body, err := decodeJSONObject(result.stdout)
	if err != nil {
		return result.failure(fmt.Sprintf("list stdout is not JSON: %v", err))
	}
	points, err := jsonArray(body, "dataPoints")
	if err != nil || len(points) != 1 {
		return result.failure(fmt.Sprintf("dataPoints length = %d, want 1: %v", len(points), err))
	}
	if err := requireJSONField(points[0], "beatsPerMinute", json.Number("72")); err != nil {
		return result.failure(err.Error())
	}
	return nil
}

func scenarioListPagination(h *harness) error {
	env, _, err := h.scenarioEnv("list-pagination")
	if err != nil {
		return err
	}
	first := jsonResponse(http.StatusOK, heartRateResponse("first", 61, "page-2"))
	first.check = func(request recordedRequest) error {
		if request.query.Get("pageToken") != "" || request.query.Get("pageSize") != "2" {
			return fmt.Errorf("first query = %v", request.query)
		}
		return nil
	}
	second := jsonResponse(http.StatusOK, heartRateResponse("second", 62, ""))
	second.check = func(request recordedRequest) error {
		if request.query.Get("pageToken") != "page-2" || request.query.Get("pageSize") != "1" {
			return fmt.Errorf("second query = %v", request.query)
		}
		return nil
	}
	api := newFakeAPI(first, second)
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "2")
	if err := requireExit(result, 0); err != nil {
		return err
	}
	body, err := decodeJSONObject(result.stdout)
	if err != nil {
		return result.failure(fmt.Sprintf("paginated stdout is not JSON: %v", err))
	}
	points, err := jsonArray(body, "dataPoints")
	if err != nil || len(points) != 2 {
		return result.failure(fmt.Sprintf("dataPoints length = %d, want 2: %v", len(points), err))
	}
	if _, exists := body["nextPageToken"]; exists {
		return result.failure("exhausted pagination retained nextPageToken")
	}
	if fmt.Sprint(points[0]["beatsPerMinute"]) != "61" || fmt.Sprint(points[1]["beatsPerMinute"]) != "62" {
		return result.failure(fmt.Sprintf("unexpected paginated rows: %v", points))
	}
	return api.Verify(2)
}

func scenarioDailyRollup(h *harness) error {
	env, _, err := h.scenarioEnv("daily-rollup-post")
	if err != nil {
		return err
	}
	response := jsonResponse(http.StatusOK, `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":9,"day":1}},"civilEndTime":{"date":{"year":2026,"month":9,"day":2}},"steps":{"count":1234}}]}`)
	response.check = checkDailyRollupRequest
	api := newFakeAPI(response)
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "steps", "daily-rollup", "--from", "2026-09-01", "--to", "2026-09-01")
	if err := checkDailyRollupResult(result); err != nil {
		return err
	}
	return api.Verify(1)
}

func checkDailyRollupRequest(request recordedRequest) error {
	if err := requireRequest(request, http.MethodPost, stepsRollupPath); err != nil {
		return err
	}
	if request.header.Get("Content-Type") != "application/json" {
		return fmt.Errorf("content-type = %q", request.header.Get("Content-Type"))
	}
	var body map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(string(request.body)))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return fmt.Errorf("decode rollup request: %w", err)
	}
	if err := requireJSONField(body, "windowSizeDays", json.Number("1")); err != nil {
		return err
	}
	if _, ok := body["range"].(map[string]interface{}); !ok {
		return fmt.Errorf("rollup request has no range object: %v", body)
	}
	return nil
}

func checkDailyRollupResult(result commandResult) error {
	if err := requireExit(result, 0); err != nil {
		return err
	}
	body, err := decodeJSONObject(result.stdout)
	if err != nil {
		return result.failure(fmt.Sprintf("daily rollup stdout is not JSON: %v", err))
	}
	rows, err := jsonArray(body, "dataPoints")
	if err != nil || len(rows) != 1 {
		return result.failure(fmt.Sprintf("daily rollup rows = %v: %v", rows, err))
	}
	if fmt.Sprint(rows[0]["count"]) != "1234" || rows[0]["date"] != "2026-09-01" {
		return result.failure(fmt.Sprintf("unexpected daily rollup rows: %v", rows))
	}
	return nil
}

func scenarioCSVFileOutput(h *harness) error {
	env, configDir, err := h.scenarioEnv("csv-file-output")
	if err != nil {
		return err
	}
	body := `{"dataPoints":[` + heartRatePoint("one", 72.123456789) + `,` + heartRatePoint("two", 73.5) + `],"nextPageToken":"remaining-page"}`
	api := newFakeAPI(jsonResponse(http.StatusOK, body))
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	outputPath := filepath.Join(configDir, "heart-rate.csv")
	result := h.run(env, "--format", "csv", "--output", outputPath, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "2")
	if err := requireExit(result, 0); err != nil {
		return err
	}
	if err := requireContains("stdout", result.stdout, "Wrote 2 rows", result); err != nil {
		return err
	}
	if err := requireContains("stderr", result.stderr, "nextPageToken: remaining-page", result); err != nil {
		return err
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return fmt.Errorf("open CSV output: %w", err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return fmt.Errorf("read CSV output: %w", err)
	}
	if len(records) != 3 {
		return fmt.Errorf("CSV records = %d, want header + 2 rows: %v", len(records), records)
	}
	joined := strings.Join(records[1], ",") + "\n" + strings.Join(records[2], ",")
	if !strings.Contains(joined, "72.123456789") || !strings.Contains(joined, "73.5") {
		return fmt.Errorf("CSV lost numeric precision: %v", records)
	}
	return api.Verify(1)
}

func scenarioAPIError(h *harness) error {
	env, _, err := h.scenarioEnv("api-error-exit-1")
	if err != nil {
		return err
	}
	api := newFakeAPI(jsonResponse(http.StatusBadRequest, `{"error":{"code":400,"message":"invalid filter","status":"INVALID_ARGUMENT"}}`))
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "1")
	if err := requireCLIError(result, 1, "api"); err != nil {
		return err
	}
	errorJSON, _ := decodeJSONObject(result.stderr)
	if err := requireJSONField(errorJSON, "error.status", json.Number("400")); err != nil {
		return result.failure(err.Error())
	}
	if err := requireContains("stderr", result.stderr, "invalid filter", result); err != nil {
		return err
	}
	return api.Verify(1)
}

func scenarioAuthError(h *harness) error {
	env, _, err := h.scenarioEnv("auth-error-exit-2")
	if err != nil {
		return err
	}
	delete(env, "GHEALTH_ACCESS_TOKEN")
	api := newFakeAPI()
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "1")
	if err := requireCLIError(result, 2, "auth"); err != nil {
		return err
	}
	return api.Verify(0)
}

func scenarioNetworkError(h *harness) error {
	env, _, err := h.scenarioEnv("network-error-exit-4")
	if err != nil {
		return err
	}
	api := newFakeAPI()
	env["GHEALTH_BASE_URL"] = api.URL()
	api.Close()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "1")
	return requireCLIError(result, 4, "network")
}

func scenarioConfigError(h *harness) error {
	env, configDir, err := h.scenarioEnv("config-error-exit-5")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("not = [valid"), 0o600); err != nil {
		return fmt.Errorf("write malformed config: %w", err)
	}
	result := h.run(env, "config", "show")
	return requireCLIError(result, 5, "config")
}

func scenarioUnauthorizedRetry(h *harness) error {
	env, _, err := h.scenarioEnv("401-refresh-and-retry")
	if err != nil {
		return err
	}
	unauthorized := jsonResponse(http.StatusUnauthorized, `{"error":{"code":401,"message":"expired token","status":"UNAUTHENTICATED"}}`)
	success := jsonResponse(http.StatusOK, heartRateResponse("after-refresh", 64, ""))
	api := newFakeAPI(unauthorized, success)
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "1")
	if err := requireExit(result, 0); err != nil {
		return err
	}
	requests := api.Requests()
	for i, request := range requests {
		if request.header.Get("Authorization") != "Bearer local-e2e-token" {
			return result.failure(fmt.Sprintf("request %d Authorization = %q", i+1, request.header.Get("Authorization")))
		}
	}
	if err := requireContains("stdout", result.stdout, `"beatsPerMinute": 64`, result); err != nil {
		return err
	}
	return api.Verify(2)
}

func scenarioServerRetryExhaustion(h *harness) error {
	env, _, err := h.scenarioEnv("503-retry-exhaustion")
	if err != nil {
		return err
	}
	responses := make([]fakeResponse, 4)
	for i := range responses {
		responses[i] = jsonResponse(http.StatusServiceUnavailable, `{"error":{"code":503,"message":"temporarily unavailable","status":"UNAVAILABLE"}}`)
	}
	api := newFakeAPI(responses...)
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "1")
	if err := requireCLIError(result, 1, "api"); err != nil {
		return err
	}
	if result.duration < 7*time.Second {
		return result.failure(fmt.Sprintf("retry duration = %s, want at least 7s", result.duration))
	}
	return api.Verify(4)
}

func scenarioMalformedResponse(h *harness) error {
	env, _, err := h.scenarioEnv("malformed-server-response")
	if err != nil {
		return err
	}
	api := newFakeAPI(jsonResponse(http.StatusOK, `{"dataPoints":`))
	defer api.Close()
	env["GHEALTH_BASE_URL"] = api.URL()
	result := h.run(env, "data", "heart-rate", "list", "--from", "2026-09-01", "--limit", "1")
	if err := requireCLIError(result, 1, "api"); err != nil {
		return err
	}
	if err := requireContains("stderr", result.stderr, "decode", result); err != nil {
		return err
	}
	return api.Verify(1)
}

func requireCLIError(result commandResult, exitCode int, errorType string) error {
	if err := requireExit(result, exitCode); err != nil {
		return err
	}
	if err := requireEmpty("stdout", result.stdout, result); err != nil {
		return err
	}
	body, err := decodeJSONObject(result.stderr)
	if err != nil {
		return result.failure(fmt.Sprintf("stderr is not structured JSON: %v", err))
	}
	if err := requireJSONField(body, "error.type", errorType); err != nil {
		return result.failure(err.Error())
	}
	if err := requireJSONField(body, "error.code", json.Number(strconv.Itoa(exitCode))); err != nil {
		return result.failure(err.Error())
	}
	return nil
}

func jsonArray(body map[string]interface{}, key string) ([]map[string]interface{}, error) {
	raw, ok := body[key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("%q is not an array", key)
	}
	rows := make([]map[string]interface{}, 0, len(raw))
	for i, value := range raw {
		row, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not an object", key, i)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func heartRateResponse(id string, bpm interface{}, nextPageToken string) string {
	response := `{"dataPoints":[` + heartRatePoint(id, bpm) + `]`
	if nextPageToken != "" {
		response += `,"nextPageToken":` + strconv.Quote(nextPageToken)
	}
	return response + `}`
}

func heartRatePoint(id string, bpm interface{}) string {
	return fmt.Sprintf(`{"name":"users/me/dataTypes/heart-rate/dataPoints/%s","heartRate":{"sampleTime":{"physicalTime":"2026-09-01T10:00:00Z","utcOffset":"+00:00"},"beatsPerMinute":%v},"dataSource":{"device":{"manufacturer":"Local","model":"Fixture"}}}`, id, bpm)
}
