package main

import (
	"strings"
	"testing"
)

func newPresignTestPlugin(t *testing.T) *plugin {
	t.Helper()
	config, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "example-bucket",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint": "https://s3.example.com",
				"region":   "us-east-1",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	connection, pluginError := createConnection(config)
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	return &plugin{connections: map[string]*s3Connection{"connection-1": connection}}
}

func TestPresignObjectReturnsSignedUrl(t *testing.T) {
	instance := newPresignTestPlugin(t)
	result, pluginError := instance.presignObject(map[string]any{
		"connectionId": "connection-1",
		"uri":          "s3://example-bucket/photos/pic%201.png",
		"expires":      float64(3600),
	})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	urlString, _ := result.(map[string]any)["url"].(string)
	if !strings.Contains(urlString, "/photos/pic%201.png") {
		t.Fatalf("expected the object key in the URL, got %q", urlString)
	}
	if !strings.Contains(urlString, "X-Amz-Signature=") {
		t.Fatalf("expected a signed URL, got %q", urlString)
	}
	if !strings.Contains(urlString, "X-Amz-Expires=3600") {
		t.Fatalf("expected the requested expiry, got %q", urlString)
	}
	if expires, _ := result.(map[string]any)["expiresIn"].(int); expires != 3600 {
		t.Fatalf("expected expiresIn 3600, got %#v", result.(map[string]any)["expiresIn"])
	}
}

func TestPresignObjectDefaultsAndCapsExpiry(t *testing.T) {
	instance := newPresignTestPlugin(t)
	result, pluginError := instance.presignObject(map[string]any{"connectionId": "connection-1", "uri": "s3://example-bucket/a.txt"})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if expires, _ := result.(map[string]any)["expiresIn"].(int); expires != defaultShareExpires {
		t.Fatalf("expected default expiry %d, got %#v", defaultShareExpires, result.(map[string]any)["expiresIn"])
	}
	if _, pluginError := instance.presignObject(map[string]any{"connectionId": "connection-1", "uri": "s3://example-bucket/a.txt", "expires": float64(maxShareExpires + 1)}); pluginError == nil {
		t.Fatal("expected expiry beyond 7 days to be rejected")
	}
}

func TestPresignObjectRequiresFileURI(t *testing.T) {
	instance := newPresignTestPlugin(t)
	for _, uri := range []string{"s3://example-bucket/", "s3://example-bucket/photos/"} {
		if _, pluginError := instance.presignObject(map[string]any{"connectionId": "connection-1", "uri": uri}); pluginError == nil {
			t.Fatalf("expected %q to be rejected as a share target", uri)
		}
	}
}
