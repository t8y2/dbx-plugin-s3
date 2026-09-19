package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

func TestParseConnectionUsesDBXConnectionFields(t *testing.T) {
	connection, pluginError := parseConnection(map[string]any{
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
	if connection.bucket != "example-bucket" || connection.accessKey != "access-key" || connection.secretKey != "secret-key" {
		t.Fatalf("unexpected connection credentials: %#v", connection)
	}
}

func TestParseConnectionAllowsEmptyBucketForBucketDiscovery(t *testing.T) {
	connection, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint": "https://s3.example.com",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if connection.bucket != "" {
		t.Fatalf("expected empty bucket, got %q", connection.bucket)
	}
}

func TestValidStreamID(t *testing.T) {
	for _, id := range []string{"stream-1", "preview_2", "a.b"} {
		if !validStreamID(id) {
			t.Errorf("expected stream id %q to be valid", id)
		}
	}
	for _, id := range []string{"", "stream/id", "stream id", strings.Repeat("x", 129)} {
		if validStreamID(id) {
			t.Errorf("expected stream id %q to be invalid", id)
		}
	}
}

func TestVerifyBucketFallsBackToListingWhenHeadBucketFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method == http.MethodGet && request.URL.Query().Get("list-type") == "2" {
			response.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(response, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>example-bucket</Name><KeyCount>0</KeyCount><MaxKeys>1</MaxKeys><IsTruncated>false</IsTruncated></ListBucketResult>`)
			return
		}
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := minio.New(endpoint.Host, &minio.Options{Region: "auto", BucketLookup: minio.BucketLookupPath})
	if err != nil {
		t.Fatal(err)
	}

	pluginError := verifyBucket(&s3Connection{client: client, bucket: "example-bucket"})
	if pluginError != nil {
		t.Fatalf("expected listing fallback to verify the bucket: %s", pluginError.Message)
	}
}

func TestParseConnectionRejectsEndpointPath(t *testing.T) {
	_, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "example-bucket",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint": "https://s3.example.com/base",
				"region":   "us-east-1",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError == nil || pluginError.Code != -32602 {
		t.Fatalf("expected invalid endpoint error, got %#v", pluginError)
	}
}

func TestParseConnectionRejectsEndpointQuery(t *testing.T) {
	_, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "example-bucket",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint": "https://s3.example.com?region=custom",
				"region":   "us-east-1",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError == nil || pluginError.Code != -32602 {
		t.Fatalf("expected invalid endpoint error, got %#v", pluginError)
	}
}

func TestParseConnectionAcceptsTemporaryTokenAndPathStyle(t *testing.T) {
	connection, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "example-bucket",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint":         "http://localhost:9000/",
				"region":           "us-east-1",
				"addressing_style": "path",
			},
			"connection_secrets": map[string]any{
				"secret_key":    "secret-key",
				"session_token": "session-token",
			},
		},
	})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if connection.sessionToken != "session-token" || connection.bucketLookup != minio.BucketLookupPath {
		t.Fatalf("unexpected temporary credential settings: %#v", connection)
	}
}

func TestParseConnectionAcceptsHostProtocolAndBasePath(t *testing.T) {
	connection, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "cogagent",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint":          "s3-cn-bj.ufileos.com",
				"endpoint_protocol": "https",
				"region":            "cn-bj",
				"base_path":         "/cogagent_annotation/data/",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if connection.endpoint != "https://s3-cn-bj.ufileos.com" || connection.basePath != "cogagent_annotation/data" {
		t.Fatalf("unexpected UFile connection settings: %#v", connection)
	}
}

func TestParseConnectionRejectsInvalidBasePath(t *testing.T) {
	_, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "example-bucket",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint":  "https://s3.example.com",
				"region":    "us-east-1",
				"base_path": "tenant/../other",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError == nil || pluginError.Code != -32602 {
		t.Fatalf("expected invalid base path error, got %#v", pluginError)
	}
}

func TestParseConnectionDefaultsRegionAndRootBasePath(t *testing.T) {
	connection, pluginError := parseConnection(map[string]any{
		"connection": map[string]any{
			"id":       "connection-1",
			"database": "example-bucket",
			"username": "access-key",
			"external_config": map[string]any{
				"endpoint":  "http://localhost:9000",
				"base_path": "/",
			},
			"connection_secrets": map[string]any{"secret_key": "secret-key"},
		},
	})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if connection.region != "us-east-1" || connection.basePath != "" {
		t.Fatalf("unexpected region/base path defaults: %#v", connection)
	}
}

func TestProviderGuardsRejectMismatchedProvider(t *testing.T) {
	if pluginError := requireConnectionProvider(map[string]any{
		"provider": map[string]any{"id": "other.connection", "databaseType": "s3"},
	}); pluginError == nil {
		t.Fatal("expected mismatched connection provider to be rejected")
	}
	if pluginError := requireFilesystemProvider(map[string]any{"providerId": "other.files"}); pluginError == nil {
		t.Fatal("expected mismatched filesystem provider to be rejected")
	}
}

func TestRenameRejectsSameOrNestedDirectory(t *testing.T) {
	connection := &s3Connection{bucket: "example-bucket"}
	plugin := &plugin{connections: map[string]*s3Connection{"connection-1": connection}}
	base := map[string]any{
		"providerId":   filesystemProvider,
		"connectionId": "connection-1",
		"sourceUri":    "s3://example-bucket/folder/",
		"targetUri":    "s3://example-bucket/folder/child/",
	}
	if _, pluginError := plugin.renameObject(base); pluginError == nil || pluginError.Code != -32602 {
		t.Fatalf("expected nested directory rename to be rejected, got %#v", pluginError)
	}
	base["targetUri"] = "s3://example-bucket/folder/"
	if _, pluginError := plugin.renameObject(base); pluginError == nil || pluginError.Code != -32602 {
		t.Fatalf("expected same directory rename to be rejected, got %#v", pluginError)
	}
	base["sourceUri"] = "s3://example-bucket/folder/child/"
	base["overwrite"] = true
	if _, pluginError := plugin.renameObject(base); pluginError == nil || pluginError.Code != -32602 {
		t.Fatalf("expected rename over an ancestor directory to be rejected, got %#v", pluginError)
	}
}

func TestParseObjectPathEnforcesConnectedBucket(t *testing.T) {
	connection := &s3Connection{bucket: "example-bucket"}
	root, pluginError := parseObjectPath("s3:/folder/file.txt", connection)
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if root.bucket != "example-bucket" || root.key != "folder/file.txt" {
		t.Fatalf("unexpected root-relative path: %#v", root)
	}
	_, pluginError = parseObjectPath("s3://other-bucket/file.txt", connection)
	if pluginError == nil {
		t.Fatal("expected cross-bucket URI to be rejected")
	}
}

func TestParseObjectPathAcceptsSelectedBucketWhenConnectionHasNoBucket(t *testing.T) {
	path, pluginError := parseObjectPath("s3://selected-bucket/folder/file.txt", &s3Connection{})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if path.bucket != "selected-bucket" || path.key != "folder/file.txt" {
		t.Fatalf("unexpected selected bucket path: %#v", path)
	}
}

func TestObjectURIEscapesObjectKey(t *testing.T) {
	connection := &s3Connection{bucket: "example-bucket"}
	uri := objectURI("example-bucket", "folder/hash#question?mark.txt")
	if uri != "s3://example-bucket/folder/hash%23question%3Fmark.txt" {
		t.Fatalf("unexpected escaped URI: %s", uri)
	}
	path, pluginError := parseObjectPath(uri, connection)
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if path.key != "folder/hash#question?mark.txt" {
		t.Fatalf("unexpected decoded object key: %s", path.key)
	}
}

func TestObjectPathMapsBasePathWithoutLeakingItToDBX(t *testing.T) {
	connection := &s3Connection{bucket: "cogagent", basePath: "cogagent_annotation/data"}
	root := objectPath{bucket: connection.bucket}
	if got := connection.remotePath(root).key; got != "cogagent_annotation/data/" {
		t.Fatalf("unexpected remote root key: %q", got)
	}
	local := objectPath{bucket: connection.bucket, key: "folder/file.txt"}
	if got := connection.remotePath(local).key; got != "cogagent_annotation/data/folder/file.txt" {
		t.Fatalf("unexpected remote object key: %q", got)
	}
	if got, ok := connection.localKey("cogagent_annotation/data/folder/file.txt"); !ok || got != local.key {
		t.Fatalf("unexpected local object key: %q, %v", got, ok)
	}
	if got, ok := connection.localKey("other/folder/file.txt"); ok || got != "" {
		t.Fatalf("unexpected out-of-scope object mapping: %q, %v", got, ok)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	const key = "folder/中文 file.txt"
	if decoded := decodeCursor(encodeCursor(key)); decoded != key {
		t.Fatalf("cursor round trip changed key to %q", decoded)
	}
}

func TestEntryFromObject(t *testing.T) {
	modified := time.Date(2026, time.September, 18, 9, 54, 26, 0, time.FixedZone("UTC+8", 8*60*60))
	file := entryFromObject(minio.ObjectInfo{Key: "folder/file.txt", Size: 12, LastModified: modified}, "example-bucket")
	if file.Name != "file.txt" || file.Kind != "file" || file.URI != "s3://example-bucket/folder/file.txt" || file.Size == nil || *file.Size != 12 {
		t.Fatalf("unexpected file entry: %#v", file)
	}
	if file.ModifiedAt != "2026-09-18T01:54:26Z" {
		t.Fatalf("unexpected modification time: %q", file.ModifiedAt)
	}
	directory := entryFromObject(minio.ObjectInfo{Key: "folder/subfolder/"}, "example-bucket")
	if directory.Name != "subfolder" || directory.Kind != "directory" || directory.Size != nil {
		t.Fatalf("unexpected directory entry: %#v", directory)
	}
}

func TestValidEntryNameRejectsUnrepresentableS3Keys(t *testing.T) {
	for _, name := range []string{"", "   ", "\t", "line\nbreak", "binary\x00name"} {
		if validEntryName(name) {
			t.Fatalf("expected entry name %q to be rejected", name)
		}
	}
	if !validEntryName("object.txt") {
		t.Fatal("expected ordinary object name to be accepted")
	}
}
