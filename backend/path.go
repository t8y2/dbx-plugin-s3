package main

import (
	"net/url"
	"strings"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

type objectPath struct {
	bucket string
	key    string
}

func parseObjectPath(rawURI string, connection *s3Connection) (objectPath, *dbxpluginsdk.PluginError) {
	if rawURI == "" {
		rawURI = "s3:/"
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "s3" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || strings.ContainsRune(parsed.Path, '\x00') {
		return objectPath{}, invalidParams("Invalid S3 object URI")
	}
	bucket := parsed.Host
	if bucket == "" {
		bucket = connection.bucket
	}
	if connection.bucket != "" && bucket != connection.bucket {
		return objectPath{}, invalidParams("S3 URI bucket does not match the connected bucket")
	}
	key := strings.TrimPrefix(parsed.Path, "/")
	return objectPath{bucket: bucket, key: key}, nil
}

func (connection *s3Connection) remoteKey(localKey string) string {
	if connection.basePath == "" {
		return localKey
	}
	if localKey == "" {
		return connection.basePath + "/"
	}
	return connection.basePath + "/" + localKey
}

func (connection *s3Connection) localKey(remoteKey string) (string, bool) {
	if connection.basePath == "" {
		return remoteKey, true
	}
	prefix := connection.basePath + "/"
	if remoteKey == connection.basePath {
		return "", true
	}
	if !strings.HasPrefix(remoteKey, prefix) {
		return "", false
	}
	return strings.TrimPrefix(remoteKey, prefix), true
}

func (connection *s3Connection) remotePath(path objectPath) objectPath {
	path.key = connection.remoteKey(path.key)
	return path
}
func objectURI(bucket string, key string) string {
	return (&url.URL{Scheme: "s3", Host: bucket, Path: "/" + key}).String()
}
