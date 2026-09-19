package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestConnectionDualstackIsOptIn(test *testing.T) {
	for _, testCase := range []struct {
		name       string
		endpoint   string
		addressing string
		dualstack  any
		host       string
	}{
		{name: "AWS default virtual", endpoint: "s3.us-west-2.amazonaws.com", addressing: "virtual", host: "example-bucket.s3.us-west-2.amazonaws.com"},
		{name: "AWS default path", endpoint: "s3.us-west-2.amazonaws.com", addressing: "path", host: "s3.us-west-2.amazonaws.com"},
		{name: "AWS explicit false", endpoint: "s3.us-west-2.amazonaws.com", dualstack: false, host: "example-bucket.s3.us-west-2.amazonaws.com"},
		{name: "AWS enabled", endpoint: "s3.us-west-2.amazonaws.com", dualstack: true, host: "example-bucket.s3.dualstack.us-west-2.amazonaws.com"},
		{name: "AWS enabled string", endpoint: "s3.us-west-2.amazonaws.com", dualstack: "true", host: "example-bucket.s3.dualstack.us-west-2.amazonaws.com"},
		{name: "custom default", endpoint: "storage.example.com", addressing: "path", host: "storage.example.com"},
		{name: "custom enabled", endpoint: "storage.example.com", addressing: "path", dualstack: true, host: "storage.example.com"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			config, pluginError := parseConnection(map[string]any{"connection": map[string]any{
				"id": "test", "username": "access-key", "connection_secrets": map[string]any{"secret_key": "secret-key"},
				"external_config": map[string]any{"endpoint": testCase.endpoint, "region": "us-west-2", "addressing_style": testCase.addressing, "aws_dualstack": testCase.dualstack},
			}})
			if pluginError != nil {
				test.Fatal(pluginError.Message)
			}
			connection, pluginError := createConnection(config)
			if pluginError != nil {
				test.Fatal(pluginError.Message)
			}
			presigned, err := connection.client.PresignedGetObject(context.Background(), "example-bucket", "report.txt", time.Hour, nil)
			if err != nil {
				test.Fatal(err)
			}
			if presigned.Host != testCase.host {
				test.Fatalf("unexpected endpoint: got %s, want %s", presigned.Host, testCase.host)
			}
		})
	}
}

type connectionTestTransport func(*http.Request) (*http.Response, error)

func (transport connectionTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestConnectionVerificationDeadlinePrecedesHostTimeout(test *testing.T) {
	for _, bucket := range []string{"", "example-bucket"} {
		test.Run("bucket="+bucket, func(test *testing.T) {
			requests := 0
			client, err := minio.New("s3.us-west-2.amazonaws.com", &minio.Options{
				Creds: credentials.NewStaticV4("access-key", "secret-key", ""), Region: "us-west-2", Secure: true, MaxRetries: 1,
				Transport: connectionTestTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					deadline, present := request.Context().Deadline()
					if !present || time.Until(deadline) > 20*time.Second || time.Until(deadline) <= 0 {
						test.Errorf("expected a positive deadline of at most 20 seconds, got %v", deadline)
					}
					return nil, context.DeadlineExceeded
				}),
			})
			if err != nil {
				test.Fatal(err)
			}
			client.SetS3EnableDualstack(false)
			pluginError := verifyBucket(&s3Connection{client: client, bucket: bucket})
			if requests == 0 || pluginError == nil || !strings.Contains(pluginError.Message, "s3.us-west-2.amazonaws.com") || !strings.Contains(pluginError.Message, "context deadline exceeded") {
				test.Fatalf("expected the original endpoint and timeout error, got %v", pluginError)
			}
		})
	}
}

func TestManifestDualstackDefaultsToDisabled(test *testing.T) {
	data, err := os.ReadFile("../manifest.json")
	if err != nil {
		test.Fatal(err)
	}
	var manifest struct {
		Contributions []struct {
			Fields []struct {
				Key     string `json:"key"`
				Type    string `json:"type"`
				Binding string `json:"binding"`
				Default any    `json:"default"`
			} `json:"fields"`
		} `json:"contributions"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		test.Fatal(err)
	}
	for _, contribution := range manifest.Contributions {
		for _, field := range contribution.Fields {
			if field.Key == "aws_dualstack" {
				if field.Type != "boolean" || field.Binding != "config" || field.Default != false {
					test.Fatalf("unexpected dualstack field: %+v", field)
				}
				return
			}
		}
	}
	test.Fatal("missing dualstack field")
}

func TestManifestOptionalFieldsDefaultToEmptyString(t *testing.T) {
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
	requiredDefaults := map[string]string{"session_token": "", "bucket": ""}
	for _, contribution := range manifest.Contributions {
		if contribution.ID != pluginID+".connection" {
			continue
		}
		for _, field := range contribution.Fields {
			if expected, tracked := requiredDefaults[field.Key]; tracked {
				value, ok := field.Default.(string)
				if !ok || value != expected || field.Required {
					t.Fatalf("%s must be optional with an explicit empty-string default", field.Key)
				}
				delete(requiredDefaults, field.Key)
			}
		}
	}
	if len(requiredDefaults) > 0 {
		t.Fatalf("missing optional connection fields with empty defaults: %v", requiredDefaults)
	}
}

func TestParseConnectionTreatsNullBucketAsEmpty(t *testing.T) {
	testCases := []struct {
		name     string
		database any
		expected string
	}{
		{name: "missing", database: nil, expected: ""},
		{name: "empty", database: "", expected: ""},
		{name: "legacy null string", database: "null", expected: ""},
		{name: "padded null string", database: " null\t", expected: ""},
		{name: "real bucket", database: "example-bucket", expected: "example-bucket"},
		{name: "null prefix bucket", database: "null-bucket", expected: "null-bucket"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			params := map[string]any{
				"provider": map[string]any{"id": pluginID + ".connection", "databaseType": "s3"},
				"connection": map[string]any{
					"id":       "connection-1",
					"database": testCase.database,
					"username": "access-key",
					"external_config": map[string]any{
						"endpoint":         "http://127.0.0.1:9000",
						"addressing_style": "path",
					},
					"connection_secrets": map[string]any{"secret_key": "secret-key"},
				},
			}
			config, pluginError := parseConnection(params)
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if config.bucket != testCase.expected {
				t.Errorf("unexpected bucket: got %q, want %q", config.bucket, testCase.expected)
			}
		})
	}
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
