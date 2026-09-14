package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type filesystemEntry struct {
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Kind        string `json:"kind"`
	Size        *int64 `json:"size,omitempty"`
	ModifiedAt  string `json:"modifiedAt,omitempty"`
	ContentType string `json:"contentType,omitempty"`
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
