package main

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const (
	pluginID           = "io.github.t8y2.s3"
	pluginVersion      = "0.1.9"
	filesystemProvider = "io.github.t8y2.s3.files"
	maxInlineBytes     = 4 * 1024 * 1024
	streamChunkBytes   = 256 * 1024
	maxStreamBytes     = 256 * 1024 * 1024
	defaultPageSize    = 200
	maxPageSize        = 1000
	operationTimeout   = 30 * time.Second
	uploadTimeout      = 30 * time.Minute
)

type plugin struct {
	mutex       sync.RWMutex
	connections map[string]*s3Connection
	streams     map[string]*s3Stream
	uploads     map[string]*s3Upload
}

func (plugin *plugin) Handle(
	_ dbxpluginsdk.RequestContext,
	method string,
	params json.RawMessage,
	emitter *dbxpluginsdk.Emitter,
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
		return plugin.disconnect(connectionID)
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
	case "filesystem/stream/open":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.openStream(values, emitter)
	case "filesystem/archive/open":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.openArchive(values, emitter)
	case "filesystem/stream/close":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.closeStream(values)
	case "filesystem/upload/open":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.openUpload(values)
	case "filesystem/upload/finish":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.finishUpload(values)
	case "filesystem/upload/abort":
		if pluginError := requireFilesystemProvider(values); pluginError != nil {
			return nil, pluginError
		}
		return plugin.abortUpload(values)
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

func main() {
	metadata := dbxpluginsdk.Metadata{ID: pluginID, Version: pluginVersion, Capabilities: []string{"connections", "filesystem"}}
	server := dbxpluginsdk.NewServer(metadata, &plugin{connections: map[string]*s3Connection{}, streams: map[string]*s3Stream{}, uploads: map[string]*s3Upload{}}).
		WithTransport(dbxpluginsdk.TransportFramed)
	if err := server.Serve(); err != nil {
		log.Fatal("DBX S3 Sidecar stopped:", err)
	}
}
