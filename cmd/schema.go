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
	"sort"
	"strings"

	"ghealth/pkg/auth"
	"ghealth/pkg/client"
	"ghealth/pkg/schema"
	"ghealth/pkg/types"
	"github.com/spf13/cobra"
)

// errFailedToEncodeOutput is returned when json.MarshalIndent unexpectedly
// fails while preparing a schema command's response payload.
const errFailedToEncodeOutput = "failed to encode output"

// HTTP methods and operation-kind/field-name literals repeated across the
// schema-type/scopes/endpoints output shapes (go:S1192).
const (
	httpMethodGet    = "GET"
	httpMethodPost   = "POST"
	httpMethodPatch  = "PATCH"
	httpMethodDelete = "DELETE"

	opList        = "list"
	opCreate      = "create"
	opUpdate      = "update"
	opDelete      = "delete"
	opRollup      = "rollup"
	opDailyRollup = "daily-rollup"
	opReconcile   = "reconcile"
	opExportTCX   = "export-tcx"

	schemaFieldScopes      = "scopes"
	schemaFieldCategory    = "category"
	schemaFieldDescription = "description"
	schemaFieldCount       = "count"
	schemaFieldDataTypes   = "dataTypes"
	schemaFieldRange       = "range"
	schemaFieldMethod      = "method"
	schemaFieldPath        = "path"
	schemaFieldDataType    = "dataType"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Explore API schema, data types, scopes, and endpoints",
}

var schemaTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List all available data types",
	RunE:  runSchemaTypes,
}

var schemaTypeCmd = &cobra.Command{
	Use:   "type <name>",
	Short: "Show details for a specific data type",
	Args:  cobra.ExactArgs(1),
	RunE:  runSchemaType,
}

var schemaScopesCmd = &cobra.Command{
	Use:   schemaFieldScopes,
	Short: "List all OAuth scopes with associated data types",
	RunE:  runSchemaScopes,
}

var schemaEndpointsCmd = &cobra.Command{
	Use:   "endpoints",
	Short: "List all API endpoints",
	RunE:  runSchemaEndpoints,
}

func init() {
	rootCmd.AddCommand(schemaCmd)
	schemaCmd.AddCommand(schemaTypesCmd)
	schemaCmd.AddCommand(schemaTypeCmd)
	schemaCmd.AddCommand(schemaScopesCmd)
	schemaCmd.AddCommand(schemaEndpointsCmd)
}

func runSchemaTypes(cmd *cobra.Command, args []string) error {
	source := "registry"
	doc, src, err := schema.FetchDiscovery()
	if err == nil {
		source = src
	}
	_ = doc // types command uses registry, not discovery

	// Build type list from registry (always authoritative for type metadata).
	ids := types.IDs()
	typeList := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		dt := types.Get(id)
		typeList = append(typeList, map[string]interface{}{
			"id":                   dt.ID,
			schemaFieldCategory:    dt.Category,
			schemaFieldDescription: dt.Description,
			"writable":             dt.Writable,
			"rollupOnly":           dt.RollupOnly,
			"operations":           dt.Operations,
		})
	}

	result := map[string]interface{}{
		"source":             source,
		schemaFieldCount:     len(typeList),
		schemaFieldDataTypes: typeList,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError(errFailedToEncodeOutput, "")
	}

	return printOutput(json.RawMessage(data))
}

func runSchemaType(cmd *cobra.Command, args []string) error {
	name := args[0]
	dt := types.Get(name)
	if dt == nil {
		return client.NewValidationError(
			fmt.Sprintf("unknown data type: %s", name),
			"Run 'ghealth schema types' to see available types",
		)
	}

	source := "registry"
	var discoveryDoc json.RawMessage

	doc, src, err := schema.FetchDiscovery()
	if err == nil {
		source = src
		discoveryDoc = doc
	}

	// Extract fields from discovery doc if available.
	fields := extractFieldsFromDiscovery(discoveryDoc, name)

	result := map[string]interface{}{
		"source":               source,
		"id":                   dt.ID,
		"filterName":           dt.FilterName,
		schemaFieldCategory:    dt.Category,
		"scope":                dt.FullScope(),
		schemaFieldDescription: dt.Description,
		"operations":           dt.Operations,
		"writable":             dt.Writable,
		"rollupOnly":           dt.RollupOnly,
		"parameters":           buildOperationParameters(dt),
	}
	if len(fields) > 0 {
		result["fields"] = fields
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError(errFailedToEncodeOutput, "")
	}

	return printOutput(json.RawMessage(data))
}

