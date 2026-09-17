package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

// The host dispatches binary frames on the plugin's single read loop, so
// HandleBinary must never block indefinitely: a stalled S3 write used to wedge
// the whole process until DBX was restarted. Chunks are handed to a pump
// goroutine instead, and every terminal path closes `dead` so a blocked
// sender returns promptly with an error. The buffer is deep because minio
// only reads between part uploads; a deep queue keeps the read loop free to
// serve JSON requests during slow transfers.
var (
	uploadChunkSlots   = 32
	uploadIdleTimeout  = 10 * time.Minute
	uploadPollInterval = 15 * time.Second
)

type s3Upload struct {
	id           string
	channel      string
	connectionID string
	writer       *io.PipeWriter
	cancel       context.CancelFunc

	chunks     chan []byte
	done       chan error // PutObject result
	pumpDone   chan struct{}
	dead       chan struct{}
	deadOnce   sync.Once
	failures   chan error
	lastWrite  time.Time
	writeMutex sync.Mutex
}

func (upload *s3Upload) markWrite() {
	upload.writeMutex.Lock()
	upload.lastWrite = time.Now()
	upload.writeMutex.Unlock()
}

// shutdown terminates the upload exactly once. Closing the pipe writer also
// unblocks a pump Write that is stuck because PutObject stopped reading.
func (upload *s3Upload) shutdown(cause error) {
	upload.deadOnce.Do(func() {
		close(upload.dead)
		if cause != nil {
			_ = upload.writer.CloseWithError(cause)
		} else {
			_ = upload.writer.Close()
		}
	})
}

func (upload *s3Upload) pump() {
	defer close(upload.pumpDone)
	for {
		select {
		case chunk, ok := <-upload.chunks:
			if !ok {
				_ = upload.writer.Close()
				return
			}
			if _, err := upload.writer.Write(chunk); err != nil {
				select {
				case upload.failures <- err:
				default:
				}
				upload.shutdown(err)
				return
			}
		case <-upload.dead:
			return
		}
	}
}

func (upload *s3Upload) watchIdle() {
	ticker := time.NewTicker(uploadPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-upload.dead:
			return
		case <-ticker.C:
			upload.writeMutex.Lock()
			idle := time.Since(upload.lastWrite) > uploadIdleTimeout
			upload.writeMutex.Unlock()
			if idle {
				// No data from the host for a while: cancel so PutObject
				// aborts and its goroutine runs shutdown.
				upload.cancel()
				return
			}
		}
	}
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
	if pluginError := requireWritable(connection); pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 upload requires a file URI")
	}
	existsContext, existsCancel := operationContext()
	exists := objectExists(existsContext, connection, path)
	existsCancel()
	if exists && !boolValue(values["overwrite"]) {
		return nil, remoteError("S3 object already exists: " + path.key)
	}
	if !exists && !boolValue(values["create"]) {
		return nil, remoteError("S3 object does not exist: " + path.key)
	}
	remotePath := connection.remotePath(path)
	contentType := stringValue(values["contentType"])
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// A fixed deadline kills slow-but-healthy transfers; the idle watchdog
	// above covers abandoned uploads instead.
	context, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	upload := &s3Upload{
		id:           uploadID,
		channel:      "s3.upload." + uploadID,
		connectionID: stringValue(values["connectionId"]),
		writer:       writer,
		cancel:       cancel,
		chunks:       make(chan []byte, uploadChunkSlots),
		done:         make(chan error, 1),
		pumpDone:     make(chan struct{}),
		dead:         make(chan struct{}),
		failures:     make(chan error, 1),
		lastWrite:    time.Now(),
	}
	plugin.mutex.Lock()
	if plugin.uploads == nil {
		plugin.uploads = make(map[string]*s3Upload)
	}
	if _, exists := plugin.uploads[uploadID]; exists {
		plugin.mutex.Unlock()
		_ = writer.CloseWithError(errors.New("duplicate S3 upload id"))
		cancel()
		return nil, invalidParams("S3 upload id is already active")
	}
	plugin.uploads[uploadID] = upload
	plugin.mutex.Unlock()
	go func() {
		_, err := connection.client.PutObject(context, remotePath.bucket, remotePath.key, reader, -1, minio.PutObjectOptions{ContentType: contentType})
		upload.shutdown(err)
		upload.done <- err
		plugin.removeUpload(upload.id)
	}()
	go upload.pump()
	go upload.watchIdle()
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
	upload.markWrite()
	select {
	case <-upload.dead:
		return remoteError("S3 upload is not active")
	default:
	}
	select {
	case upload.chunks <- data:
		return nil
	case <-upload.dead:
		return remoteError("S3 upload is not active")
	}
}

func (plugin *plugin) finishUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	uploadID := stringValue(values["uploadId"])
	upload := plugin.removeUpload(uploadID)
	if upload == nil {
		return nil, remoteError("S3 upload is not active")
	}
	close(upload.chunks)
	<-upload.pumpDone
	putErr := <-upload.done
	upload.cancel()
	var writeErr error
	select {
	case writeErr = <-upload.failures:
	default:
	}
	if writeErr != nil {
		return nil, remoteError("S3 upload failed: " + writeErr.Error())
	}
	if putErr != nil {
		return nil, remoteError("S3 upload failed: " + putErr.Error())
	}
	return map[string]any{"success": true, "message": "S3 object uploaded"}, nil
}

func (plugin *plugin) abortUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	upload := plugin.removeUpload(stringValue(values["uploadId"]))
	if upload != nil {
		upload.shutdown(errors.New("S3 upload aborted"))
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
