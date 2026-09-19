package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type s3Connection struct {
	client   *minio.Client
	bucket   string
	region   string
	endpoint string
	basePath string
	readOnly bool
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
	readOnly     bool
	dualstack    bool
	bucketLookup minio.BucketLookupType
}

func parseConnection(values map[string]any) (connectionConfig, *dbxpluginsdk.PluginError) {
	connection, _ := values["connection"].(map[string]any)
	result := connectionConfig{
		id:        stringValue(connection["id"]),
		bucket:    emptyIfNull(stringValue(connection["database"])),
		accessKey: stringValue(connection["username"]),
		// The host marks the connection read-only in DBX and serializes the
		// flag into the connect payload; every mutating method checks it.
		readOnly: boolValue(connection["read_only"]) || boolValue(connection["readOnly"]),
	}
	secrets, _ := connection["connection_secrets"].(map[string]any)
	result.secretKey = stringValue(secrets["secret_key"])
	result.sessionToken = emptyIfNull(stringValue(secrets["session_token"]))
	config, _ := connection["external_config"].(map[string]any)
	result.dualstack = boolValue(config["aws_dualstack"]) || strings.EqualFold(strings.TrimSpace(stringValue(config["aws_dualstack"])), "true")
	result.endpoint = stringValue(config["endpoint"])
	result.region = strings.TrimSpace(stringValue(config["region"]))
	if result.region == "" {
		result.region = "us-east-1"
	}
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
		"id": result.id, "access key": result.accessKey,
		"secret key": result.secretKey, "endpoint": result.endpoint,
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

// emptyIfNull rescues optional fields the host persisted as the literal
// string "null" when they had no explicit default; " null " padding is
// treated the same way.
func emptyIfNull(value string) string {
	value = strings.TrimSpace(value)
	if value == "null" {
		return ""
	}
	return value
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
	client.SetS3EnableDualstack(config.dualstack)
	return &s3Connection{client: client, bucket: config.bucket, region: config.region, endpoint: config.endpoint, basePath: config.basePath, readOnly: config.readOnly}, nil
}

// requireWritable rejects mutating methods on connections the user marked
// read-only in DBX. The host itself does not gate plugin API calls, so the
// sidecar is the enforcement point.
func requireWritable(connection *s3Connection) *dbxpluginsdk.PluginError {
	if connection.readOnly {
		return remoteError("Connection is read-only: the connection is marked read-only in DBX, so uploads, deletes, renames, and folder creation are disabled")
	}
	return nil
}

func verifyConnection(config connectionConfig) *dbxpluginsdk.PluginError {
	connection, pluginError := createConnection(config)
	if pluginError != nil {
		return pluginError
	}
	return verifyBucket(connection)
}

func verifyBucket(connection *s3Connection) *dbxpluginsdk.PluginError {
	context, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if connection.bucket == "" {
		if _, err := connection.client.ListBuckets(context); err != nil {
			return remoteError("S3 bucket listing failed: " + err.Error())
		}
		return nil
	}
	exists, err := connection.client.BucketExists(context, connection.bucket)
	if err != nil {
		if listErr := verifyBucketByListing(context, connection); listErr == nil {
			return nil
		}
		return remoteError("S3 bucket check failed: " + err.Error())
	}
	if !exists {
		if listErr := verifyBucketByListing(context, connection); listErr == nil {
			return nil
		}
		return remoteError("S3 bucket does not exist: " + connection.bucket)
	}
	return nil
}

func verifyBucketByListing(context context.Context, connection *s3Connection) error {
	objects := connection.client.ListObjects(context, connection.bucket, minio.ListObjectsOptions{
		Recursive: false,
		MaxKeys:   1,
	})
	for object := range objects {
		if object.Err != nil {
			return object.Err
		}
		return nil
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

func (plugin *plugin) disconnect(connectionID string) (any, *dbxpluginsdk.PluginError) {
	plugin.closeConnectionDownloads(connectionID)
	plugin.mutex.Lock()
	delete(plugin.connections, connectionID)
	streams := make([]*s3Stream, 0)
	uploads := make([]*s3Upload, 0)
	for streamID, stream := range plugin.streams {
		if stream.connectionID == connectionID {
			delete(plugin.streams, streamID)
			streams = append(streams, stream)
		}
	}
	for uploadID, upload := range plugin.uploads {
		if upload.connectionID == connectionID {
			delete(plugin.uploads, uploadID)
			uploads = append(uploads, upload)
		}
	}
	plugin.mutex.Unlock()
	for _, stream := range streams {
		stream.cancel()
		_ = stream.reader.Close()
	}
	for _, upload := range uploads {
		upload.shutdown(errors.New("S3 connection disconnected"))
		if upload.expiry != nil {
			upload.expiry.Stop()
		}
		upload.cancel()
		<-upload.done
	}
	return map[string]any{"success": true}, nil
}
