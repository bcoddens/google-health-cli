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
	"net/http"
	"testing"

	"ghealth/pkg/client"
)

// doRequest prints the server's JSON response verbatim (pretty-printed) on
// success.
func TestDataCharDoRequest_Success(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"users/me/dataTypes/steps/dataPoints/abc","done":false}`))
	})

	req := &client.Request{Method: "POST", Path: "/users/me/dataTypes/steps/dataPoints", Body: []byte(`{"steps":{}}`)}
	out, err := dataCharCaptureStdout(t, func() error {
		return doRequest(req)
	})
	if err != nil {
		t.Fatalf("doRequest error: %v", err)
	}
	var got map[string]interface{}
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil {
		t.Fatalf("output not valid JSON: %v\n%s", uerr, out)
	}
	if got["name"] != "users/me/dataTypes/steps/dataPoints/abc" {
		t.Errorf("name = %v, want the operation name", got["name"])
	}
}

// A 4xx API error is surfaced as a structured CLIError.
func TestDataCharDoRequest_APIErrorPassthrough(t *testing.T) {
	dataCharWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":409,"message":"already exists","status":"ALREADY_EXISTS"}}`))
	})

	req := &client.Request{Method: "POST", Path: "/users/me/dataTypes/steps/dataPoints", Body: []byte(`{"steps":{}}`)}
	_, err := dataCharCaptureStdout(t, func() error {
		return doRequest(req)
	})
	if err == nil {
		t.Fatal("doRequest error = nil, want API error")
	}
	cliErr, ok := client.AsCLIError(err)
	if !ok {
		t.Fatalf("error is not a *CLIError: %v (%T)", err, err)
	}
	if cliErr.Message != "already exists" {
		t.Errorf("cliErr.Message = %q, want %q", cliErr.Message, "already exists")
	}
	if cliErr.Status != 409 {
		t.Errorf("cliErr.Status = %d, want 409", cliErr.Status)
	}
}
