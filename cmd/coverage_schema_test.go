package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ghealth/pkg/client"
	"ghealth/pkg/config"
)

func coverageSchemaEnv(t *testing.T) {
	t.Helper()
	coverageConfigEnv(t)
	doc := `{"schemas":{"steps":{"properties":{"count":{"type":"integer","format":"int64","description":"Required. Step count"},"interval":{"$ref":"Interval"}}},"Interval":{"properties":{"startTime":{},"endTime":{}}}}}`
	path := config.DiscoveryCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func coverageSchemaJSON(t *testing.T, fn func() error) map[string]interface{} {
	t.Helper()
	out, err := coverageRunStdout(t, fn)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("schema output: %v\n%s", err, out)
	}
	return got
}

func TestCoverageSchemaTypesAndType(t *testing.T) {
	coverageSchemaEnv(t)
	all := coverageSchemaJSON(t, func() error { return runSchemaTypes(schemaTypesCmd, nil) })
	if all["source"] != "cache" || all["count"].(float64) != 40 {
		t.Fatalf("schema types = %#v", all)
	}

	one := coverageSchemaJSON(t, func() error { return runSchemaType(schemaTypeCmd, []string{"steps"}) })
	if one["id"] != "steps" || one["source"] != "cache" {
		t.Fatalf("schema type = %#v", one)
	}
	fields, ok := one["fields"].([]interface{})
	if !ok || len(fields) != 2 {
		t.Fatalf("fields = %#v", one["fields"])
	}

	err := runSchemaType(schemaTypeCmd, []string{"does-not-exist"})
	got, ok := client.AsCLIError(err)
	if !ok || got.Code != client.ExitValidation || !strings.Contains(got.Message, "unknown data type") {
		t.Fatalf("unknown type error = %#v", err)
	}
}

func TestCoverageSchemaScopesAndEndpoints(t *testing.T) {
	coverageSchemaEnv(t)
	scopes := coverageSchemaJSON(t, func() error { return runSchemaScopes(schemaScopesCmd, nil) })
	if scopes["count"].(float64) == 0 || len(scopes["scopes"].([]interface{})) == 0 {
		t.Fatalf("scopes = %#v", scopes)
	}
	endpoints := coverageSchemaJSON(t, func() error { return runSchemaEndpoints(schemaEndpointsCmd, nil) })
	if endpoints["count"].(float64) < 10 || len(endpoints["endpoints"].([]interface{})) < 10 {
		t.Fatalf("endpoints = %#v", endpoints)
	}
}

func TestCoverageSchemaExtractionFallbacks(t *testing.T) {
	if got := extractFieldsFromDiscovery(nil, "steps"); got != nil {
		t.Fatalf("nil document fields = %#v", got)
	}
	if got := extractFieldsFromDiscovery(json.RawMessage(`not-json`), "steps"); got != nil {
		t.Fatalf("invalid document fields = %#v", got)
	}
	if got := extractFieldsFromDiscovery(json.RawMessage(`{"schemas":{"Other":{"properties":{}}}}`), "steps"); got != nil {
		t.Fatalf("missing schema fields = %#v", got)
	}
	if got := toPascalCase("heart-rate"); got != "HeartRate" {
		t.Fatalf("toPascalCase = %q", got)
	}
	if got := toPascalCase("already--split"); got != "AlreadySplit" {
		t.Fatalf("toPascalCase empty segment = %q", got)
	}
}
