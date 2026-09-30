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
	"reflect"
	"testing"
)

// TestMiscCharParseSchemaFields_Empty pins nil-return behaviour for schemas
// with no properties or invalid JSON.
func TestMiscCharParseSchemaFields_Empty(t *testing.T) {
	if got := parseSchemaFields(json.RawMessage(`{}`), nil); got != nil {
		t.Errorf("empty schema: got %v, want nil", got)
	}
	if got := parseSchemaFields(json.RawMessage(`not json`), nil); got != nil {
		t.Errorf("invalid JSON: got %v, want nil", got)
	}
}

// TestMiscCharParseSchemaFields_Basic pins field ordering (alphabetical) and
// the plain type/format/description/enum extraction.
func TestMiscCharParseSchemaFields_Basic(t *testing.T) {
	schema := json.RawMessage(`{
		"properties": {
			"zebra": {"type": "string", "format": "byte"},
			"alpha": {"type": "integer", "description": "Required. The count.", "enum": ["a", "b"]}
		}
	}`)
	got := parseSchemaFields(schema, nil)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0]["name"] != "alpha" {
		t.Errorf("got[0][name] = %v, want alpha (alphabetical order)", got[0]["name"])
	}
	if got[0]["type"] != "integer" {
		t.Errorf("got[0][type] = %v, want integer", got[0]["type"])
	}
	if got[0]["description"] != "Required. The count." {
		t.Errorf("got[0][description] = %v", got[0]["description"])
	}
	if got[0]["required"] != true {
		t.Errorf("got[0][required] = %v, want true (Required. prefix)", got[0]["required"])
	}
	if !reflect.DeepEqual(got[0]["enum"], []string{"a", "b"}) {
		t.Errorf("got[0][enum] = %v, want [a b]", got[0]["enum"])
	}
	if got[1]["name"] != "zebra" {
		t.Errorf("got[1][name] = %v, want zebra", got[1]["name"])
	}
	if got[1]["type"] != "string" {
		t.Errorf("got[1][type] = %v, want string", got[1]["type"])
	}
	if got[1]["format"] != "byte" {
		t.Errorf("got[1][format] = %v, want byte", got[1]["format"])
	}
	if _, ok := got[1]["required"]; ok {
		t.Errorf("got[1][required] should be absent, got %v", got[1]["required"])
	}
}

// TestMiscCharParseSchemaFields_RefAndArrayRef pins $ref resolution to
// type=object/array plus properties/itemProperties from allSchemas.
func TestMiscCharParseSchemaFields_RefAndArrayRef(t *testing.T) {
	allSchemas := map[string]json.RawMessage{
		"#/schemas/Foo": json.RawMessage(`{"properties": {"b": {}, "a": {}}}`),
		"#/schemas/Bar": json.RawMessage(`{"properties": {"y": {}, "x": {}}}`),
	}
	schema := json.RawMessage(`{
		"properties": {
			"ref": {"$ref": "#/schemas/Foo"},
			"list": {"type": "array", "items": {"$ref": "#/schemas/Bar"}}
		}
	}`)
	got := parseSchemaFields(schema, allSchemas)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	// alphabetical: "list" before "ref"
	if got[0]["name"] != "list" {
		t.Fatalf("got[0][name] = %v, want list", got[0]["name"])
	}
	if got[0]["type"] != "array" {
		t.Errorf("got[0][type] = %v, want array", got[0]["type"])
	}
	if !reflect.DeepEqual(got[0]["itemProperties"], []string{"x", "y"}) {
		t.Errorf("got[0][itemProperties] = %v, want [x y]", got[0]["itemProperties"])
	}
	if got[1]["name"] != "ref" {
		t.Fatalf("got[1][name] = %v, want ref", got[1]["name"])
	}
	if got[1]["type"] != "object" {
		t.Errorf("got[1][type] = %v, want object", got[1]["type"])
	}
	if !reflect.DeepEqual(got[1]["properties"], []string{"a", "b"}) {
		t.Errorf("got[1][properties] = %v, want [a b]", got[1]["properties"])
	}
}

// TestMiscCharParseSchemaFields_UnresolvableRef pins the case where a $ref
// or items.$ref points at a schema absent from allSchemas: type is still set
// but the properties/itemProperties key is omitted.
func TestMiscCharParseSchemaFields_UnresolvableRef(t *testing.T) {
	schema := json.RawMessage(`{"properties": {"ref": {"$ref": "#/schemas/Missing"}}}`)
	got := parseSchemaFields(schema, map[string]json.RawMessage{})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0]["type"] != "object" {
		t.Errorf("type = %v, want object", got[0]["type"])
	}
	if _, ok := got[0]["properties"]; ok {
		t.Errorf("properties should be absent for unresolvable ref, got %v", got[0]["properties"])
	}
}

// TestMiscCharResolveRefProperties_MissingRef pins nil for an unknown ref.
func TestMiscCharResolveRefProperties_MissingRef(t *testing.T) {
	if got := resolveRefProperties("#/schemas/Nope", map[string]json.RawMessage{}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
