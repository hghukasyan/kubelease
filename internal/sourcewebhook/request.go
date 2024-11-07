/*
Copyright 2026 KubeLease Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sourcewebhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Action is a supported webhook verb.
type Action string

const (
	ActionCreate Action = "create"
	ActionExpire Action = "expire"
	ActionTouch  Action = "touch"
)

// Request is the JSON body accepted by POST /v1/leases.
// Unknown fields are rejected so callers cannot smuggle namespace/maxTTL/quota/etc.
type Request struct {
	Action    Action `json:"action"`
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
	Team      string `json:"team,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

var leaseNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func decodeRequest(r *http.Request, maxBody int64) (Request, error) {
	if r.Method != http.MethodPost {
		return Request{}, fmt.Errorf("method not allowed")
	}
	body := io.LimitReader(r.Body, maxBody+1)
	raw, err := io.ReadAll(body)
	if err != nil {
		return Request{}, fmt.Errorf("read body: %w", err)
	}
	if int64(len(raw)) > maxBody {
		return Request{}, fmt.Errorf("request body too large")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return Request{}, fmt.Errorf("empty request body")
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return Request{}, fmt.Errorf("malformed JSON: %w", err)
	}
	if dec.More() {
		return Request{}, fmt.Errorf("malformed JSON: trailing data")
	}
	return req, nil
}

func (req Request) validate() error {
	switch req.Action {
	case ActionCreate, ActionExpire, ActionTouch:
	case "":
		return fmt.Errorf("action is required")
	default:
		return fmt.Errorf("unsupported action %q", req.Action)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 63 {
		return fmt.Errorf("name must be at most 63 characters")
	}
	if !leaseNameRE.MatchString(name) {
		return fmt.Errorf("name must be a valid DNS-1123 label")
	}
	return nil
}
