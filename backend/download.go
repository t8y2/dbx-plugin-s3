package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const downloadChunkBytes = 1024 * 1024

type s3Download struct {
	mutex        sync.Mutex
	readMutex    sync.Mutex
	connectionID string
	reader       io.ReadCloser
	context      context.Context
	cancel       context.CancelFunc
	timer        *time.Timer
}

func (plugin *plugin) openDownload(values map[string]any) (result any, pluginError *dbxpluginsdk.PluginError) {
	id := stringValue(values["downloadId"])
	if !validStreamID(id) {
		return nil, invalidParams("Invalid S3 download id")
	}
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	downloadContext, cancel := context.WithCancel(context.Background())
	download := &s3Download{connectionID: stringValue(values["connectionId"]), context: downloadContext, cancel: cancel}
	download.mutex.Lock()
	defer download.mutex.Unlock()
	plugin.mutex.Lock()
	if plugin.downloads == nil {
		plugin.downloads = map[string]*s3Download{}
	}
	if plugin.downloads[id] != nil || len(plugin.downloads) >= 8 {
		plugin.mutex.Unlock()
		cancel()
		return nil, invalidParams("S3 download is already active or the download limit was reached")
	}
	plugin.downloads[id] = download
	download.timer = time.AfterFunc(90*time.Second, func() { plugin.removeDownload(id, download) })
	plugin.mutex.Unlock()
	defer func() {
		if pluginError != nil {
			cancel()
			download.timer.Stop()
			plugin.mutex.Lock()
			if plugin.downloads[id] == download {
				delete(plugin.downloads, id)
			}
			plugin.mutex.Unlock()
			if download.reader != nil {
				_ = download.reader.Close()
			}
		}
	}()
	if boolValue(values["archive"]) {
		uris, _ := values["uris"].([]any)
		if len(uris) == 0 || len(uris) > maxArchiveMembers {
			return nil, invalidParams("S3 archive requires a bounded list of URIs")
		}
		members, size, planError := planArchiveWithLimit(downloadContext, connection, uris, 0)
		if planError != nil {
			return nil, planError
		}
		reader, writer := io.Pipe()
		download.reader = reader
		go plugin.writeArchive(downloadContext, connection, members, writer)
		return map[string]any{"size": size, "contentType": "application/zip", "files": len(members)}, nil
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 download requires a file URI")
	}
	remotePath := connection.remotePath(path)
	object, err := connection.client.GetObject(downloadContext, remotePath.bucket, remotePath.key, minio.GetObjectOptions{})
	if err != nil {
		return nil, remoteError("S3 download failed: " + err.Error())
	}
	download.reader = object
	metadata, err := object.Stat()
	if err != nil {
		return nil, remoteError("S3 download failed: " + err.Error())
	}
	return map[string]any{"size": metadata.Size, "contentType": metadata.ContentType}, nil
}

func (plugin *plugin) readDownload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	id := stringValue(values["downloadId"])
	plugin.mutex.RLock()
	download := plugin.downloads[id]
	plugin.mutex.RUnlock()
	if download == nil || download.connectionID != stringValue(values["connectionId"]) {
		return nil, invalidParams("S3 download is not active")
	}
	download.readMutex.Lock()
	defer download.readMutex.Unlock()
	download.mutex.Lock()
	if download.context.Err() != nil {
		download.mutex.Unlock()
		return nil, remoteError("S3 download cancelled")
	}
	download.timer.Reset(90 * time.Second)
	reader := download.reader
	download.mutex.Unlock()
	buffer := make([]byte, downloadChunkBytes)
	count, err := reader.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, remoteError("S3 download failed: " + err.Error())
	}
	if count == 0 && err == nil {
		return nil, remoteError("S3 download made no progress")
	}
	return map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(buffer[:count]), "done": errors.Is(err, io.EOF)}, nil
}

func (plugin *plugin) removeDownload(id string, download *s3Download) {
	plugin.mutex.Lock()
	if plugin.downloads[id] != download {
		plugin.mutex.Unlock()
		return
	}
	delete(plugin.downloads, id)
	plugin.mutex.Unlock()
	download.cancel()
	download.mutex.Lock()
	defer download.mutex.Unlock()
	download.timer.Stop()
	if download.reader != nil {
		_ = download.reader.Close()
	}
}

func (plugin *plugin) closeDownload(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	id := stringValue(values["downloadId"])
	plugin.mutex.RLock()
	download := plugin.downloads[id]
	plugin.mutex.RUnlock()
	if download != nil {
		if download.connectionID != stringValue(values["connectionId"]) {
			return nil, invalidParams("S3 download belongs to another connection")
		}
		plugin.removeDownload(id, download)
	}
	return map[string]any{"success": true}, nil
}

func (plugin *plugin) closeConnectionDownloads(connectionID string) {
	plugin.mutex.RLock()
	downloads := map[string]*s3Download{}
	for id, download := range plugin.downloads {
		if download.connectionID == connectionID {
			downloads[id] = download
		}
	}
	plugin.mutex.RUnlock()
	for id, download := range downloads {
		plugin.removeDownload(id, download)
	}
}
