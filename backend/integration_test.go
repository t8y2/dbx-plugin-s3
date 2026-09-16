package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type fakeS3Object struct {
	data        []byte
	contentType string
	etag        string
	modified    time.Time
}

type fakeS3Server struct {
	mutex   sync.Mutex
	bucket  string
	objects map[string]fakeS3Object
}

type fakeListResult struct {
	XMLName      xml.Name           `xml:"ListBucketResult"`
	Name         string             `xml:"Name"`
	Prefix       string             `xml:"Prefix"`
	KeyCount     int                `xml:"KeyCount"`
	MaxKeys      int                `xml:"MaxKeys"`
	IsTruncated  bool               `xml:"IsTruncated"`
	EncodingType string             `xml:"EncodingType,omitempty"`
	Contents     []fakeListObject   `xml:"Contents,omitempty"`
	CommonPrefix []fakeCommonPrefix `xml:"CommonPrefixes,omitempty"`
}

type fakeListObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type fakeCommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type fakeCopyResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

func newFakeS3Server(bucket string) (*fakeS3Server, *httptest.Server) {
	fake := &fakeS3Server{bucket: bucket, objects: map[string]fakeS3Object{}}
	return fake, httptest.NewServer(fake)
}

func (fake *fakeS3Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	bucket, key, ok := splitFakeS3Path(request.URL.Path)
	if !ok || bucket != fake.bucket {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	if request.Method == http.MethodGet && request.URL.Query().Get("list-type") == "2" {
		fake.list(response, request)
		return
	}

	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	switch request.Method {
	case http.MethodHead:
		if key == "" {
			response.WriteHeader(http.StatusOK)
			return
		}
		object, exists := fake.objects[key]
		if !exists {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		writeObjectHeaders(response, object)
		response.WriteHeader(http.StatusOK)
	case http.MethodGet:
		object, exists := fake.objects[key]
		if !exists {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		writeObjectHeaders(response, object)
		_, _ = response.Write(object.data)
	case http.MethodPut:
		if source := request.Header.Get("X-Amz-Copy-Source"); source != "" {
			fake.copyObject(response, request, source, key)
			return
		}
		if request.Header.Get("If-None-Match") == "*" {
			if _, exists := fake.objects[key]; exists {
				response.WriteHeader(http.StatusPreconditionFailed)
				return
			}
		}
		if !fake.matchesETag(request, key) {
			response.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		data, err := readFakeBody(request)
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		fake.objects[key] = newFakeObject(data, request.Header.Get("Content-Type"))
		response.Header().Set("ETag", `"`+fake.objects[key].etag+`"`)
		response.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		delete(fake.objects, key)
		response.WriteHeader(http.StatusNoContent)
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func readFakeBody(request *http.Request) ([]byte, error) {
	data, err := io.ReadAll(request.Body)
	if err != nil || !strings.HasPrefix(request.Header.Get("X-Amz-Content-Sha256"), "STREAMING-AWS4-HMAC-SHA256-PAYLOAD") {
		return data, err
	}
	decoded := make([]byte, 0, len(data))
	for len(data) > 0 {
		separator := bytes.Index(data, []byte("\r\n"))
		if separator < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		sizeText := strings.SplitN(string(data[:separator]), ";", 2)[0]
		size, parseErr := strconv.ParseInt(sizeText, 16, 64)
		if parseErr != nil || size < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		data = data[separator+2:]
		if size == 0 {
			return decoded, nil
		}
		if int64(len(data)) < size+2 {
			return nil, io.ErrUnexpectedEOF
		}
		decoded = append(decoded, data[:size]...)
		data = data[size+2:]
	}
	return decoded, io.ErrUnexpectedEOF
}

func (fake *fakeS3Server) list(response http.ResponseWriter, request *http.Request) {
	prefix := request.URL.Query().Get("prefix")
	delimiter := request.URL.Query().Get("delimiter")
	startAfter := request.URL.Query().Get("start-after")
	maxKeys, _ := strconv.Atoi(request.URL.Query().Get("max-keys"))
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	fake.mutex.Lock()
	keys := make([]string, 0, len(fake.objects))
	for key := range fake.objects {
		if strings.HasPrefix(key, prefix) && key > startAfter {
			keys = append(keys, key)
		}
	}
	fake.mutex.Unlock()
	sort.Strings(keys)
	result := fakeListResult{Name: fake.bucket, Prefix: prefix, MaxKeys: maxKeys, EncodingType: "url"}
	seenPrefixes := map[string]struct{}{}
	for _, key := range keys {
		remainder := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if separator := strings.Index(remainder, delimiter); separator >= 0 {
				common := prefix + remainder[:separator+len(delimiter)]
				if common <= startAfter {
					continue
				}
				if _, seen := seenPrefixes[common]; seen {
					continue
				}
				seenPrefixes[common] = struct{}{}
				result.CommonPrefix = append(result.CommonPrefix, fakeCommonPrefix{Prefix: common})
				continue
			}
		}
		fake.mutex.Lock()
		object := fake.objects[key]
		fake.mutex.Unlock()
		result.Contents = append(result.Contents, fakeListObject{
			Key:          url.PathEscape(key),
			LastModified: object.modified.UTC().Format(time.RFC3339),
			ETag:         `"` + object.etag + `"`,
			Size:         int64(len(object.data)),
			StorageClass: "STANDARD",
		})
		if len(result.Contents)+len(result.CommonPrefix) >= maxKeys {
			break
		}
	}
	result.KeyCount = len(result.Contents) + len(result.CommonPrefix)
	result.IsTruncated = false
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(response).Encode(result)
}

func (fake *fakeS3Server) copyObject(response http.ResponseWriter, request *http.Request, source, target string) {
	decodedSource, err := url.PathUnescape(source)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	source = strings.TrimPrefix(decodedSource, "/")
	sourceParts := strings.SplitN(source, "/", 2)
	if len(sourceParts) != 2 || sourceParts[0] != fake.bucket {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	object, exists := fake.objects[sourceParts[1]]
	if !exists || !fake.matchesETag(request, target) {
		response.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	fake.objects[target] = object
	response.Header().Set("ETag", `"`+object.etag+`"`)
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(response).Encode(fakeCopyResult{LastModified: object.modified.UTC().Format(time.RFC3339), ETag: `"` + object.etag + `"`})
}

func (fake *fakeS3Server) matchesETag(request *http.Request, key string) bool {
	value := request.Header.Get("If-Match")
	if value == "" {
		return true
	}
	object, exists := fake.objects[key]
	return exists && strings.Trim(value, `"`) == object.etag
}

func splitFakeS3Path(rawPath string) (string, string, bool) {
	path := strings.TrimPrefix(rawPath, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	key, err := url.PathUnescape(parts[1])
	return parts[0], key, err == nil
}

func newFakeObject(data []byte, contentType string) fakeS3Object {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	digest := md5.Sum(data)
	return fakeS3Object{data: append([]byte(nil), data...), contentType: contentType, etag: hex.EncodeToString(digest[:]), modified: time.Now().UTC()}
}

func writeObjectHeaders(response http.ResponseWriter, object fakeS3Object) {
	response.Header().Set("Content-Type", object.contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(object.data)))
	response.Header().Set("ETag", `"`+object.etag+`"`)
	response.Header().Set("Last-Modified", object.modified.UTC().Format(http.TimeFormat))
}

func invokeIntegration(t *testing.T, plugin *plugin, method string, values map[string]any) (map[string]any, *dbxpluginsdk.PluginError) {
	t.Helper()
	params, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	result, pluginError := plugin.Handle(dbxpluginsdk.RequestContext{}, method, params, nil)
	if pluginError != nil {
		return nil, pluginError
	}
	value, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected object result for %s, got %#v", method, result)
	}
	return value, nil
}

func TestFilesystemLifecycleAgainstS3HTTPContract(t *testing.T) {
	for _, basePath := range []string{"", "tenant/data"} {
		t.Run("basePath="+basePath, func(t *testing.T) {
			testFilesystemLifecycle(t, basePath)
		})
	}
}

func testFilesystemLifecycle(t *testing.T, basePath string) {
	t.Helper()
	const bucket = "example-bucket"
	fake, server := newFakeS3Server(bucket)
	defer server.Close()
	if basePath != "" {
		fake.objects["outside.txt"] = newFakeObject([]byte("untouched"), "text/plain")
		fake.objects[basePath+"-other/outside.txt"] = newFakeObject([]byte("untouched"), "text/plain")
	}
	endpoint, err := url.Parse(server.URL)
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
	plugin := &plugin{connections: map[string]*s3Connection{}}
	connection := map[string]any{
		"id":       "connection-1",
		"database": bucket,
		"username": "access-key",
		"external_config": map[string]any{
			"endpoint":         server.URL,
			"region":           "us-east-1",
			"addressing_style": "path",
			"base_path":        basePath,
		},
		"connection_secrets": map[string]any{"secret_key": "secret-key"},
	}
	connectionParams := map[string]any{
		"provider":   map[string]any{"id": pluginID + ".connection", "databaseType": "s3"},
		"connection": connection,
	}
	if _, pluginError := invokeIntegration(t, plugin, "connection/connect", connectionParams); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if len(plugin.connections) != 1 {
		t.Fatal("connection was not registered")
	}
	plugin.connections["connection-1"].client = client
	base := map[string]any{"providerId": filesystemProvider, "connectionId": "connection-1"}
	if _, pluginError := invokeIntegration(t, plugin, "filesystem/write", mergeIntegration(base, map[string]any{
		"uri": "s3://example-bucket/missing.txt", "dataBase64": "bWlzc2luZw==", "create": false,
	})); pluginError == nil {
		t.Fatal("expected a write without create permission to fail")
	}

	result, pluginError := invokeIntegration(t, plugin, "filesystem/write", mergeIntegration(base, map[string]any{
		"uri":         "s3://example-bucket/root.txt",
		"dataBase64":  "aGVsbG8=",
		"contentType": "text/plain",
		"create":      true,
		"overwrite":   false,
	}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if result["success"] != true {
		t.Fatalf("unexpected write result: %#v", result)
	}

	read, pluginError := invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/root.txt", "maxBytes": 64}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if read["dataBase64"] != "aGVsbG8=" || read["truncated"] != false {
		t.Fatalf("unexpected read result: %#v", read)
	}
	etag, ok := read["etag"].(string)
	if !ok || etag == "" {
		t.Fatalf("missing read etag: %#v", read)
	}

	if _, pluginError = invokeIntegration(t, plugin, "filesystem/write", mergeIntegration(base, map[string]any{
		"uri": "s3://example-bucket/root.txt", "dataBase64": "d3Jvbmc=", "create": false, "overwrite": true, "etag": "wrong",
	})); pluginError == nil {
		t.Fatal("expected stale etag to fail")
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/write", mergeIntegration(base, map[string]any{
		"uri": "s3://example-bucket/root.txt", "dataBase64": "d29ybGQ=", "create": false, "overwrite": true, "etag": etag,
	})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	truncated, pluginError := invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/root.txt", "maxBytes": 2}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if truncated["dataBase64"] != "d28=" || truncated["truncated"] != true {
		t.Fatalf("unexpected truncated read result: %#v", truncated)
	}

	if _, pluginError = invokeIntegration(t, plugin, "filesystem/createDirectory", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/folder/"})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/write", mergeIntegration(base, map[string]any{
		"uri": "s3://example-bucket/folder/nested.txt", "dataBase64": "bmVzdGVk", "create": true,
	})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}

	list, pluginError := invokeIntegration(t, plugin, "filesystem/list", mergeIntegration(base, map[string]any{"uri": "s3:/", "limit": 20}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if !containsEntry(list, "folder", "directory") || !containsEntry(list, "root.txt", "file") {
		t.Fatalf("unexpected root listing: %#v", list)
	}
	if len(list["entries"].([]filesystemEntry)) != 2 {
		t.Fatal("listing exposed objects outside the configured base path")
	}
	for _, entry := range list["entries"].([]filesystemEntry) {
		if entry.URI != objectURI(bucket, "root.txt") && entry.URI != objectURI(bucket, "folder/") {
			t.Fatalf("listing did not return a root-relative URI: %q", entry.URI)
		}
	}
	firstPage, pluginError := invokeIntegration(t, plugin, "filesystem/list", mergeIntegration(base, map[string]any{"uri": "s3:/", "limit": 1}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	nextCursor, ok := firstPage["nextCursor"].(string)
	if !ok || nextCursor == "" {
		t.Fatalf("missing pagination cursor: %#v", firstPage)
	}
	secondPage, pluginError := invokeIntegration(t, plugin, "filesystem/list", mergeIntegration(base, map[string]any{"uri": "s3:/", "limit": 1, "cursor": nextCursor}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if len(secondPage["entries"].([]filesystemEntry)) != 1 {
		t.Fatalf("unexpected second page: %#v", secondPage)
	}

	directories, pluginError := invokeIntegration(t, plugin, "filesystem/list", mergeIntegration(base, map[string]any{"uri": "s3:/", "limit": 20, "directoriesOnly": true}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	directoryEntries := directories["entries"].([]filesystemEntry)
	if len(directoryEntries) != 1 || directoryEntries[0].Name != "folder" || directoryEntries[0].Kind != "directory" {
		t.Fatalf("unexpected directories-only listing: %#v", directories)
	}
	if _, hasCursor := directories["nextCursor"]; hasCursor {
		t.Fatal("directories-only listing must not paginate when the prefix is drained")
	}

	if _, pluginError = invokeIntegration(t, plugin, "filesystem/createDirectory", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/existing/"})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/write", mergeIntegration(base, map[string]any{
		"uri": "s3://example-bucket/existing/old.txt", "dataBase64": "b2xk", "create": true,
	})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/rename", mergeIntegration(base, map[string]any{
		"sourceUri": "s3://example-bucket/folder/", "targetUri": "s3://example-bucket/existing/", "overwrite": false,
	})); pluginError == nil {
		t.Fatal("expected rename over an existing directory to fail without overwrite")
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/rename", mergeIntegration(base, map[string]any{
		"sourceUri": "s3://example-bucket/folder/", "targetUri": "s3://example-bucket/existing/", "overwrite": true,
	})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/existing/nested.txt", "maxBytes": 64})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/existing/old.txt", "maxBytes": 64})); pluginError == nil {
		t.Fatal("expected overwritten directory contents to be removed")
	}

	if _, pluginError = invokeIntegration(t, plugin, "filesystem/rename", mergeIntegration(base, map[string]any{
		"sourceUri": "s3://example-bucket/existing/", "targetUri": "s3://example-bucket/archive/", "overwrite": false,
	})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/archive/nested.txt", "maxBytes": 64})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/delete", mergeIntegration(base, map[string]any{"uri": "s3://example-bucket/archive/", "recursive": true})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	renamedURI := objectURI(bucket, "renamed #中文?.txt")
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/rename", mergeIntegration(base, map[string]any{
		"sourceUri": "s3://example-bucket/root.txt", "targetUri": renamedURI,
	})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	renamed, pluginError := invokeIntegration(t, plugin, "filesystem/read", mergeIntegration(base, map[string]any{"uri": renamedURI}))
	if pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if renamed["dataBase64"] != "d29ybGQ=" {
		t.Fatal("file rename changed the stored content")
	}
	if _, pluginError = invokeIntegration(t, plugin, "filesystem/delete", mergeIntegration(base, map[string]any{"uri": renamedURI})); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	if _, pluginError = invokeIntegration(t, plugin, "connection/disconnect", connectionParams); pluginError != nil {
		t.Fatal(pluginError.Message)
	}
	expectedRemaining := 0
	if basePath != "" {
		expectedRemaining = 2
		for _, key := range []string{"outside.txt", basePath + "-other/outside.txt"} {
			if string(fake.objects[key].data) != "untouched" {
				t.Fatal("operation modified an object outside the configured base path")
			}
		}
	}
	if len(fake.objects) != expectedRemaining {
		t.Fatalf("unexpected objects after cleanup: %#v", fake.objects)
	}
}

func mergeIntegration(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func containsEntry(result map[string]any, name, kind string) bool {
	entries, ok := result["entries"].([]filesystemEntry)
	if ok {
		for _, entry := range entries {
			if entry.Name == name && entry.Kind == kind {
				return true
			}
		}
	}
	return false
}
