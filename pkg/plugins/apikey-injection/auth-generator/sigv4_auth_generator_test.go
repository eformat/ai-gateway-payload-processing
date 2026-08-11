/*
Copyright 2026 The opendatahub.io Authors.

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

package authgenerator

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestSigV4AuthHeadersGenerator(t *testing.T) {
	validCredentials := map[string]string{
		"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
		"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"request_body":          `{"model":"anthropic.claude-v2","prompt":"hello"}`,
		"endpoint":              "bedrock-runtime.us-east-1.amazonaws.com",
		"path":                  "/default/bedrock-model/v1/chat/completions",
		"region":                "us-east-1",
		"service":               "bedrock",
	}

	validCredentialsWithToken := map[string]string{
		"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
		"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"aws-session-token":     "FwoGZXIvYXdzEBYaDH7example-session-token",
		"request_body":          `{"model":"anthropic.claude-v2","prompt":"hello"}`,
		"endpoint":              "bedrock-runtime.us-east-1.amazonaws.com",
		"path":                  "/default/bedrock-model/v1/chat/completions",
		"region":                "us-east-1",
		"service":               "bedrock",
	}

	tests := []struct {
		name             string
		credentials      map[string]string
		wantHeaders      []string
		wantAuthPrefix   string
		wantNoHeader     string
		wantBodyHash     bool
		wantRegionInAuth string
		wantErrContains  string
	}{
		{
			name:           "valid credentials without session token",
			credentials:    validCredentials,
			wantHeaders:    []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256"},
			wantAuthPrefix: "AWS4-HMAC-SHA256",
			wantNoHeader:   "X-Amz-Security-Token",
			wantBodyHash:   true,
		},
		{
			name:           "valid credentials with session token",
			credentials:    validCredentialsWithToken,
			wantHeaders:    []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256", "X-Amz-Security-Token"},
			wantAuthPrefix: "AWS4-HMAC-SHA256",
			wantBodyHash:   true,
		},
		{
			name: "missing access key returns error",
			credentials: map[string]string{
				"aws-secret-access-key": "secret",
				"request_body":          "{}",
				"endpoint":              "bedrock-runtime.us-east-1.amazonaws.com",
				"region":                "us-east-1",
				"service":               "bedrock",
			},
			wantErrContains: "aws-access-key-id",
		},
		{
			name: "missing secret key returns error",
			credentials: map[string]string{
				"aws-access-key-id": "AKIA...",
				"request_body":      "{}",
				"endpoint":          "bedrock-runtime.us-east-1.amazonaws.com",
				"region":            "us-east-1",
				"service":           "bedrock",
			},
			wantErrContains: "aws-secret-access-key",
		},
		{
			name: "explicit region overrides hostname extraction",
			credentials: map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"request_body":          `{"model":"anthropic.claude-v2","prompt":"hello"}`,
				"endpoint":              "bedrock-runtime.us-east-1.amazonaws.com",
				"path":                  "/default/bedrock-model/v1/chat/completions",
				"region":                "eu-west-2",
				"service":               "bedrock",
			},
			wantHeaders:      []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256"},
			wantAuthPrefix:   "AWS4-HMAC-SHA256",
			wantRegionInAuth: "eu-west-2",
			wantBodyHash:     true,
		},
		{
			name: "explicit region with VPC endpoint that would fail hostname parsing",
			credentials: map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"request_body":          `{"model":"anthropic.claude-v2","prompt":"hello"}`,
				"endpoint":              "vpce-0123456789.bedrock-runtime.us-west-2.vpce.amazonaws.com",
				"path":                  "/default/bedrock-model/v1/chat/completions",
				"region":                "us-west-2",
				"service":               "bedrock",
			},
			wantHeaders:      []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256"},
			wantAuthPrefix:   "AWS4-HMAC-SHA256",
			wantRegionInAuth: "us-west-2",
			wantBodyHash:     true,
		},
		{
			name: "empty access key returns error",
			credentials: map[string]string{
				"aws-access-key-id":     "",
				"aws-secret-access-key": "secret",
				"request_body":          "{}",
				"endpoint":              "bedrock-runtime.us-east-1.amazonaws.com",
				"region":                "us-east-1",
				"service":               "bedrock",
			},
			wantErrContains: "aws-access-key-id",
		},
		{
			name:            "empty credentials returns error",
			credentials:     map[string]string{},
			wantErrContains: "aws-access-key-id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := &SigV4AuthGenerator{}
			authHeaders, err := generator.GenerateAuthHeaders(test.credentials)

			if test.wantErrContains != "" {
				if err == nil {
					t.Errorf("expected error containing %q but got nil", test.wantErrContains)
				} else if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Errorf("expected error containing %q, got: %v", test.wantErrContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, header := range test.wantHeaders {
				val, ok := authHeaders[header]
				if !ok {
					t.Errorf("expected header %q not found in result", header)
					continue
				}
				if val == "" {
					t.Errorf("header %q is empty", header)
				}
			}

			if test.wantAuthPrefix != "" {
				auth := authHeaders["Authorization"]
				if !strings.HasPrefix(auth, test.wantAuthPrefix) {
					t.Errorf("Authorization header should start with %q, got: %q", test.wantAuthPrefix, auth)
				}
			}

			if test.wantNoHeader != "" {
				if _, ok := authHeaders[test.wantNoHeader]; ok {
					t.Errorf("header %q should not be present", test.wantNoHeader)
				}
			}

			if test.wantRegionInAuth != "" {
				auth := authHeaders["Authorization"]
				if !strings.Contains(auth, test.wantRegionInAuth) {
					t.Errorf("Authorization header should contain region %q, got: %q", test.wantRegionInAuth, auth)
				}
			}

			if test.wantBodyHash {
				body := test.credentials["request_body"]
				expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
				if got := authHeaders["X-Amz-Content-Sha256"]; got != expectedHash {
					t.Errorf("content hash mismatch: want %q, got %q", expectedHash, got)
				}
			}
		})
	}
}

func TestRegionFromEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		wantRegion string
		wantErr    bool
	}{
		{
			name:       "standard bedrock endpoint",
			endpoint:   "bedrock-runtime.us-east-1.amazonaws.com",
			wantRegion: "us-east-1",
		},
		{
			name:       "eu-west-1 region",
			endpoint:   "bedrock-runtime.eu-west-1.amazonaws.com",
			wantRegion: "eu-west-1",
		},
		{
			name:       "ap-southeast-1 region",
			endpoint:   "bedrock-runtime.ap-southeast-1.amazonaws.com",
			wantRegion: "ap-southeast-1",
		},
		{
			name:     "too few parts - localhost",
			endpoint: "localhost",
			wantErr:  true,
		},
		{
			name:     "only three parts",
			endpoint: "bedrock.us-east-1.com",
			wantErr:  true,
		},
		{
			name:     "VPC endpoint - non-standard format requires explicit region",
			endpoint: "vpce-0123456789.bedrock-runtime.us-west-2.vpce.amazonaws.com",
			wantErr:  true,
		},
		{
			name:     "endpoint with scheme rejected",
			endpoint: "https://bedrock-runtime.us-east-1.amazonaws.com",
			wantErr:  true,
		},
		{
			name:     "wrong domain suffix",
			endpoint: "bedrock-runtime.us-east-1.attacker.tld",
			wantErr:  true,
		},
		{
			name:     "five parts - extra subdomain",
			endpoint: "extra.bedrock-runtime.us-east-1.amazonaws.com",
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			region, err := regionFromEndpoint(test.endpoint)

			if test.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if region != test.wantRegion {
				t.Errorf("region mismatch: want %q, got %q", test.wantRegion, region)
			}
		})
	}
}
