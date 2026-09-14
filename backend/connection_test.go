package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManifestSessionTokenDefaultsToEmptyString(t *testing.T) {
	data, err := os.ReadFile("../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributions []struct {
			ID     string `json:"id"`
			Fields []struct {
				Key      string `json:"key"`
				Default  any    `json:"default"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"contributions"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, contribution := range manifest.Contributions {
		if contribution.ID != pluginID+".connection" {
			continue
		}
		for _, field := range contribution.Fields {
			if field.Key == "session_token" {
				value, ok := field.Default.(string)
				if !ok || value != "" || field.Required {
					t.Fatal("session_token must be optional with an explicit empty-string default")
				}
				return
			}
		}
	}
	t.Fatal("missing session_token connection field")
}

func TestConnectionMethodsHandleOptionalSessionTokens(t *testing.T) {
	testCases := []struct {
		name     string
		secrets  map[string]any
		expected string
	}{
		{name: "missing", secrets: map[string]any{}},
		{name: "json null", secrets: map[string]any{"session_token": nil}},
		{name: "empty", secrets: map[string]any{"session_token": ""}},
		{name: "whitespace", secrets: map[string]any{"session_token": " \t\n"}},
		{name: "legacy null string", secrets: map[string]any{"session_token": "null"}},
		{name: "padded null string", secrets: map[string]any{"session_token": " null\t"}},
		{name: "temporary token", secrets: map[string]any{"session_token": "session-token+/="}, expected: "session-token+/="},
		{name: "padded temporary token", secrets: map[string]any{"session_token": " session-token+/=\n"}, expected: "session-token+/="},
		{name: "null prefix", secrets: map[string]any{"session_token": "null-token"}, expected: "null-token"},
	}
	for _, testCase := range testCases {
		for _, method := range []string{"connection/test", "connection/connect"} {
			t.Run(testCase.name+"/"+method, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					_, hasToken := request.Header["X-Amz-Security-Token"]
					signedToken := strings.Contains(request.Header.Get("Authorization"), "x-amz-security-token")
					expectsToken := testCase.expected != ""
					if request.Header.Get("X-Amz-Security-Token") != testCase.expected || hasToken != expectsToken || signedToken != expectsToken {
						response.WriteHeader(http.StatusForbidden)
						return
					}
					if request.Method != http.MethodHead || request.URL.Path != "/example-bucket/" {
						response.WriteHeader(http.StatusNotFound)
						return
					}
					response.WriteHeader(http.StatusOK)
				}))
				defer server.Close()

				secrets := map[string]any{"secret_key": "secret-key"}
				for key, value := range testCase.secrets {
					secrets[key] = value
				}
				params := map[string]any{
					"provider": map[string]any{"id": pluginID + ".connection", "databaseType": "s3"},
					"connection": map[string]any{
						"id":       "connection-1",
						"database": "example-bucket",
						"username": "access-key",
						"external_config": map[string]any{
							"endpoint":         server.URL,
							"region":           "us-east-1",
							"addressing_style": "path",
						},
						"connection_secrets": secrets,
					},
				}
				config, pluginError := parseConnection(params)
				if pluginError != nil {
					t.Fatal(pluginError.Message)
				}
				if config.sessionToken != testCase.expected {
					t.Errorf("unexpected session token: got %q, want %q", config.sessionToken, testCase.expected)
				}
				plugin := &plugin{connections: map[string]*s3Connection{}}
				if _, pluginError := invokeIntegration(t, plugin, method, params); pluginError != nil {
					t.Fatalf("%s failed: %s", method, pluginError.Message)
				}
				if requests.Load() == 0 {
					t.Fatal("connection did not reach the S3 endpoint")
				}
				if method == "connection/connect" && plugin.connections[config.id] == nil {
					t.Fatal("connection was not registered")
				}
			})
		}
	}
}
