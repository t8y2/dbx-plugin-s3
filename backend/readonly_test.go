package main

import (
	"strings"
	"testing"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

func TestParseConnectionReadsReadOnlyFlag(t *testing.T) {
	testCases := []struct {
		name  string
		key   string
		value any
		want  bool
	}{
		{name: "absent", key: "", value: nil, want: false},
		{name: "snake_case true", key: "read_only", value: true, want: true},
		{name: "camelCase true", key: "readOnly", value: true, want: true},
		{name: "explicit false", key: "read_only", value: false, want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			connection := map[string]any{
				"id":       "connection-1",
				"database": "example-bucket",
				"username": "access-key",
				"external_config": map[string]any{
					"endpoint":         "http://127.0.0.1:9000",
					"addressing_style": "path",
				},
				"connection_secrets": map[string]any{"secret_key": "secret-key"},
			}
			if testCase.key != "" {
				connection[testCase.key] = testCase.value
			}
			config, pluginError := parseConnection(map[string]any{
				"provider":   map[string]any{"id": pluginID + ".connection", "databaseType": "s3"},
				"connection": connection,
			})
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if config.readOnly != testCase.want {
				t.Errorf("unexpected readOnly: got %v, want %v", config.readOnly, testCase.want)
			}
		})
	}
}

// Mutating methods must reject before any S3 traffic when the connection is
// marked read-only, so the guard tests never need a live endpoint.
func TestMutatingMethodsRejectReadOnlyConnections(t *testing.T) {
	plugin := &plugin{connections: map[string]*s3Connection{
		"connection-1": {bucket: "example-bucket", readOnly: true},
	}}
	base := func(extra map[string]any) map[string]any {
		values := map[string]any{"providerId": filesystemProvider, "connectionId": "connection-1"}
		for key, value := range extra {
			values[key] = value
		}
		return values
	}
	assertRejected := func(name string, pluginError *dbxpluginsdk.PluginError) {
		t.Helper()
		if pluginError == nil {
			t.Fatalf("%s: expected read-only rejection, got success", name)
		}
		if pluginError.Code != -32010 || !strings.Contains(pluginError.Message, "read-only") {
			t.Fatalf("%s: unexpected error: %#v", name, pluginError)
		}
	}

	_, pluginError := plugin.deleteObject(base(map[string]any{"uri": "s3://example-bucket/file.txt"}))
	assertRejected("delete", pluginError)
	_, pluginError = plugin.writeObject(base(map[string]any{"uri": "s3://example-bucket/file.txt", "dataBase64": "aGk=", "create": true}))
	assertRejected("write", pluginError)
	_, pluginError = plugin.createDirectory(base(map[string]any{"uri": "s3://example-bucket/folder/"}))
	assertRejected("createDirectory", pluginError)
	_, pluginError = plugin.renameObject(base(map[string]any{"sourceUri": "s3://example-bucket/a.txt", "targetUri": "s3://example-bucket/b.txt"}))
	assertRejected("rename", pluginError)
	_, pluginError = plugin.openUpload(base(map[string]any{"uploadId": "upload-1", "uri": "s3://example-bucket/file.txt", "create": true}))
	assertRejected("upload/open", pluginError)
}

// Presigning is local signing and stays available on read-only connections.
func TestPresignStaysAvailableForReadOnlyConnections(t *testing.T) {
	config, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":        "connection-1",
			"database":  "example-bucket",
			"username":  "access-key",
			"read_only": true,
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
	if !config.readOnly {
		t.Fatal("expected parsed connection to be read-only")
	}
	connection, pluginError := createConnection(config)
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if !connection.readOnly {
		t.Fatal("expected live connection to carry the read-only flag")
	}
	plugin := &plugin{connections: map[string]*s3Connection{"connection-1": connection}}
	result, pluginError := plugin.presignObject(map[string]any{
		"providerId":   filesystemProvider,
		"connectionId": "connection-1",
		"uri":          "s3://example-bucket/file.txt",
		"expires":      3600,
	})
	if pluginError != nil {
		t.Fatalf("presign should stay available on read-only connections: %#v", pluginError)
	}
	if _, ok := result.(map[string]any)["url"]; !ok {
		t.Fatalf("expected presign result to carry a url: %#v", result)
	}
}