// buildOperationParameters describes the REAL request parameters per
// operation, derived from the type registry (FilterPath) — v4 has no
// startTime/endTime/bucketDuration parameters on these endpoints.
func buildOperationParameters(dt *types.DataType) map[string]interface{} {
	const (
		pageSizeDesc         = "integer (server default 1440, max 10000; exercise/sleep max 25)"
		pageTokenDesc        = "string — continuation token from a previous response"
		dataSourceFamilyDesc = "all-sources | google-wearables | google-sources"
	)

	params := map[string]interface{}{}
	for _, op := range dt.Operations {
		switch op {
		case opList:
			p := map[string]interface{}{
				"pageSize":  pageSizeDesc,
				"pageToken": pageTokenDesc,
			}
			if tmpl := filterTemplate(dt); tmpl != "" {
				p["filter"] = tmpl
			}
			params[op] = p
		case opRollup:
			params[op] = map[string]interface{}{
				"body": map[string]interface{}{
					schemaFieldRange: map[string]string{
						"startTime": "<RFC3339>",
						"endTime":   "<RFC3339, exclusive>",
					},
					"windowSize":       `duration string, e.g. "3600s", "86400s" (required)`,
					"pageSize":         pageSizeDesc,
					"pageToken":        pageTokenDesc,
					"dataSourceFamily": dataSourceFamilyDesc,
				},
			}
		case opDailyRollup:
			params[op] = map[string]interface{}{
				"body": map[string]interface{}{
					schemaFieldRange: map[string]string{
						"start": "civil date {year, month, day}",
						"end":   "civil date {year, month, day}, exclusive",
					},
					"windowSizeDays":   "integer, default 1",
					"dataSourceFamily": dataSourceFamilyDesc,
				},
			}
		}
	}
	return params
}

// filterTemplate builds a concrete list-filter example from the registry's
// real filter field for this type. The placeholder reflects the field's
// timestamp format (RFC-3339 with Z for physical time, ISO 8601 without
// offset for civil time, plain dates for daily-summary types).
func filterTemplate(dt *types.DataType) string {
	path := dt.FilterPath()
	if path == "" {
		return ""
	}
	placeholder := "<ISO8601, no offset>"
	switch dt.TimeField {
	case types.TimeFieldSample, types.TimeFieldPhysicalIntervalStart:
		placeholder = "<RFC3339>"
	case types.TimeFieldDaily:
		placeholder = "<YYYY-MM-DD>"
	}
	if dt.TimeField == types.TimeFieldPhysicalIntervalStart {
		// Only a lower bound is supported (e.g. electrocardiogram).
		return fmt.Sprintf("%s >= %q", path, placeholder)
	}
	return fmt.Sprintf("%s >= %q AND %s < %q", path, placeholder, path, placeholder)
}

func runSchemaScopes(cmd *cobra.Command, args []string) error {
	scopeList := make([]map[string]interface{}, 0, len(auth.AllScopes))
	for _, s := range auth.AllScopes {
		// Find data types associated with this scope's category.
		var associatedTypes []string
		for _, dt := range types.All() {
			if dt.Category == s.Category {
				associatedTypes = append(associatedTypes, dt.ID)
			}
		}
		sort.Strings(associatedTypes)

		scopeList = append(scopeList, map[string]interface{}{
			"scope":              auth.FullScope(s.Suffix),
			"suffix":             s.Suffix,
			"label":              s.Label,
			schemaFieldCategory:  s.Category,
			schemaFieldDataTypes: associatedTypes,
		})
	}

	result := map[string]interface{}{
		schemaFieldCount:  len(scopeList),
		schemaFieldScopes: scopeList,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError(errFailedToEncodeOutput, "")
	}

	return printOutput(json.RawMessage(data))
}

