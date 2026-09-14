package main

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type s3Upload struct {
	id           string
	channel      string
	connectionID string
	writer       *io.PipeWriter
	cancel       context.CancelFunc
	done         chan error
}

func (plugin *plugin) openUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	uploadID := stringValue(values["uploadId"])
	if !validStreamID(uploadID) {
		return nil, invalidParams("Invalid S3 upload id")
	}
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 upload requires a file URI")
	}
	context, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	exists := objectExists(context, connection, path)
	if exists && !boolValue(values["overwrite"]) {
		cancel()
		return nil, remoteError("S3 object already exists: " + path.key)
	}
	if !exists && !boolValue(values["create"]) {
		cancel()
		return nil, remoteError("S3 object does not exist: " + path.key)
	}
	remotePath := connection.remotePath(path)
	contentType := stringValue(values["contentType"])
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := connection.client.PutObject(context, remotePath.bucket, remotePath.key, reader, -1, minio.PutObjectOptions{ContentType: contentType})
		done <- err
	}()
	upload := &s3Upload{id: uploadID, channel: "s3.upload." + uploadID, connectionID: stringValue(values["connectionId"]), writer: writer, cancel: cancel, done: done}
	plugin.mutex.Lock()
	if plugin.uploads == nil {
		plugin.uploads = make(map[string]*s3Upload)
	}
	if _, exists := plugin.uploads[uploadID]; exists {
		plugin.mutex.Unlock()
		_ = writer.CloseWithError(errors.New("duplicate S3 upload id"))
		cancel()
		<-done
		return nil, invalidParams("S3 upload id is already active")
	}
	plugin.uploads[uploadID] = upload
	plugin.mutex.Unlock()
	return map[string]any{"uploadId": uploadID, "channel": upload.channel}, nil
}

func (plugin *plugin) HandleBinary(channel string, data []byte, _ *dbxpluginsdk.Emitter) *dbxpluginsdk.PluginError {
	if !strings.HasPrefix(channel, "s3.upload.") {
		return invalidParams("Unknown S3 binary channel")
	}
	plugin.mutex.RLock()
	var upload *s3Upload
	for _, candidate := range plugin.uploads {
		if candidate.channel == channel {
			upload = candidate
			break
		}
	}
	plugin.mutex.RUnlock()
	if upload == nil {
		return remoteError("S3 upload is not active")
	}
	if _, err := upload.writer.Write(data); err != nil {
		return remoteError("S3 upload failed: " + err.Error())
	}
	return nil
}

func (plugin *plugin) finishUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	uploadID := stringValue(values["uploadId"])
	upload := plugin.removeUpload(uploadID)
	if upload == nil {
		return nil, remoteError("S3 upload is not active")
	}
	closeErr := upload.writer.Close()
	putErr := <-upload.done
	upload.cancel()
	if closeErr != nil {
		return nil, remoteError("S3 upload failed: " + closeErr.Error())
	}
	if putErr != nil {
		return nil, remoteError("S3 upload failed: " + putErr.Error())
	}
	return map[string]any{"success": true, "message": "S3 object uploaded"}, nil
}

func (plugin *plugin) abortUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	upload := plugin.removeUpload(stringValue(values["uploadId"]))
	if upload != nil {
		_ = upload.writer.CloseWithError(errors.New("S3 upload aborted"))
		upload.cancel()
		<-upload.done
	}
	return map[string]any{"success": true}, nil
}

func (plugin *plugin) removeUpload(uploadID string) *s3Upload {
	plugin.mutex.Lock()
	upload := plugin.uploads[uploadID]
	delete(plugin.uploads, uploadID)
	plugin.mutex.Unlock()
	return upload
}
