package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const (
	pluginID           = "io.github.t8y2.s3"
	pluginVersion      = "0.1.2"
	filesystemProvider = "io.github.t8y2.s3.files"
	maxInlineBytes     = 4 * 1024 * 1024
	defaultPageSize    = 200
	maxPageSize        = 1000
	operationTimeout   = 30 * time.Second
)

type plugin struct {
	mutex       sync.RWMutex
	connections map[string]*s3Connection
}

type s3Connection struct {
	client   *minio.Client
	bucket   string
	region   string
	endpoint string
	basePath string
}

type connectionConfig struct {
	id           string
	bucket       string
	accessKey    string
	secretKey    string
	sessionToken string
	endpoint     string
	region       string
	basePath     string
	bucketLookup minio.BucketLookupType
}

type objectPath struct {
	bucket string
	key    string
}

type filesystemEntry struct {
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Kind        string `json:"kind"`
	Size        *int64 `json:"size,omitempty"`
	ModifiedAt  string `json:"modifiedAt,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

func (plugin *plugin) Handle(
	_ dbxpluginsdk.RequestContext,
	method string,
	params json.RawMessage,
	_ *dbxpluginsdk.Emitter,
) (any, *dbxpluginsdk.PluginError) {
	var values map[string]any
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, invalidParams("Invalid request parameters")
	}

	switch method {
	case "connection/test":
		if pluginError := requireConnectionProvider(values); pluginError != nil {
			return nil, pluginError
		}
		connection, pluginError := parseConnection(values)
		if pluginError != nil {
			return nil, pluginError
		}
		if pluginError := verifyConnection(connection); pluginError != nil {
			return nil, pluginError
		}
		return map[string]any{"success": true, "message": "S3 connection succeeded"}, nil
	case "connection/connect":
		if pluginError := requireConnectionProvider(values); pluginError != nil {
			return nil, pluginError
		}
		connection, pluginError := parseConnection(values)
		if pluginError != nil {
			return nil, pluginError
		}
		client, pluginError := createConnection(connection)
		if pluginError != nil {
			return nil, pluginError
		}
		if pluginError := verifyBucket(client); pluginError != nil {
			return nil, pluginError
		}
		plugin.mutex.Lock()
		plugin.connections[connection.id] = client
		plugin.mutex.Unlock()
		return map[string]any{"success": true}, nil
	case "connection/disconnect":
		if pluginError := requireConnectionProvider(values); pluginError != nil {
			return nil, pluginError
		}
		connectionID, pluginError := requestConnectionID(values)
		if pluginError != nil {
			return nil, pluginError
		}
		plugin.mutex.Lock()
		delete(plugin.connections, connectionID)
		plugin.mutex.Unlock()
		return map[string]any{"success": true}, nil
	case "filesystem/list":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.listObjects(values)
	case "filesystem/read":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.readObject(values)
	case "filesystem/write":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.writeObject(values)
	case "filesystem/createDirectory":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.createDirectory(values)
	case "filesystem/delete":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.deleteObject(values)
	case "filesystem/rename":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.renameObject(values)
	case pluginID + "/ping":
		return map[string]any{"ok": true, "plugin": pluginID, "language": "go", "connectionId": values["connectionId"]}, nil
	default:
		return nil, dbxpluginsdk.MethodNotFound(method)
	}
}

func parseConnection(values map[string]any) (connectionConfig, *dbxpluginsdk.PluginError) {
	connection, _ := values["connection"].(map[string]any)
	result := connectionConfig{
		id:        stringValue(connection["id"]),
		bucket:    stringValue(connection["database"]),
		accessKey: stringValue(connection["username"]),
	}
	secrets, _ := connection["connection_secrets"].(map[string]any)
	result.secretKey = stringValue(secrets["secret_key"])
	result.sessionToken = stringValue(secrets["session_token"])
	config, _ := connection["external_config"].(map[string]any)
	result.endpoint = stringValue(config["endpoint"])
	result.region = stringValue(config["region"])
	endpointProtocol := stringValue(config["endpoint_protocol"])
	if endpointProtocol != "" && endpointProtocol != "http" && endpointProtocol != "https" {
		return connectionConfig{}, invalidParams("S3 endpoint protocol must be http or https")
	}
	if !strings.Contains(result.endpoint, "://") {
		if endpointProtocol == "" {
			endpointProtocol = "https"
		}
		result.endpoint = endpointProtocol + "://" + result.endpoint
	} else if endpointProtocol != "" {
		parsedEndpoint, parseErr := url.Parse(result.endpoint)
		if parseErr != nil || parsedEndpoint.Host == "" {
			return connectionConfig{}, invalidParams("S3 endpoint must be a valid host")
		}
		parsedEndpoint.Scheme = endpointProtocol
		result.endpoint = parsedEndpoint.String()
	}
	rawBasePath := stringValue(config["base_path"])
	result.basePath = normalizeBasePath(rawBasePath)
	if strings.Trim(strings.TrimSpace(rawBasePath), "/") != "" && result.basePath == "" {
		return connectionConfig{}, invalidParams("S3 base path must contain valid object path segments")
	}
	addressingStyle := stringValue(config["addressing_style"])
	if addressingStyle == "" || addressingStyle == "auto" {
		result.bucketLookup = minio.BucketLookupAuto
	} else if addressingStyle == "path" {
		result.bucketLookup = minio.BucketLookupPath
	} else if addressingStyle == "virtual" {
		result.bucketLookup = minio.BucketLookupDNS
	} else {
		return connectionConfig{}, invalidParams("S3 addressing style must be auto, path, or virtual")
	}

	for field, value := range map[string]string{
		"id": result.id, "bucket": result.bucket, "access key": result.accessKey,
		"secret key": result.secretKey, "endpoint": result.endpoint, "region": result.region,
	} {
		if strings.TrimSpace(value) == "" {
			return connectionConfig{}, invalidParams("Missing S3 connection field: " + field)
		}
	}
	parsedEndpoint, err := url.Parse(result.endpoint)
	if err != nil || parsedEndpoint.Host == "" || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || (parsedEndpoint.Path != "" && parsedEndpoint.Path != "/") || parsedEndpoint.RawQuery != "" || parsedEndpoint.Fragment != "" || parsedEndpoint.User != nil {
		return connectionConfig{}, invalidParams("S3 endpoint must be an HTTP(S) origin without a path")
	}
	return result, nil
}

func normalizeBasePath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsRune(segment, '\x00') {
			return ""
		}
	}
	return value
}

func requireConnectionProvider(values map[string]any) *dbxpluginsdk.PluginError {
	provider, _ := values["provider"].(map[string]any)
	if stringValue(provider["id"]) != pluginID+".connection" || stringValue(provider["databaseType"]) != "s3" {
		return invalidParams("Unknown S3 connection provider")
	}
	return nil
}

func requireFilesystemProvider(values map[string]any) *dbxpluginsdk.PluginError {
	if stringValue(values["providerId"]) != filesystemProvider {
		return invalidParams("Unknown S3 filesystem provider")
	}
	return nil
}

func createConnection(config connectionConfig) (*s3Connection, *dbxpluginsdk.PluginError) {
	parsedEndpoint, _ := url.Parse(config.endpoint)
	client, err := minio.New(parsedEndpoint.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(config.accessKey, config.secretKey, config.sessionToken),
		Secure:       parsedEndpoint.Scheme == "https",
		Region:       config.region,
		BucketLookup: config.bucketLookup,
		MaxRetries:   2,
	})
	if err != nil {
		return nil, remoteError("Failed to create S3 client: " + err.Error())
	}
	return &s3Connection{client: client, bucket: config.bucket, region: config.region, endpoint: config.endpoint, basePath: config.basePath}, nil
}

func verifyConnection(config connectionConfig) *dbxpluginsdk.PluginError {
	connection, pluginError := createConnection(config)
	if pluginError != nil {
		return pluginError
	}
	return verifyBucket(connection)
}

func verifyBucket(connection *s3Connection) *dbxpluginsdk.PluginError {
	context, cancel := operationContext()
	defer cancel()
	exists, err := connection.client.BucketExists(context, connection.bucket)
	if err != nil {
		return remoteError("S3 bucket check failed: " + err.Error())
	}
	if !exists {
		return remoteError("S3 bucket does not exist: " + connection.bucket)
	}
	return nil
}

func (plugin *plugin) connectionFor(values map[string]any) (*s3Connection, *dbxpluginsdk.PluginError) {
	connectionID := stringValue(values["connectionId"])
	if connectionID == "" {
		return nil, invalidParams("Missing connectionId")
	}
	plugin.mutex.RLock()
	connection := plugin.connections[connectionID]
	plugin.mutex.RUnlock()
	if connection == nil {
		return nil, remoteError("S3 connection is not active: " + connectionID)
	}
	return connection, nil
}

func parseObjectPath(rawURI string, connection *s3Connection) (objectPath, *dbxpluginsdk.PluginError) {
	if rawURI == "" {
		rawURI = "s3:/"
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "s3" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || strings.ContainsRune(parsed.Path, '\x00') {
		return objectPath{}, invalidParams("Invalid S3 object URI")
	}
	bucket := parsed.Host
	if bucket == "" {
		bucket = connection.bucket
	}
	if bucket != connection.bucket {
		return objectPath{}, invalidParams("S3 URI bucket does not match the connected bucket")
	}
	key := strings.TrimPrefix(parsed.Path, "/")
	return objectPath{bucket: bucket, key: key}, nil
}

func (plugin *plugin) listObjects(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	context, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil {
		return nil, pluginError
	}
	prefix := path.key
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	limit := int(numberValue(values["limit"]))
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	cursor := decodeCursor(stringValue(values["cursor"]))
	remotePrefix := connection.remoteKey(prefix)
	remoteCursor := connection.remoteKey(cursor)
	objects := connection.client.ListObjects(context, path.bucket, minio.ListObjectsOptions{Prefix: remotePrefix, Recursive: false, StartAfter: remoteCursor, MaxKeys: limit + 1})
	listedObjects := make([]minio.ObjectInfo, 0, limit+1)
	seenKeys := make(map[string]struct{}, limit+1)
	for object := range objects {
		if object.Err != nil {
			return nil, remoteError("S3 list failed: " + object.Err.Error())
		}
		localKey, ok := connection.localKey(object.Key)
		if !ok || localKey == prefix {
			continue
		}
		entry := entryFromObject(minio.ObjectInfo{Key: localKey}, path.bucket)
		if !validEntryName(entry.Name) {
			continue
		}
		object.Key = localKey
		if _, seen := seenKeys[localKey]; seen {
			continue
		}
		seenKeys[localKey] = struct{}{}
		listedObjects = append(listedObjects, object)
		if len(listedObjects) >= limit+1 {
			break
		}
	}
	sort.SliceStable(listedObjects, func(left, right int) bool { return listedObjects[left].Key < listedObjects[right].Key })
	entries := make([]filesystemEntry, 0, minInt(limit, len(listedObjects)))
	for _, object := range listedObjects[:minInt(limit, len(listedObjects))] {
		entries = append(entries, entryFromObject(object, path.bucket))
	}
	result := map[string]any{"entries": entries}
	if len(listedObjects) > limit {
		result["nextCursor"] = encodeCursor(listedObjects[limit-1].Key)
	}
	return result, nil
}

func (plugin *plugin) readObject(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	context, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 read requires a file URI")
	}
	maxBytes := int64(numberValue(values["maxBytes"]))
	if maxBytes <= 0 || maxBytes > maxInlineBytes {
		maxBytes = maxInlineBytes
	}
	remotePath := connection.remotePath(path)
	object, err := connection.client.GetObject(context, remotePath.bucket, remotePath.key, minio.GetObjectOptions{})
	if err != nil {
		return nil, remoteError("S3 read failed: " + err.Error())
	}
	defer object.Close()
	metadata, err := object.Stat()
	if err != nil {
		return nil, remoteError("S3 read failed: " + err.Error())
	}
	data, err := io.ReadAll(io.LimitReader(object, maxBytes+1))
	if err != nil {
		return nil, remoteError("S3 read failed: " + err.Error())
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return map[string]any{
		"dataBase64":  base64.StdEncoding.EncodeToString(data),
		"contentType": metadata.ContentType,
		"truncated":   truncated,
		"etag":        metadata.ETag,
	}, nil
}

func (plugin *plugin) writeObject(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	context, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 write requires a file URI")
	}
	data, err := base64.StdEncoding.DecodeString(stringValue(values["dataBase64"]))
	if err != nil || len(data) > maxInlineBytes {
		return nil, invalidParams("S3 inline writes must contain valid base64 data up to 4 MiB")
	}
	etag := stringValue(values["etag"])
	exists := objectExists(context, connection, path)
	if etag != "" {
		remotePath := connection.remotePath(path)
		metadata, statErr := connection.client.StatObject(context, remotePath.bucket, remotePath.key, minio.StatObjectOptions{})
		if statErr != nil || metadata.ETag != etag {
			return nil, remoteError("S3 object changed before write")
		}
	}
	overwrite := boolValue(values["overwrite"])
	create := boolValue(values["create"])
	if exists && !overwrite {
		return nil, remoteError("S3 object already exists: " + path.key)
	}
	if !exists && !create {
		return nil, remoteError("S3 object does not exist: " + path.key)
	}
	contentType := stringValue(values["contentType"])
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	options := minio.PutObjectOptions{ContentType: contentType}
	if etag != "" {
		options.SetMatchETag(etag)
	} else if create && !overwrite {
		options.SetMatchETagExcept("*")
	}
	remotePath := connection.remotePath(path)
	_, err = connection.client.PutObject(context, remotePath.bucket, remotePath.key, bytes.NewReader(data), int64(len(data)), options)
	if err != nil {
		return nil, remoteError("S3 write failed: " + err.Error())
	}
	return map[string]any{"success": true, "message": "S3 object written"}, nil
}

func (plugin *plugin) createDirectory(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	context, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" {
		return nil, invalidParams("S3 directory URI is required")
	}
	if !strings.HasSuffix(path.key, "/") {
		path.key += "/"
	}
	remotePath := connection.remotePath(path)
	_, err := connection.client.PutObject(context, remotePath.bucket, remotePath.key, strings.NewReader(""), 0, minio.PutObjectOptions{ContentType: "application/x-directory"})
	if err != nil {
		return nil, remoteError("S3 directory creation failed: " + err.Error())
	}
	return map[string]any{"success": true, "message": "S3 directory created"}, nil
}

func (plugin *plugin) deleteObject(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	context, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" {
		return nil, invalidParams("S3 delete requires an object URI")
	}
	if boolValue(values["recursive"]) && strings.HasSuffix(path.key, "/") {
		if pluginError := deletePrefix(context, connection, path); pluginError != nil {
			return nil, pluginError
		}
	} else {
		remotePath := connection.remotePath(path)
		if err := connection.client.RemoveObject(context, remotePath.bucket, remotePath.key, minio.RemoveObjectOptions{}); err != nil {
			return nil, remoteError("S3 delete failed: " + err.Error())
		}
	}
	return map[string]any{"success": true, "message": "S3 object deleted"}, nil
}

func (plugin *plugin) renameObject(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	context, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	source, pluginError := parseObjectPath(stringValue(values["sourceUri"]), connection)
	if pluginError != nil {
		return nil, pluginError
	}
	target, pluginError := parseObjectPath(stringValue(values["targetUri"]), connection)
	if pluginError != nil {
		return nil, pluginError
	}
	if source.key == "" || target.key == "" {
		return nil, invalidParams("S3 rename requires source and target object URIs")
	}
	if source.key == target.key {
		return nil, invalidParams("S3 rename source and target must differ")
	}
	if strings.HasSuffix(source.key, "/") {
		if !strings.HasSuffix(target.key, "/") {
			target.key += "/"
		}
		if source.key == target.key || strings.HasPrefix(target.key, source.key) || strings.HasPrefix(source.key, target.key) {
			return nil, invalidParams("S3 directory rename paths cannot overlap")
		}
		if !prefixExists(context, connection, source) {
			return nil, remoteError("S3 source directory does not exist: " + source.key)
		}
		overwrite := boolValue(values["overwrite"])
		targetFile := objectPath{bucket: target.bucket, key: strings.TrimSuffix(target.key, "/")}
		targetHasFile := objectExists(context, connection, targetFile)
		targetHasPrefix := prefixExists(context, connection, target)
		if (targetHasFile || targetHasPrefix) && !overwrite {
			return nil, remoteError("S3 target object already exists: " + strings.TrimSuffix(target.key, "/"))
		}
		if targetHasFile {
			remoteTargetFile := connection.remotePath(targetFile)
			if err := connection.client.RemoveObject(context, remoteTargetFile.bucket, remoteTargetFile.key, minio.RemoveObjectOptions{}); err != nil {
				return nil, remoteError("S3 target cleanup failed: " + err.Error())
			}
		}
		if targetHasPrefix {
			if pluginError := deletePrefix(context, connection, target); pluginError != nil {
				return nil, pluginError
			}
		}
		if pluginError := copyPrefix(context, connection, source, target); pluginError != nil {
			return nil, pluginError
		}
		if pluginError := deletePrefix(context, connection, source); pluginError != nil {
			return nil, pluginError
		}
		return map[string]any{"success": true, "message": "S3 directory renamed"}, nil
	}
	if strings.HasSuffix(target.key, "/") {
		return nil, invalidParams("S3 file target cannot be a directory URI")
	}
	if !boolValue(values["overwrite"]) && objectExists(context, connection, target) {
		return nil, remoteError("S3 target object already exists: " + target.key)
	}
	remoteSource := connection.remotePath(source)
	remoteTarget := connection.remotePath(target)
	_, err := connection.client.CopyObject(context, minio.CopyDestOptions{Bucket: remoteTarget.bucket, Object: remoteTarget.key}, minio.CopySrcOptions{Bucket: remoteSource.bucket, Object: remoteSource.key})
	if err != nil {
		return nil, remoteError("S3 rename copy failed: " + err.Error())
	}
	if err := connection.client.RemoveObject(context, remoteSource.bucket, remoteSource.key, minio.RemoveObjectOptions{}); err != nil {
		return nil, remoteError("S3 rename cleanup failed: " + err.Error())
	}
	return map[string]any{"success": true, "message": "S3 object renamed"}, nil
}

func objectExists(context context.Context, connection *s3Connection, path objectPath) bool {
	remotePath := connection.remotePath(path)
	_, err := connection.client.StatObject(context, remotePath.bucket, remotePath.key, minio.StatObjectOptions{})
	return err == nil
}

func prefixExists(parentContext context.Context, connection *s3Connection, path objectPath) bool {
	childContext, cancel := context.WithCancel(parentContext)
	defer cancel()
	remotePath := connection.remotePath(path)
	objects := connection.client.ListObjects(childContext, remotePath.bucket, minio.ListObjectsOptions{Prefix: remotePath.key, Recursive: true, MaxKeys: 1})
	for object := range objects {
		return object.Err == nil
	}
	return false
}

func deletePrefix(context context.Context, connection *s3Connection, path objectPath) *dbxpluginsdk.PluginError {
	remotePath := connection.remotePath(path)
	objects := connection.client.ListObjects(context, remotePath.bucket, minio.ListObjectsOptions{Prefix: remotePath.key, Recursive: true})
	for object := range objects {
		if object.Err != nil {
			return remoteError("S3 recursive delete listing failed: " + object.Err.Error())
		}
		if err := connection.client.RemoveObject(context, path.bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
			return remoteError("S3 recursive delete failed: " + err.Error())
		}
	}
	return nil
}

func copyPrefix(context context.Context, connection *s3Connection, source objectPath, target objectPath) *dbxpluginsdk.PluginError {
	remoteSource := connection.remotePath(source)
	remoteTarget := connection.remotePath(target)
	objects := connection.client.ListObjects(context, remoteSource.bucket, minio.ListObjectsOptions{Prefix: remoteSource.key, Recursive: true})
	for object := range objects {
		if object.Err != nil {
			return remoteError("S3 directory copy listing failed: " + object.Err.Error())
		}
		relativeKey := strings.TrimPrefix(object.Key, remoteSource.key)
		_, err := connection.client.CopyObject(
			context,
			minio.CopyDestOptions{Bucket: remoteTarget.bucket, Object: remoteTarget.key + relativeKey},
			minio.CopySrcOptions{Bucket: remoteSource.bucket, Object: object.Key},
		)
		if err != nil {
			return remoteError("S3 directory copy failed: " + err.Error())
		}
	}
	return nil
}

func (connection *s3Connection) remoteKey(localKey string) string {
	if connection.basePath == "" {
		return localKey
	}
	if localKey == "" {
		return connection.basePath + "/"
	}
	return connection.basePath + "/" + localKey
}

func (connection *s3Connection) localKey(remoteKey string) (string, bool) {
	if connection.basePath == "" {
		return remoteKey, true
	}
	prefix := connection.basePath + "/"
	if remoteKey == connection.basePath {
		return "", true
	}
	if !strings.HasPrefix(remoteKey, prefix) {
		return "", false
	}
	return strings.TrimPrefix(remoteKey, prefix), true
}

func (connection *s3Connection) remotePath(path objectPath) objectPath {
	path.key = connection.remoteKey(path.key)
	return path
}

func entryFromObject(object minio.ObjectInfo, bucket string) filesystemEntry {
	isDirectory := object.Key != "" && strings.HasSuffix(object.Key, "/")
	trimmedKey := strings.TrimSuffix(object.Key, "/")
	name := trimmedKey
	if separator := strings.LastIndex(trimmedKey, "/"); separator >= 0 {
		name = trimmedKey[separator+1:]
	}
	entry := filesystemEntry{Name: name, URI: objectURI(bucket, object.Key), Kind: "file"}
	if isDirectory {
		entry.Kind = "directory"
	} else {
		size := object.Size
		entry.Size = &size
		entry.ContentType = object.ContentType
	}
	if !object.LastModified.IsZero() {
		entry.ModifiedAt = object.LastModified.UTC().Format(time.RFC3339)
	}
	return entry
}

func validEntryName(name string) bool {
	if strings.TrimSpace(name) == "" || len(name) > 1024 {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func objectURI(bucket string, key string) string {
	return (&url.URL{Scheme: "s3", Host: bucket, Path: "/" + key}).String()
}

func requestConnectionID(values map[string]any) (string, *dbxpluginsdk.PluginError) {
	connection, _ := values["connection"].(map[string]any)
	connectionID := stringValue(connection["id"])
	if connectionID == "" {
		connectionID = stringValue(values["connectionId"])
	}
	if connectionID == "" {
		return "", invalidParams("Missing connection id")
	}
	return connectionID, nil
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func numberValue(value any) int {
	result, _ := value.(float64)
	return int(result)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func encodeCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeCursor(value string) string {
	if value == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return value
	}
	return string(decoded)
}

func invalidParams(message string) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32602, message)
}

func remoteError(message string) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32010, message)
}

func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), operationTimeout)
}

func main() {
	metadata := dbxpluginsdk.Metadata{ID: pluginID, Version: pluginVersion, Capabilities: []string{"connections", "filesystem"}}
	server := dbxpluginsdk.NewServer(metadata, &plugin{connections: map[string]*s3Connection{}})
	if err := server.Serve(); err != nil {
		log.Fatal("DBX S3 Sidecar stopped:", err)
	}
}