func runSchemaEndpoints(cmd *cobra.Command, args []string) error {
	var endpoints []map[string]interface{}

	// User endpoints (static).
	endpoints = append(endpoints,
		map[string]interface{}{schemaFieldMethod: httpMethodGet, schemaFieldPath: "/v4/users/me/identity", schemaFieldDescription: "Get user identity"},
		map[string]interface{}{schemaFieldMethod: httpMethodGet, schemaFieldPath: "/v4/users/me/profile", schemaFieldDescription: "Get user profile"},
		map[string]interface{}{schemaFieldMethod: httpMethodPatch, schemaFieldPath: "/v4/users/me/profile", schemaFieldDescription: "Update user profile"},
		map[string]interface{}{schemaFieldMethod: httpMethodGet, schemaFieldPath: "/v4/users/me/settings", schemaFieldDescription: "Get user settings"},
		map[string]interface{}{schemaFieldMethod: httpMethodPatch, schemaFieldPath: "/v4/users/me/settings", schemaFieldDescription: "Update user settings"},
	)

	// Webhook endpoints — project-level subscriber/subscription model
	// (discovery revision 20260528). These require the cloud-platform scope
	// and a configured project ID; see 'ghealth webhooks --help'.
	endpoints = append(endpoints,
		map[string]interface{}{schemaFieldMethod: httpMethodGet, schemaFieldPath: "/v4/projects/{project}/subscribers", schemaFieldDescription: "List webhook subscribers"},
		map[string]interface{}{schemaFieldMethod: httpMethodPost, schemaFieldPath: "/v4/projects/{project}/subscribers", schemaFieldDescription: "Create webhook subscriber"},
		map[string]interface{}{schemaFieldMethod: httpMethodPatch, schemaFieldPath: "/v4/projects/{project}/subscribers/{subscriber}", schemaFieldDescription: "Update webhook subscriber"},
		map[string]interface{}{schemaFieldMethod: httpMethodDelete, schemaFieldPath: "/v4/projects/{project}/subscribers/{subscriber}", schemaFieldDescription: "Delete webhook subscriber"},
		map[string]interface{}{schemaFieldMethod: httpMethodGet, schemaFieldPath: "/v4/projects/{project}/subscribers/{subscriber}/subscriptions", schemaFieldDescription: "List webhook subscriptions"},
		map[string]interface{}{schemaFieldMethod: httpMethodPost, schemaFieldPath: "/v4/projects/{project}/subscribers/{subscriber}/subscriptions", schemaFieldDescription: "Create webhook subscription"},
		map[string]interface{}{schemaFieldMethod: httpMethodPatch, schemaFieldPath: "/v4/projects/{project}/subscribers/{subscriber}/subscriptions/{subscription}", schemaFieldDescription: "Update webhook subscription"},
		map[string]interface{}{schemaFieldMethod: httpMethodDelete, schemaFieldPath: "/v4/projects/{project}/subscribers/{subscriber}/subscriptions/{subscription}", schemaFieldDescription: "Delete webhook subscription"},
	)

	// Data type endpoints (derived from registry).
	ids := types.IDs()
	for _, id := range ids {
		dt := types.Get(id)
		basePath := fmt.Sprintf("/v4/users/me/dataTypes/%s/dataPoints", dt.ID)

		for _, op := range dt.Operations {
			var method, path, desc string
			switch op {
			case opList:
				method, path, desc = httpMethodGet, basePath, fmt.Sprintf("List %s data points", id)
			case opCreate:
				method, path, desc = httpMethodPost, basePath, fmt.Sprintf("Create %s data point", id)
			case opUpdate:
				method, path, desc = httpMethodPatch, basePath+"/{id}", fmt.Sprintf("Update %s data point", id)
			case opDelete:
				method, path, desc = httpMethodPost, basePath+":batchDelete", fmt.Sprintf("Delete %s data points", id)
			case opRollup:
				method, path, desc = httpMethodPost, basePath+":rollUp", fmt.Sprintf("Roll up %s data", id)
			case opDailyRollup:
				method, path, desc = httpMethodPost, basePath+":dailyRollUp", fmt.Sprintf("Daily roll up %s data", id)
			case opReconcile:
				method, path, desc = httpMethodGet, basePath+":reconcile", fmt.Sprintf("Reconcile %s data", id)
			case opExportTCX:
				method, path, desc = httpMethodGet, basePath+"/{id}:exportExerciseTcx", fmt.Sprintf("Export %s as TCX", id)
			default:
				continue
			}
			endpoints = append(endpoints, map[string]interface{}{
				schemaFieldMethod:      method,
				schemaFieldPath:        path,
				schemaFieldDescription: desc,
				schemaFieldDataType:    id,
			})
		}
	}

	result := map[string]interface{}{
		schemaFieldCount: len(endpoints),
		"endpoints":      endpoints,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError(errFailedToEncodeOutput, "")
	}

	return printOutput(json.RawMessage(data))
}

