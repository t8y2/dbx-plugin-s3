package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

func newArchiveTestConnection(t *testing.T, serverURL string, basePath string) *s3Connection {
	t.Helper()
	endpoint, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := minio.New(endpoint.Host, &minio.Options{
		Creds:        credentials.NewStaticV4("access-key", "secret-key", ""),
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
		MaxRetries:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &s3Connection{client: client, bucket: "example-bucket", region: "us-east-1", endpoint: serverURL, basePath: basePath}
}

func seedArchiveObjects(fake *fakeS3Server, basePath string) {
	prefix := ""
	if basePath != "" {
		prefix = basePath + "/"
		fake.objects["outside.txt"] = newFakeObject([]byte("outside"), "text/plain")
	}
	fake.objects[prefix+"root.txt"] = newFakeObject([]byte("root"), "text/plain")
	fake.objects[prefix+"folder/"] = newFakeObject(nil, "application/x-directory")
	fake.objects[prefix+"folder/nested.txt"] = newFakeObject([]byte("nested"), "text/plain")
	fake.objects[prefix+"folder/sub/deep.txt"] = newFakeObject([]byte("deep"), "text/plain")
}

func memberNames(members []archiveMember) []string {
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.name)
	}
	return names
}

func TestPlanArchiveBuildsMembersForFilesFoldersAndBuckets(t *testing.T) {
	for _, basePath := range []string{"", "tenant/data"} {
		t.Run("basePath="+basePath, func(t *testing.T) {
			fake, server := newFakeS3Server("example-bucket")
			defer server.Close()
			seedArchiveObjects(fake, basePath)
			connection := newArchiveTestConnection(t, server.URL, basePath)
			context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			file, total, pluginError := planArchive(context, connection, []any{"s3://example-bucket/root.txt"})
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if len(file) != 1 || file[0].name != "root.txt" || file[0].bucket != "example-bucket" || total != 4 {
				t.Fatalf("unexpected file members: %#v (total %d)", file, total)
			}
			remoteKey := "root.txt"
			if basePath != "" {
				remoteKey = basePath + "/root.txt"
			}
			if file[0].key != remoteKey {
				t.Fatalf("unexpected remote key: %q", file[0].key)
			}

			folder, total, pluginError := planArchive(context, connection, []any{"s3://example-bucket/folder/"})
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if !reflect.DeepEqual(memberNames(folder), []string{"folder/nested.txt", "folder/sub/deep.txt"}) || total != 10 {
				t.Fatalf("unexpected folder members: %#v (total %d)", memberNames(folder), total)
			}

			bucket, total, pluginError := planArchive(context, connection, []any{"s3://example-bucket"})
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if !reflect.DeepEqual(memberNames(bucket), []string{"example-bucket/folder/nested.txt", "example-bucket/folder/sub/deep.txt", "example-bucket/root.txt"}) || total != 14 {
				t.Fatalf("unexpected bucket members: %#v (total %d)", memberNames(bucket), total)
			}

			mixed, total, pluginError := planArchive(context, connection, []any{"s3://example-bucket/root.txt", "s3://example-bucket/folder/"})
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if len(mixed) != 3 || total != 14 {
				t.Fatalf("unexpected mixed members: %#v (total %d)", memberNames(mixed), total)
			}

			// With a connected bucket, "s3:/" resolves to that bucket's root.
			wholeBucket, _, pluginError := planArchive(context, connection, []any{"s3:/"})
			if pluginError != nil {
				t.Fatal(pluginError.Message)
			}
			if !reflect.DeepEqual(memberNames(wholeBucket), memberNames(bucket)) {
				t.Fatalf("unexpected connected-root members: %#v", memberNames(wholeBucket))
			}
			if _, _, pluginError := planArchive(context, &s3Connection{client: connection.client, region: connection.region, endpoint: connection.endpoint, basePath: basePath}, []any{"s3:/"}); pluginError == nil || !strings.Contains(pluginError.Message, "bucket URI") {
				t.Fatalf("expected bucket URI error, got %#v", pluginError)
			}
			if _, _, pluginError := planArchive(context, connection, []any{"s3://example-bucket/missing.txt"}); pluginError == nil || !strings.Contains(pluginError.Message, "stat failed") {
				t.Fatalf("expected stat failure, got %#v", pluginError)
			}
		})
	}
}

