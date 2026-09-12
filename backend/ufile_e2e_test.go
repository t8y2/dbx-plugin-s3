package main

import (
	"os"
	"strings"
	"testing"
)

func TestUFileReadOnlyE2E(t *testing.T) {
	if os.Getenv("DBX_S3_E2E") != "1" {
		t.Skip("set DBX_S3_E2E=1 to run the opt-in UFile read-only check")
	}
	endpoint := os.Getenv("DBX_S3_ENDPOINT")
	protocol := os.Getenv("DBX_S3_ENDPOINT_PROTOCOL")
	bucket := os.Getenv("DBX_S3_BUCKET")
	region := os.Getenv("DBX_S3_REGION")
	basePath := os.Getenv("DBX_S3_BASE_PATH")
	accessKey := os.Getenv("DBX_S3_ACCESS_KEY")
	secretKey := os.Getenv("DBX_S3_SECRET_KEY")
	for name, value := range map[string]string{
		"DBX_S3_ENDPOINT": endpoint, "DBX_S3_ENDPOINT_PROTOCOL": protocol,
		"DBX_S3_BUCKET": bucket, "DBX_S3_REGION": region,
		"DBX_S3_BASE_PATH": basePath, "DBX_S3_ACCESS_KEY": accessKey,
		"DBX_S3_SECRET_KEY": secretKey,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("missing %s for UFile E2E", name)
		}
	}

	plugin := &plugin{connections: map[string]*s3Connection{}}
	connection := map[string]any{
		"id":       "ufile-e2e",
		"database": bucket,
		"username": accessKey,
		"external_config": map[string]any{
			"endpoint":          endpoint,
			"endpoint_protocol": protocol,
			"region":            region,
			"base_path":         basePath,
			"addressing_style":  os.Getenv("DBX_S3_ADDRESSING_STYLE"),
		},
		"connection_secrets": map[string]any{"secret_key": secretKey},
	}
	params := map[string]any{
		"provider":   map[string]any{"id": pluginID + ".connection", "databaseType": "s3"},
		"connection": connection,
	}
	if _, pluginError := invokeIntegration(t, plugin, "connection/test", params); pluginError != nil {
		t.Fatalf("UFile connection test failed: %s", pluginError.Message)
	}
	if _, pluginError := invokeIntegration(t, plugin, "connection/connect", params); pluginError != nil {
		t.Fatalf("UFile connection failed: %s", pluginError.Message)
	}
	defer func() {
		_, _ = invokeIntegration(t, plugin, "connection/disconnect", params)
	}()

	base := map[string]any{"providerId": filesystemProvider, "connectionId": "ufile-e2e"}
	root, pluginError := invokeIntegration(t, plugin, "filesystem/list", mergeIntegration(base, map[string]any{"uri": "s3:/", "limit": 50}))
	if pluginError != nil {
		t.Fatalf("UFile root listing failed: %s", pluginError.Message)
	}
	entries, ok := root["entries"].([]filesystemEntry)
	if !ok {
		t.Fatal("UFile root listing returned an invalid entry payload")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.URI, "s3://"+bucket+"/") {
			t.Fatalf("UFile listing returned a non-root-relative URI")
		}
	}

	var firstFile *filesystemEntry
	for index := range entries {
		if entries[index].Kind == "file" {
			firstFile = &entries[index]
			break
		}
	}
	if firstFile == nil {
		for _, entry := range entries {
			if entry.Kind != "directory" {
				continue
			}
			listing, listError := invokeIntegration(t, plugin, "filesystem/list", mergeIntegration(base, map[string]any{"uri": entry.URI, "limit": 50}))
			if listError != nil {
				t.Fatalf("UFile directory listing failed: %s", listError.Message)
			}
			nested, nestedOK := listing["entries"].([]filesystemEntry)
			if !nestedOK {
				t.Fatal("UFile directory listing returned an invalid entry payload")
			}
			for nestedIndex := range nested {
				if nested[nestedIndex].Kind == "file" {
					firstFile = &nested[nestedIndex]
					break
				}
			}
			if firstFile != nil {
				break
			}
		}
	}
	if firstFile != nil {
		if _, readError := invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": firstFile.URI, "maxBytes": 64})); readError != nil {
			t.Fatalf("UFile file read failed: %s", readError.Message)
		}
	}
	t.Logf("UFile read-only E2E passed: %d root entries, readable file found=%t", len(entries), firstFile != nil)
}