// extractFieldsFromDiscovery attempts to pull field info from the discovery JSON,
// resolving $ref references to show nested object properties.
func extractFieldsFromDiscovery(doc json.RawMessage, dataTypeID string) []map[string]interface{} {
	if doc == nil {
		return nil
	}

	var parsed struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil || len(parsed.Schemas) == 0 {
		return nil
	}

	// Try to find a schema matching the data type name.
	candidates := []string{
		dataTypeID,
		types.KebabToSnake(dataTypeID),
		strings.ReplaceAll(dataTypeID, "-", ""),
		toPascalCase(dataTypeID),
	}

	for _, candidate := range candidates {
		if schemaRaw, found := parsed.Schemas[candidate]; found {
			return parseSchemaFields(schemaRaw, parsed.Schemas)
		}
	}

	return nil
}

// schemaProperty represents a single property from a discovery schema.
type schemaProperty struct {
	Type        string   `json:"type"`
	Ref         string   `json:"$ref"`
	Description string   `json:"description"`
	Format      string   `json:"format"`
	Enum        []string `json:"enum"`
	Items       *struct {
		Ref string `json:"$ref"`
	} `json:"items"`
}

func parseSchemaFields(schemaRaw json.RawMessage, allSchemas map[string]json.RawMessage) []map[string]interface{} {
	var s struct {
		Properties map[string]schemaProperty `json:"properties"`
	}
	if err := json.Unmarshal(schemaRaw, &s); err != nil || len(s.Properties) == 0 {
		return nil
	}

	// Sort field names for consistent output.
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		fields = append(fields, buildSchemaField(name, s.Properties[name], allSchemas))
	}
	return fields
}

// buildSchemaField converts a single discovery-schema property into the
// field map used by the schema-type/endpoints output.
func buildSchemaField(name string, prop schemaProperty, allSchemas map[string]json.RawMessage) map[string]interface{} {
	f := map[string]interface{}{
		"name": name,
	}
	applySchemaFieldType(f, prop, allSchemas)
	applySchemaFieldMetadata(f, prop)
	return f
}

// applySchemaFieldType sets "type" and, for $ref/array-of-$ref properties,
// the resolved "properties"/"itemProperties" from allSchemas.
func applySchemaFieldType(f map[string]interface{}, prop schemaProperty, allSchemas map[string]json.RawMessage) {
	switch {
	case prop.Ref != "":
		f["type"] = "object"
		if subProps := resolveRefProperties(prop.Ref, allSchemas); len(subProps) > 0 {
			f["properties"] = subProps
		}
	case prop.Type == "array" && prop.Items != nil && prop.Items.Ref != "":
		f["type"] = "array"
		if subProps := resolveRefProperties(prop.Items.Ref, allSchemas); len(subProps) > 0 {
			f["itemProperties"] = subProps
		}
	case prop.Type != "":
		f["type"] = prop.Type
	}
}

// applySchemaFieldMetadata sets "format", "description" (plus the derived
// "required" flag from a "Required." description prefix), and "enum".
func applySchemaFieldMetadata(f map[string]interface{}, prop schemaProperty) {
	if prop.Format != "" {
		f["format"] = prop.Format
	}
	if prop.Description != "" {
		f[schemaFieldDescription] = prop.Description
		// Extract required/optional from description prefix.
		if strings.HasPrefix(prop.Description, "Required.") {
			f["required"] = true
		}
	}
	if len(prop.Enum) > 0 {
		f["enum"] = prop.Enum
	}
}

// resolveRefProperties looks up a $ref schema and returns its property names.
func resolveRefProperties(ref string, allSchemas map[string]json.RawMessage) []string {
	schemaRaw, ok := allSchemas[ref]
	if !ok {
		return nil
	}
	var s struct {
		Properties map[string]interface{} `json:"properties"`
	}
	if err := json.Unmarshal(schemaRaw, &s); err != nil {
		return nil
	}
	props := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		props = append(props, name)
	}
	sort.Strings(props)
	return props
}

func toPascalCase(kebab string) string {
	parts := strings.Split(kebab, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
