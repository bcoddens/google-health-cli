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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

type fakeResponse struct {
	status int
	header http.Header
	body   string
	check  func(recordedRequest) error
}

type recordedRequest struct {
	method string
	path   string
	query  url.Values
	header http.Header
	body   []byte
}

type fakeAPI struct {
	server *httptest.Server

	mu        sync.Mutex
	responses []fakeResponse
	requests  []recordedRequest
	errors    []error
}

func newFakeAPI(responses ...fakeResponse) *fakeAPI {
	api := &fakeAPI{responses: append([]fakeResponse(nil), responses...)}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	return api
}

func jsonResponse(status int, body string) fakeResponse {
	return fakeResponse{
		status: status,
		header: http.Header{"Content-Type": []string{"application/json"}},
		body:   body,
	}
}

func (a *fakeAPI) URL() string {
	return a.server.URL
}

func (a *fakeAPI) Close() {
	a.server.Close()
}

func (a *fakeAPI) Requests() []recordedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]recordedRequest(nil), a.requests...)
}

func (a *fakeAPI) Verify(wantRequests int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var messages []string
	if len(a.requests) != wantRequests {
		messages = append(messages, fmt.Sprintf("request count = %d, want %d", len(a.requests), wantRequests))
	}
	if len(a.responses) != 0 {
		messages = append(messages, fmt.Sprintf("%d queued responses were not consumed", len(a.responses)))
	}
	for _, err := range a.errors {
		messages = append(messages, err.Error())
	}
	if len(messages) > 0 {
		return fmt.Errorf("fake API verification failed: %s", strings.Join(messages, "; "))
	}
	return nil
}

func (a *fakeAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.recordError(fmt.Errorf("read request body: %w", err))
		http.Error(w, "cannot read request", http.StatusBadRequest)
		return
	}
	recorded := recordedRequest{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.Query(),
		header: r.Header.Clone(),
		body:   body,
	}

	a.mu.Lock()
	a.requests = append(a.requests, recorded)
	if len(a.responses) == 0 {
		a.errors = append(a.errors, fmt.Errorf("unexpected %s %s", r.Method, r.URL.RequestURI()))
		a.mu.Unlock()
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	}
	response := a.responses[0]
	a.responses = a.responses[1:]
	a.mu.Unlock()

	if response.check != nil {
		if err := response.check(recorded); err != nil {
			a.recordError(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	for key, values := range response.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if _, err := io.WriteString(w, response.body); err != nil {
		a.recordError(fmt.Errorf("write fixture response: %w", err))
	}
}

func (a *fakeAPI) recordError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errors = append(a.errors, err)
}

func requireRequest(request recordedRequest, method, path string) error {
	if request.method != method || request.path != path {
		return fmt.Errorf("request = %s %s, want %s %s", request.method, request.path, method, path)
	}
	return nil
}
