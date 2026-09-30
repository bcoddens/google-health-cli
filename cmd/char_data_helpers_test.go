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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"ghealth/pkg/client"
)

// dataCharWithServer starts an httptest server, points client.BaseURL at it
// for the duration of the test (restored on cleanup), and sets a fake access
// token so the composite token source succeeds without touching real
// credentials, a config file, or the network.
func dataCharWithServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	origBaseURL := client.BaseURL
	client.BaseURL = srv.URL
	t.Cleanup(func() { client.BaseURL = origBaseURL })
	t.Setenv("GHEALTH_ACCESS_TOKEN", "dataChar-test-token")
	t.Setenv("GHEALTH_FORMAT", "json")
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	originalFormat := flagFormat
	flagFormat = "json"
	t.Cleanup(func() { flagFormat = originalFormat })
	return srv
}

// dataCharCaptureStdout redirects os.Stdout for the duration of fn and
// returns everything written to it, plus fn's own return value.
func dataCharCaptureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fnErr := fn()
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe writer: %v", cerr)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out), fnErr
}

// dataCharSetRaw sets the global --raw flag for the duration of the test and
// restores it on cleanup (executeDataList/executeDataRollup/executeDataGet
// all branch on this package-level var).
func dataCharSetRaw(t *testing.T, raw bool) {
	t.Helper()
	orig := flagRaw
	flagRaw = raw
	t.Cleanup(func() { flagRaw = orig })
}
