package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type s3Stream struct {
	id           string
	connectionID string
	reader       io.ReadCloser
	context      context.Context
	cancel       context.CancelFunc
	maxBytes     int64
}

func (plugin *plugin) openStream(values map[string]any, emitter *dbxpluginsdk.Emitter) (any, *dbxpluginsdk.PluginError) {
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
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 stream requires a file URI")
	}
	maxBytes := int64(numberValue(values["maxBytes"]))
	if maxBytes <= 0 {
		maxBytes = maxStreamBytes
	}
	if maxBytes > maxStreamBytes {
		return nil, invalidParams("S3 stream exceeds the 256 MiB limit")
	}
	streamContext, cancel := context.WithCancel(context.Background())
	remotePath := connection.remotePath(path)
	object, err := connection.client.GetObject(streamContext, remotePath.bucket, remotePath.key, minio.GetObjectOptions{})
	if err != nil {
		cancel()
		return nil, remoteError("S3 stream failed: " + err.Error())
	}
	metadata, err := object.Stat()
	if err != nil {
		cancel()
		_ = object.Close()
		return nil, remoteError("S3 stream failed: " + err.Error())
	}
	stream := &s3Stream{id: streamID, connectionID: stringValue(values["connectionId"]), reader: object, context: streamContext, cancel: cancel, maxBytes: maxBytes}
	plugin.mutex.Lock()
	if plugin.streams == nil {
		plugin.streams = make(map[string]*s3Stream)
	}
	if _, exists := plugin.streams[streamID]; exists {
		plugin.mutex.Unlock()
		cancel()
		_ = object.Close()
		return nil, invalidParams("S3 stream id is already active")
	}
	plugin.streams[streamID] = stream
	plugin.mutex.Unlock()
	go plugin.pumpStream(stream, emitter)
	return map[string]any{
		"streamId":     streamID,
		"size":         metadata.Size,
		"contentType":  metadata.ContentType,
		"etag":         metadata.ETag,
		"lastModified": metadata.LastModified.UTC().Format(time.RFC3339),
	}, nil
}

func (plugin *plugin) closeStream(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	streamID := stringValue(values["streamId"])
	if !validStreamID(streamID) {
		return nil, invalidParams("Invalid S3 stream id")
	}
	plugin.mutex.Lock()
	stream := plugin.streams[streamID]
	delete(plugin.streams, streamID)
	plugin.mutex.Unlock()
	if stream != nil {
		stream.cancel()
		_ = stream.reader.Close()
	}
	return map[string]any{"success": true}, nil
}

func (plugin *plugin) pumpStream(stream *s3Stream, emitter *dbxpluginsdk.Emitter) {
	defer func() {
		stream.cancel()
		_ = stream.reader.Close()
		plugin.mutex.Lock()
		delete(plugin.streams, stream.id)
		plugin.mutex.Unlock()
	}()

	buffer := make([]byte, streamChunkBytes)
	remaining := stream.maxBytes
	transferred := int64(0)
	for remaining > 0 {
		select {
		case <-stream.context.Done():
			return
		default:
		}
		readSize := int64(len(buffer))
		if remaining < readSize {
			readSize = remaining
		}
		readCount, err := stream.reader.Read(buffer[:readSize])
		if readCount > 0 {
			if pluginError := emitter.Event("host.stream.chunk", map[string]any{
				"streamId":   stream.id,
				"dataBase64": base64.StdEncoding.EncodeToString(buffer[:readCount]),
				"bytes":      readCount,
			}); pluginError != nil {
				return
			}
			transferred += int64(readCount)
			remaining -= int64(readCount)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				plugin.emitStreamEnd(stream, emitter, transferred, false)
				return
			}
			plugin.emitStreamError(stream, emitter, err)
			return
		}
		if readCount == 0 {
			plugin.emitStreamError(stream, emitter, errors.New("S3 stream returned no data"))
			return
		}
	}
	var extra [1]byte
	readCount, err := stream.reader.Read(extra[:])
	if err != nil && !errors.Is(err, io.EOF) {
		plugin.emitStreamError(stream, emitter, err)
		return
	}
	plugin.emitStreamEnd(stream, emitter, transferred, readCount > 0)
}

func (plugin *plugin) emitStreamEnd(stream *s3Stream, emitter *dbxpluginsdk.Emitter, transferred int64, truncated bool) {
	_ = emitter.Event("host.stream.end", map[string]any{"streamId": stream.id, "bytes": transferred, "truncated": truncated})
}

func (plugin *plugin) emitStreamError(stream *s3Stream, emitter *dbxpluginsdk.Emitter, err error) {
	_ = emitter.Event("host.stream.error", map[string]any{"streamId": stream.id, "message": err.Error()})
}
func validStreamID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, character := range id {
		if !(unicode.IsLetter(character) || unicode.IsDigit(character) || strings.ContainsRune("._-", character)) {
			return false
		}
	}
	return true
}
