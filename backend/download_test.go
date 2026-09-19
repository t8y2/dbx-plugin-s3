package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestPullDownloadsExceedLegacyLimitsWithBoundedChunks(test *testing.T) {
	const size = int64(300 * 1024 * 1024)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Length", fmt.Sprint(size))
		response.Header().Set("Content-Type", "application/octet-stream")
		response.Header().Set("Last-Modified", "Fri, 18 Sep 2026 01:54:26 GMT")
		response.Header().Set("ETag", `"00000000000000000000000000000000"`)
		if request.Method != http.MethodHead {
			_, _ = io.CopyN(response, zeroReader{}, size)
		}
	}))
	defer server.Close()
	for _, archive := range []bool{false, true} {
		test.Run(fmt.Sprintf("archive=%t", archive), func(test *testing.T) {
			plugin := &plugin{connections: map[string]*s3Connection{"test": newArchiveTestConnection(test, server.URL, "")}}
			params := map[string]any{"providerId": filesystemProvider, "connectionId": "test", "downloadId": "large-test", "uri": "s3://example-bucket/large.bin", "archive": archive, "uris": []any{"s3://example-bucket/large.bin"}}
			defer plugin.closeDownload(params)
			result, pluginError := plugin.openDownload(params)
			if pluginError != nil {
				test.Fatal(pluginError.Message)
			}
			if result.(map[string]any)["size"] != size {
				test.Fatalf("unexpected size: %v", result)
			}
			total := int64(0)
			var compressed bytes.Buffer
			for {
				result, pluginError = plugin.readDownload(params)
				if pluginError != nil {
					test.Fatal(pluginError.Message)
				}
				chunk := result.(map[string]any)
				data, err := base64.StdEncoding.DecodeString(chunk["dataBase64"].(string))
				if err != nil || len(data) > downloadChunkBytes {
					test.Fatalf("invalid bounded chunk: %d, %v", len(data), err)
				}
				total += int64(len(data))
				if archive {
					compressed.Write(data)
				}
				if chunk["done"] == true {
					break
				}
			}
			if archive {
				reader, err := zip.NewReader(bytes.NewReader(compressed.Bytes()), int64(compressed.Len()))
				if err != nil || len(reader.File) != 1 {
					test.Fatalf("invalid ZIP: %v", err)
				}
				member, err := reader.File[0].Open()
				if err != nil {
					test.Fatal(err)
				}
				total, err = io.Copy(io.Discard, member)
				_ = member.Close()
				if err != nil {
					test.Fatal(err)
				}
			}
			if total != size {
				test.Fatalf("download truncated: got %d, expected %d", total, size)
			}
			if _, pluginError := plugin.closeDownload(params); pluginError != nil {
				test.Fatal(pluginError.Message)
			}
			if len(plugin.downloads) != 0 {
				test.Fatal("download session leaked")
			}
		})
	}
}

func TestPullDownloadRejectsCrossConnectionReadAndClose(test *testing.T) {
	fake, server := newFakeS3Server("example-bucket")
	defer server.Close()
	seedArchiveObjects(fake, "")
	plugin := &plugin{connections: map[string]*s3Connection{"test": newArchiveTestConnection(test, server.URL, "")}}
	params := map[string]any{"connectionId": "test", "downloadId": "owned", "uri": "s3://example-bucket/root.txt"}
	if _, pluginError := plugin.openDownload(params); pluginError != nil {
		test.Fatal(pluginError.Message)
	}
	defer plugin.closeDownload(params)
	other := map[string]any{"connectionId": "other", "downloadId": "owned"}
	if _, pluginError := plugin.readDownload(other); pluginError == nil {
		test.Fatal("read allowed from another connection")
	}
	if _, pluginError := plugin.closeDownload(other); pluginError == nil {
		test.Fatal("close allowed from another connection")
	}
	if _, pluginError := plugin.openDownload(params); pluginError == nil {
		test.Fatal("duplicate session allowed")
	}
	plugin.closeConnectionDownloads("test")
	if len(plugin.downloads) != 0 {
		test.Fatal("disconnect did not release download")
	}
}
