package main

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const uploadChunkBytes = 1024 * 1024

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
	received   int64
	processed  int64
	size       int64
	sealed     bool
	err        error
	versionID  string
	expiry     *time.Timer
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
		upload.writeMutex.Lock()
		upload.err = cause
		upload.writeMutex.Unlock()
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
			upload.writeMutex.Lock()
			upload.processed += int64(len(chunk))
			upload.lastWrite = time.Now()
			upload.writeMutex.Unlock()
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
				upload.shutdown(errors.New("S3 upload timed out waiting for progress"))
				upload.cancel()
				return
			}
		}
	}
}

func uploadSize(values map[string]any) (int64, *dbxpluginsdk.PluginError) {
	value, supplied := values["size"]
	if !supplied {
		return -1, nil
	}
	size, valid := value.(float64)
	if !valid || math.IsNaN(size) || math.IsInf(size, 0) || size < 0 || size > 5*1024*1024*1024*1024 || math.Trunc(size) != size {
		return 0, invalidParams("Invalid S3 upload size")
	}
	return int64(size), nil
}

func (upload *s3Upload) Read(data []byte) (int, error) {
	upload.markWrite()
	return len(data), nil
}

func (upload *s3Upload) seal() error {
	upload.writeMutex.Lock()
	defer upload.writeMutex.Unlock()
	if upload.err != nil {
		return upload.err
	}
	if upload.size >= 0 && upload.received != upload.size {
		return errors.New("S3 upload size does not match the received bytes")
	}
	if !upload.sealed {
		upload.sealed = true
		close(upload.chunks)
	}
	return nil
}

func (plugin *plugin) openUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	uploadID := stringValue(values["uploadId"])
	if !validStreamID(uploadID) {
		return nil, invalidParams("Invalid S3 upload id")
	}
	size, pluginError := uploadSize(values)
	if pluginError != nil {
		return nil, pluginError
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
	options, pluginError := writeOptions(existsContext, connection, path, values)
	existsCancel()
	if pluginError != nil {
		return nil, pluginError
	}
	remotePath := connection.remotePath(path)
	if size == -1 {
		options.PartSize = 16 * 1024 * 1024
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
		size:         size,
	}
	options.Progress = upload
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
		info, err := upload.putObject(context, connection.client, remotePath, reader, size, options)
		upload.writeMutex.Lock()
		upload.versionID = info.VersionID
		upload.writeMutex.Unlock()
		upload.shutdown(err)
		_ = reader.Close()
		upload.done <- err
		plugin.mutex.Lock()
		if plugin.uploads[upload.id] == upload {
			upload.expiry = time.AfterFunc(uploadIdleTimeout, func() {
				plugin.mutex.Lock()
				if plugin.uploads[upload.id] == upload {
					delete(plugin.uploads, upload.id)
				}
				plugin.mutex.Unlock()
			})
		}
		plugin.mutex.Unlock()
	}()
	go upload.pump()
	go upload.watchIdle()
	return map[string]any{"uploadId": uploadID, "channel": upload.channel, "windowBytes": 8 * uploadChunkBytes}, nil
}

func (plugin *plugin) HandleBinary(channel string, data []byte, _ *dbxpluginsdk.Emitter) *dbxpluginsdk.PluginError {
	if !strings.HasPrefix(channel, "s3.upload.") {
		return invalidParams("Unknown S3 binary channel")
	}
	plugin.mutex.RLock()
	upload := plugin.uploads[strings.TrimPrefix(channel, "s3.upload.")]
	plugin.mutex.RUnlock()
	if upload == nil {
		return remoteError("S3 upload is not active")
	}
	upload.writeMutex.Lock()
	if upload.sealed || len(data) > uploadChunkBytes || (upload.size >= 0 && upload.received+int64(len(data)) > upload.size) {
		upload.writeMutex.Unlock()
		return invalidParams("Invalid S3 upload chunk or size")
	}
	select {
	case <-upload.dead:
		upload.writeMutex.Unlock()
		return remoteError("S3 upload is not active")
	default:
	}
	select {
	case upload.chunks <- data:
		upload.received += int64(len(data))
		upload.lastWrite = time.Now()
		upload.writeMutex.Unlock()
		return nil
	default:
		upload.writeMutex.Unlock()
		err := errors.New("S3 upload queue is full; wait for upload status before sending more data")
		upload.shutdown(err)
		upload.cancel()
		return remoteError(err.Error())
	}
}

func (plugin *plugin) uploadStatus(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	plugin.mutex.RLock()
	upload := plugin.uploads[stringValue(values["uploadId"])]
	plugin.mutex.RUnlock()
	if upload == nil || upload.connectionID != stringValue(values["connectionId"]) {
		return nil, remoteError("S3 upload is not active")
	}
	if boolValue(values["seal"]) {
		if err := upload.seal(); err != nil {
			return nil, remoteError(err.Error())
		}
	}
	upload.writeMutex.Lock()
	defer upload.writeMutex.Unlock()
	if upload.err != nil {
		return nil, remoteError("S3 upload failed: " + upload.err.Error())
	}
	complete := false
	select {
	case <-upload.dead:
		complete = true
	default:
	}
	return map[string]any{"received": upload.received, "processed": upload.processed, "complete": complete}, nil
}

func (plugin *plugin) finishUpload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	uploadID := stringValue(values["uploadId"])
	upload := plugin.removeUpload(uploadID)
	if upload == nil {
		return nil, remoteError("S3 upload is not active")
	}
	if err := upload.seal(); err != nil {
		upload.shutdown(err)
		upload.cancel()
	}
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
	upload.writeMutex.Lock()
	defer upload.writeMutex.Unlock()
	if upload.err != nil {
		return nil, remoteError("S3 upload failed: " + upload.err.Error())
	}
	return map[string]any{"success": true, "message": "S3 object uploaded", "versionId": upload.versionID}, nil
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
	if upload != nil && upload.expiry != nil {
		upload.expiry.Stop()
	}
	plugin.mutex.Unlock()
	return upload
}
