package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func versionTestConnection(test *testing.T, handler http.Handler) *s3Connection {
	test.Helper()
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("test-access", "test-secret", ""), Region: "us-east-1", BucketLookup: minio.BucketLookupPath, MaxRetries: 1})
	if err != nil {
		test.Fatal(err)
	}
	return &s3Connection{client: client, bucket: "example-bucket"}
}

func TestVersionedWritesPreserveOverwriteProtection(test *testing.T) {
	for _, scenario := range []struct {
		name, status            string
		denied, allow, succeeds bool
	}{
		{"enabled", "Enabled", false, true, true},
		{"suspended", "Suspended", false, true, false},
		{"unversioned", "", false, true, false},
		{"permission denied", "Enabled", true, true, false},
		{"excluded prefixes", "Enabled", false, true, false},
		{"legacy caller", "Enabled", false, false, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			fake := &fakeS3Server{bucket: "example-bucket", objects: map[string]fakeS3Object{"file.txt": newFakeObject([]byte("original"), "text/plain")}}
			connection := versionTestConnection(test, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Query().Has("versioning") {
					if scenario.denied {
						response.WriteHeader(http.StatusForbidden)
						return
					}
					if scenario.name == "excluded prefixes" {
						fmt.Fprint(response, `<VersioningConfiguration><Status>Enabled</Status><ExcludedPrefixes><Prefix>legacy/</Prefix></ExcludedPrefixes></VersioningConfiguration>`)
						return
					}
					fmt.Fprintf(response, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, scenario.status)
					return
				}
				if request.Method == http.MethodPut {
					response.Header().Set("X-Amz-Version-Id", "version-new")
				}
				fake.ServeHTTP(response, request)
			}))
			instance := &plugin{connections: map[string]*s3Connection{"test": connection}}
			values := map[string]any{"connectionId": "test", "uri": "s3://example-bucket/file.txt", "dataBase64": "bmV3", "create": true, "allowNewVersion": scenario.allow}
			result, pluginError := instance.writeObject(values)
			if (pluginError == nil) != scenario.succeeds {
				test.Fatalf("unexpected write outcome: %v", pluginError)
			}
			fake.mutex.Lock()
			data := string(fake.objects["file.txt"].data)
			fake.mutex.Unlock()
			if scenario.succeeds {
				if data != "new" || result.(map[string]any)["versionId"] != "version-new" {
					test.Fatalf("missing write/version result: %s %#v", data, result)
				}
			} else if data != "original" {
				test.Fatal("rejected write changed the object")
			}
			if !scenario.succeeds {
				values["uploadId"] = "stream-test"
				if _, pluginError := instance.openUpload(values); pluginError == nil {
					test.Fatal("streaming upload bypassed overwrite protection")
				}
			}
		})
	}
}

func TestWriteOptionsRejectsUncertainObjectState(test *testing.T) {
	connection := versionTestConnection(test, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) { response.WriteHeader(http.StatusForbidden) }))
	_, pluginError := writeOptions(context.Background(), connection, objectPath{bucket: "example-bucket", key: "file.txt"}, map[string]any{"create": true, "overwrite": true})
	if pluginError == nil || !strings.Contains(pluginError.Message, "object check failed") {
		test.Fatalf("permission error must not be treated as missing object: %v", pluginError)
	}
}

func TestVersionListingExactKeyBasePathAndMarkers(test *testing.T) {
	connection := versionTestConnection(test, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !request.URL.Query().Has("versions") || request.URL.Query().Get("prefix") != "tenant/报告.txt" {
			test.Errorf("incorrect version request: %s", request.URL)
		}
		fmt.Fprint(response, `<ListVersionsResult><Name>example-bucket</Name><IsTruncated>false</IsTruncated><DeleteMarker><Key>tenant/报告.txt</Key><VersionId>deleted</VersionId><IsLatest>true</IsLatest><LastModified>2026-09-17T01:00:00Z</LastModified></DeleteMarker><Version><Key>tenant/报告.txt</Key><VersionId>older</VersionId><IsLatest>false</IsLatest><LastModified>2026-09-16T01:00:00Z</LastModified><Size>12</Size></Version><Version><Key>tenant/报告.txt.bak</Key><VersionId>unrelated</VersionId><Size>1</Size></Version></ListVersionsResult>`)
	}))
	connection.basePath = "tenant"
	connection.readOnly = true
	instance := &plugin{connections: map[string]*s3Connection{"test": connection}}
	result, pluginError := instance.listVersions(map[string]any{"connectionId": "test", "uri": "s3://example-bucket/%E6%8A%A5%E5%91%8A.txt"})
	if pluginError != nil {
		test.Fatal(pluginError)
	}
	versions := result.(map[string]any)["versions"].([]objectVersion)
	if len(versions) != 2 || !versions[0].DeleteMarker || !versions[0].IsLatest || versions[1].VersionID != "older" {
		test.Fatalf("incorrect versions: %#v", versions)
	}
}

func TestVersionListingBoundedAndErrors(test *testing.T) {
	for _, denied := range []bool{false, true} {
		test.Run(fmt.Sprint(denied), func(test *testing.T) {
			connection := versionTestConnection(test, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if denied {
					response.WriteHeader(http.StatusForbidden)
					return
				}
				fmt.Fprint(response, `<ListVersionsResult><Name>example-bucket</Name><IsTruncated>false</IsTruncated>`)
				for index := 0; index <= maxPageSize; index++ {
					fmt.Fprintf(response, `<Version><Key>file.txt</Key><VersionId>v%d</VersionId><Size>1</Size></Version>`, index)
				}
				fmt.Fprint(response, `</ListVersionsResult>`)
			}))
			instance := &plugin{connections: map[string]*s3Connection{"test": connection}}
			result, pluginError := instance.listVersions(map[string]any{"connectionId": "test", "uri": "s3://example-bucket/file.txt"})
			if denied {
				if pluginError == nil {
					test.Fatal("permission error was hidden")
				}
				return
			}
			if pluginError != nil {
				test.Fatal(pluginError)
			}
			response := result.(map[string]any)
			if !response["truncated"].(bool) || len(response["versions"].([]objectVersion)) != maxPageSize {
				test.Fatal("version listing was not bounded")
			}
		})
	}
}
