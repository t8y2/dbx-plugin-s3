package main

import (
	"context"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

func writeOptions(ctx context.Context, connection *s3Connection, path objectPath, values map[string]any) (minio.PutObjectOptions, *dbxpluginsdk.PluginError) {
	options := minio.PutObjectOptions{ContentType: stringValue(values["contentType"])}
	if options.ContentType == "" {
		options.ContentType = "application/octet-stream"
	}
	remote := connection.remotePath(path)
	metadata, err := connection.client.StatObject(ctx, remote.bucket, remote.key, minio.StatObjectOptions{})
	exists := err == nil
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.StatusCode != 404 && response.Code != "NoSuchKey" && response.Code != "NotFound" {
			return options, remoteError("S3 object check failed: " + err.Error())
		}
	}
	create, overwrite := boolValue(values["create"]), boolValue(values["overwrite"])
	etag := stringValue(values["etag"])
	if etag != "" && (!exists || metadata.ETag != etag) {
		return options, remoteError("S3 object changed before write")
	}
	if exists && !overwrite && boolValue(values["allowNewVersion"]) {
		configuration, versionErr := connection.client.GetBucketVersioning(ctx, remote.bucket)
		if versionErr == nil && len(configuration.ExcludedPrefixes) > 0 {
			return options, remoteError("S3 object already exists; automatic version uploads are disabled for buckets with versioning exclusions")
		}
		// A failed versioning probe (S3-compatible endpoints without the S3
		// versioning API, or denied permissions) must not bury the
		// already-exists rejection: fall through so the caller can offer an
		// explicit overwrite instead.
		overwrite = versionErr == nil && configuration.Status == "Enabled"
	}
	if exists && !overwrite {
		return options, remoteError("S3 object already exists: " + path.key)
	}
	if !exists && !create {
		return options, remoteError("S3 object does not exist: " + path.key)
	}
	if etag != "" {
		options.SetMatchETag(etag)
	}
	// Deliberately no If-None-Match guard for fresh objects: some S3-compatible
	// services (Aliyun OSS among them) mishandle the header, and the StatObject
	// probe above already enforces create semantics.
	return options, nil
}

type objectVersion struct {
	VersionID    string `json:"versionId"`
	Size         int64  `json:"size"`
	Modified     string `json:"modified"`
	IsLatest     bool   `json:"isLatest"`
	DeleteMarker bool   `json:"deleteMarker"`
}

func (plugin *plugin) listVersions(values map[string]any) (any, *dbxpluginsdk.PluginError) {
	ctx, cancel := operationContext()
	defer cancel()
	connection, pluginError := plugin.connectionFor(values)
	if pluginError != nil {
		return nil, pluginError
	}
	path, pluginError := parseObjectPath(stringValue(values["uri"]), connection)
	if pluginError != nil || path.key == "" || strings.HasSuffix(path.key, "/") {
		return nil, invalidParams("S3 versions require a file URI")
	}
	remote := connection.remotePath(path)
	versions := make([]objectVersion, 0)
	truncated := false
	for object := range connection.client.ListObjects(ctx, remote.bucket, minio.ListObjectsOptions{Prefix: remote.key, Recursive: true, WithVersions: true, MaxKeys: 100}) {
		if object.Err != nil {
			return nil, remoteError("S3 version listing failed: " + object.Err.Error())
		}
		if object.Key != remote.key {
			break
		}
		if len(versions) == maxPageSize {
			truncated = true
			break
		}
		versions = append(versions, objectVersion{VersionID: object.VersionID, Size: object.Size, Modified: object.LastModified.UTC().Format(time.RFC3339), IsLatest: object.IsLatest, DeleteMarker: object.IsDeleteMarker})
	}
	return map[string]any{"versions": versions, "truncated": truncated}, nil
}
