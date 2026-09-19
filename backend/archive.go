package main

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

// maxArchiveBytes caps the uncompressed source data so deflate overhead
// cannot push the zip past maxStreamBytes, which would truncate it.
var (
	maxArchiveBytes   = int64(220 * 1024 * 1024)
	maxArchiveMembers = 20000
)

type archiveMember struct {
	bucket   string
	key      string
	name     string
	size     int64
	modified time.Time
}

func (plugin *plugin) openArchive(values map[string]any, emitter *dbxpluginsdk.Emitter) (any, *dbxpluginsdk.PluginError) {
	if emitter == nil {
		return nil, remoteError("S3 streaming is unavailable")
	}
	streamID := stringValue(values["streamId"])
	if !validStreamID(streamID) {
		return nil, invalidParams("Invalid S3 stream id")
	}
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	requestedUris, _ := values["uris"].([]any)
	if len(requestedUris) == 0 {
		return nil, invalidParams("S3 archive requires at least one URI")
	}
	listContext, cancelList := operationContext()
	defer cancelList()
	members, totalSize, pluginError := planArchive(listContext, connection, requestedUris)
	if pluginError != nil {
		return nil, pluginError
	}
	streamContext, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	stream := &s3Stream{id: streamID, connectionID: stringValue(values["connectionId"]), reader: reader, context: streamContext, cancel: cancel, maxBytes: maxStreamBytes}
	plugin.mutex.Lock()
	if plugin.streams == nil {
		plugin.streams = make(map[string]*s3Stream)
	}
	if _, exists := plugin.streams[streamID]; exists {
		plugin.mutex.Unlock()
		cancel()
		_ = reader.Close()
		return nil, invalidParams("S3 stream id is already active")
	}
	plugin.streams[streamID] = stream
	plugin.mutex.Unlock()
	go plugin.writeArchive(streamContext, connection, members, writer)
	go plugin.pumpStream(stream, emitter)
	return map[string]any{"streamId": streamID, "size": totalSize, "contentType": "application/zip", "files": len(members)}, nil
}

func planArchive(context context.Context, connection *s3Connection, requestedUris []any) ([]archiveMember, int64, *dbxpluginsdk.PluginError) {
	return planArchiveWithLimit(context, connection, requestedUris, maxArchiveBytes)
}

func planArchiveWithLimit(context context.Context, connection *s3Connection, requestedUris []any, sizeLimit int64) ([]archiveMember, int64, *dbxpluginsdk.PluginError) {
	members := make([]archiveMember, 0, 64)
	totalSize := int64(0)
	for _, requestedUri := range requestedUris {
		path, pluginError := parseObjectPath(stringValue(requestedUri), connection)
		if pluginError != nil {
			return nil, 0, pluginError
		}
		if path.bucket == "" {
			return nil, 0, invalidParams("S3 archive requires a bucket URI")
		}
		if path.key != "" && !strings.HasSuffix(path.key, "/") {
			remotePath := connection.remotePath(path)
			metadata, err := connection.client.StatObject(context, remotePath.bucket, remotePath.key, minio.StatObjectOptions{})
			if err != nil {
				return nil, 0, remoteError("S3 archive stat failed: " + err.Error())
			}
			members = append(members, archiveMember{bucket: path.bucket, key: remotePath.key, name: baseName(path.key), size: metadata.Size, modified: metadata.LastModified})
			totalSize += metadata.Size
		} else {
			listed, pluginError := listArchiveMembers(context, connection, path)
			if pluginError != nil {
				return nil, 0, pluginError
			}
			members = append(members, listed...)
			for _, member := range listed {
				totalSize += member.size
			}
		}
		if sizeLimit > 0 && totalSize > sizeLimit {
			return nil, 0, invalidParams("S3 archive exceeds the size limit")
		}
		if len(members) > maxArchiveMembers {
			return nil, 0, invalidParams("S3 archive exceeds the file count limit")
		}
	}
	return members, totalSize, nil
}

func listArchiveMembers(context context.Context, connection *s3Connection, path objectPath) ([]archiveMember, *dbxpluginsdk.PluginError) {
	localPrefix := path.key
	rootName := baseName(strings.TrimSuffix(localPrefix, "/"))
	if localPrefix == "" {
		rootName = path.bucket
	}
	remotePrefix := connection.remoteKey(localPrefix)
	if remotePrefix != "" && !strings.HasSuffix(remotePrefix, "/") {
		remotePrefix += "/"
	}
	members := make([]archiveMember, 0, 16)
	objects := connection.client.ListObjects(context, path.bucket, minio.ListObjectsOptions{Prefix: remotePrefix, Recursive: true})
	for object := range objects {
		if object.Err != nil {
			return nil, remoteError("S3 archive listing failed: " + object.Err.Error())
		}
		localKey, ok := connection.localKey(object.Key)
		if !ok || localKey == "" || localKey == localPrefix || strings.HasSuffix(localKey, "/") {
			continue
		}
		relative := strings.TrimPrefix(localKey, localPrefix)
		members = append(members, archiveMember{bucket: path.bucket, key: object.Key, name: rootName + "/" + relative, size: object.Size, modified: object.LastModified})
		if len(members) > maxArchiveMembers {
			return nil, invalidParams("S3 archive exceeds the file count limit")
		}
	}
	return members, nil
}

func (plugin *plugin) writeArchive(context context.Context, connection *s3Connection, members []archiveMember, writer *io.PipeWriter) {
	zipWriter := zip.NewWriter(writer)
	for _, member := range members {
		select {
		case <-context.Done():
			_ = zipWriter.Close()
			_ = writer.CloseWithError(context.Err())
			return
		default:
		}
		header := &zip.FileHeader{Name: member.name, Method: zip.Deflate, Modified: member.modified}
		entry, err := zipWriter.CreateHeader(header)
		if err == nil {
			var object *minio.Object
			object, err = connection.client.GetObject(context, member.bucket, member.key, minio.GetObjectOptions{})
			if err == nil {
				_, err = io.Copy(entry, object)
				if closeErr := object.Close(); err == nil {
					err = closeErr
				}
			}
		}
		if err != nil {
			_ = zipWriter.Close()
			_ = writer.CloseWithError(errors.New("S3 archive failed: " + err.Error()))
			return
		}
	}
	if err := zipWriter.Close(); err != nil {
		_ = writer.CloseWithError(errors.New("S3 archive failed: " + err.Error()))
		return
	}
	_ = writer.Close()
}

func baseName(key string) string {
	trimmed := strings.TrimSuffix(key, "/")
	if separator := strings.LastIndex(trimmed, "/"); separator >= 0 {
		return trimmed[separator+1:]
	}
	return trimmed
}