func TestPlanArchiveEnforcesSizeAndCountLimits(t *testing.T) {
	fake, server := newFakeS3Server("example-bucket")
	defer server.Close()
	seedArchiveObjects(fake, "")
	connection := newArchiveTestConnection(t, server.URL, "")
	context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	originalSizeLimit, originalCountLimit := maxArchiveBytes, maxArchiveMembers
	t.Cleanup(func() { maxArchiveBytes, maxArchiveMembers = originalSizeLimit, originalCountLimit })

	maxArchiveBytes = 8
	if _, _, pluginError := planArchive(context, connection, []any{"s3://example-bucket"}); pluginError == nil || !strings.Contains(pluginError.Message, "size limit") {
		t.Fatalf("expected size limit error, got %#v", pluginError)
	}
	maxArchiveBytes = originalSizeLimit

	maxArchiveMembers = 2
	if _, _, pluginError := planArchive(context, connection, []any{"s3://example-bucket"}); pluginError == nil || !strings.Contains(pluginError.Message, "file count limit") {
		t.Fatalf("expected file count limit error, got %#v", pluginError)
	}
}

func TestWriteArchiveStreamsValidZip(t *testing.T) {
	fake, server := newFakeS3Server("example-bucket")
	defer server.Close()
	seedArchiveObjects(fake, "")
	connection := newArchiveTestConnection(t, server.URL, "")
	context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	members, _, pluginError := planArchive(context, connection, []any{"s3://example-bucket/folder/", "s3://example-bucket/root.txt"})
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	reader, writer := io.Pipe()
	go (&plugin{}).writeArchive(context, connection, members, writer)
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive is not a valid zip: %v", err)
	}
	contents := map[string]string{}
	for _, file := range archive.File {
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(entry)
		_ = entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name] = string(body)
	}
	expected := map[string]string{"folder/nested.txt": "nested", "folder/sub/deep.txt": "deep", "root.txt": "root"}
	if !reflect.DeepEqual(contents, expected) {
		t.Fatalf("unexpected archive contents: %#v", contents)
	}
}

func TestOpenArchiveValidatesRequest(t *testing.T) {
	plugin := &plugin{connections: map[string]*s3Connection{}, streams: map[string]*s3Stream{}}
	base := map[string]any{"providerId": filesystemProvider, "connectionId": "connection-1"}
	if _, pluginError := plugin.openArchive(map[string]any{"providerId": filesystemProvider, "connectionId": "connection-1", "streamId": "stream 1", "uris": []any{"s3://example-bucket"}}, &dbxpluginsdk.Emitter{}); pluginError == nil || !strings.Contains(pluginError.Message, "stream id") {
		t.Fatalf("expected stream id error, got %#v", pluginError)
	}
	if _, pluginError := plugin.openArchive(mergeIntegration(base, map[string]any{"streamId": "stream-1"}), &dbxpluginsdk.Emitter{}); pluginError == nil || !strings.Contains(pluginError.Message, "not active") {
		t.Fatalf("expected inactive connection error, got %#v", pluginError)
	}
	_, server := newFakeS3Server("example-bucket")
	defer server.Close()
	plugin.connections["connection-1"] = newArchiveTestConnection(t, server.URL, "")
	if _, pluginError := plugin.openArchive(mergeIntegration(base, map[string]any{"streamId": "stream-1"}), &dbxpluginsdk.Emitter{}); pluginError == nil || !strings.Contains(pluginError.Message, "at least one URI") {
		t.Fatalf("expected uris error, got %#v", pluginError)
	}
}
