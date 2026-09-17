package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestLocalS3UploadLifecycle(test *testing.T) {
	endpoint := os.Getenv("DBX_S3_LOCAL_ENDPOINT")
	if endpoint == "" {
		test.Skip("set DBX_S3_LOCAL_ENDPOINT to an isolated local MinIO server")
	}
	if !strings.HasPrefix(endpoint, "127.0.0.1:") {
		test.Fatal("local S3 tests only accept an isolated loopback endpoint")
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(os.Getenv("DBX_S3_LOCAL_ACCESS_KEY"), os.Getenv("DBX_S3_LOCAL_SECRET_KEY"), ""), Region: "us-east-1", BucketLookup: minio.BucketLookupPath, MaxRetries: 1})
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bucket := fmt.Sprintf("dbx-issue-test-%d", time.Now().UnixNano())
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		for object := range client.ListObjects(cleanup, bucket, minio.ListObjectsOptions{Recursive: true, WithVersions: true}) {
			if object.Err != nil {
				test.Error(object.Err)
				break
			}
			if err := client.RemoveObject(cleanup, bucket, object.Key, minio.RemoveObjectOptions{VersionID: object.VersionID}); err != nil {
				test.Error(err)
			}
		}
		if err := client.RemoveBucket(cleanup, bucket); err != nil {
			test.Error(err)
		}
	})
	connection := &s3Connection{client: client, bucket: bucket, basePath: "tenant"}
	instance := &plugin{connections: map[string]*s3Connection{"local": connection}}
	largeSize := int64(36*1024*1024 + 333)
	if requested := os.Getenv("DBX_S3_LARGE_TEST_BYTES"); requested != "" {
		largeSize, err = strconv.ParseInt(requested, 10, 64)
		if err != nil || largeSize < 1 {
			test.Fatal("invalid large test size")
		}
	}
	for _, scenario := range []struct {
		name   string
		size   int64
		legacy bool
	}{
		{"small-stream", 512 * 1024, false},
		{"empty-stream", 0, false},
		{"exact-part", 16 * 1024 * 1024, false},
		{"multipart", largeSize, false},
		{"unknown-size", 17*1024*1024 + 1, true},
		{"unknown-empty", 0, true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			started := time.Now()
			uri := objectURI(bucket, "资料/"+scenario.name+".bin")
			params := map[string]any{"connectionId": "local", "uploadId": scenario.name, "uri": uri, "create": true}
			if !scenario.legacy {
				params["size"] = float64(scenario.size)
			}
			result, pluginError := instance.openUpload(params)
			if pluginError != nil {
				test.Fatal(pluginError)
			}
			test.Cleanup(func() { _, _ = instance.abortUpload(params) })
			channel := result.(map[string]any)["channel"].(string)
			chunk := bytes.Repeat([]byte{0x5a}, uploadChunkBytes)
			expectedHash := sha256.New()
			for offset := int64(0); offset < scenario.size; {
				count := int64(len(chunk))
				if count > scenario.size-offset {
					count = scenario.size - offset
				}
				if pluginError := instance.HandleBinary(channel, chunk[:count], nil); pluginError != nil {
					test.Fatal(pluginError)
				}
				_, _ = expectedHash.Write(chunk[:count])
				offset += count
				if offset%(8*uploadChunkBytes) == 0 && offset < scenario.size {
					waitLocalUpload(test, instance, params, func(status map[string]any) bool { return status["processed"].(int64) >= offset })
				}
			}
			params["seal"] = true
			waitLocalUpload(test, instance, params, func(status map[string]any) bool { return status["complete"].(bool) })
			if _, pluginError := instance.finishUpload(params); pluginError != nil {
				test.Fatal(pluginError)
			}
			object, err := client.GetObject(ctx, bucket, "tenant/资料/"+scenario.name+".bin", minio.GetObjectOptions{})
			if err != nil {
				test.Fatal(err)
			}
			defer object.Close()
			actualHash := sha256.New()
			count, err := io.Copy(actualHash, object)
			if err != nil || count != scenario.size || !bytes.Equal(actualHash.Sum(nil), expectedHash.Sum(nil)) {
				test.Fatalf("uploaded data mismatch: bytes=%d error=%v", count, err)
			}
			test.Logf("verified %d bytes and SHA-256 in %s", count, time.Since(started).Round(time.Millisecond))
		})
	}

	test.Run("abort-cleans-multipart", func(test *testing.T) {
		params := map[string]any{"connectionId": "local", "uploadId": "cancel", "uri": objectURI(bucket, "cancel.bin"), "create": true, "size": float64(64 * uploadChunkBytes)}
		opened, pluginError := instance.openUpload(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		test.Cleanup(func() { _, _ = instance.abortUpload(params) })
		chunk := make([]byte, uploadChunkBytes)
		for index := 0; index < 16; index++ {
			if pluginError := instance.HandleBinary(opened.(map[string]any)["channel"].(string), chunk, nil); pluginError != nil {
				test.Fatal(pluginError)
			}
		}
		waitLocalUpload(test, instance, params, func(status map[string]any) bool { return status["processed"].(int64) >= 16*uploadChunkBytes })
		if _, pluginError := instance.abortUpload(params); pluginError != nil {
			test.Fatal(pluginError)
		}
		for object := range client.ListIncompleteUploads(ctx, bucket, "tenant/cancel.bin", true) {
			if object.Err != nil {
				test.Fatal(object.Err)
			}
			test.Fatalf("aborted multipart upload was retained: %s", object.UploadID)
		}
	})

	test.Run("concurrent-create-protection", func(test *testing.T) {
		params := map[string]any{"connectionId": "local", "uploadId": "collision", "uri": objectURI(bucket, "collision.bin"), "create": true, "size": float64(17 * uploadChunkBytes)}
		opened, pluginError := instance.openUpload(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		test.Cleanup(func() { _, _ = instance.abortUpload(params) })
		chunk := make([]byte, uploadChunkBytes)
		for index := 0; index < 17; index++ {
			if pluginError := instance.HandleBinary(opened.(map[string]any)["channel"].(string), chunk, nil); pluginError != nil {
				test.Fatal(pluginError)
			}
			if (index+1)%8 == 0 {
				waitLocalUpload(test, instance, params, func(status map[string]any) bool {
					return status["processed"].(int64) >= int64((index+1)*uploadChunkBytes)
				})
			}
			if index == 15 {
				if _, err := client.PutObject(ctx, bucket, "tenant/collision.bin", strings.NewReader("winner"), 6, minio.PutObjectOptions{}); err != nil {
					test.Fatal(err)
				}
			}
		}
		if _, pluginError := instance.finishUpload(params); pluginError == nil {
			test.Fatal("multipart upload overwrote a concurrently created object")
		}
		object, err := client.GetObject(ctx, bucket, "tenant/collision.bin", minio.GetObjectOptions{})
		if err != nil {
			test.Fatal(err)
		}
		defer object.Close()
		data, err := io.ReadAll(object)
		if err != nil || string(data) != "winner" {
			test.Fatal("concurrent object was not preserved")
		}
	})

	test.Run("versioning", func(test *testing.T) {
		if err := client.SetBucketVersioning(ctx, bucket, minio.BucketVersioningConfiguration{Status: "Enabled"}); err != nil {
			test.Fatal(err)
		}
		params := map[string]any{"connectionId": "local", "uri": objectURI(bucket, "versions.txt"), "create": true, "allowNewVersion": true, "dataBase64": "b2xk"}
		first, pluginError := instance.writeObject(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		params["uploadId"], params["size"] = "version-stream", float64(17*uploadChunkBytes)
		opened, pluginError := instance.openUpload(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		chunk := make([]byte, uploadChunkBytes)
		for index := 0; index < 17; index++ {
			if pluginError := instance.HandleBinary(opened.(map[string]any)["channel"].(string), chunk, nil); pluginError != nil {
				test.Fatal(pluginError)
			}
		}
		second, pluginError := instance.finishUpload(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		firstID, secondID := first.(map[string]any)["versionId"], second.(map[string]any)["versionId"]
		if firstID == "" || secondID == "" || firstID == secondID {
			test.Fatal("duplicate upload did not create a distinct version")
		}
		result, pluginError := instance.listVersions(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		versions := result.(map[string]any)["versions"].([]objectVersion)
		if len(versions) != 2 || versions[0].VersionID != secondID || versions[1].VersionID != firstID {
			test.Fatalf("incorrect versions: %#v", versions)
		}
		if err := client.SetBucketVersioning(ctx, bucket, minio.BucketVersioningConfiguration{Status: "Suspended"}); err != nil {
			test.Fatal(err)
		}
		if _, pluginError := instance.writeObject(params); pluginError == nil {
			test.Fatal("suspended versioning allowed overwrite")
		}
		if _, pluginError := instance.openUpload(params); pluginError == nil {
			test.Fatal("suspended versioning allowed streaming overwrite")
		}
		test.Logf("verified distinct versions: %s, %s; suspended bucket rejects duplicates", firstID, secondID)
	})
}

func waitLocalUpload(test *testing.T, instance *plugin, params map[string]any, ready func(map[string]any) bool) {
	test.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		result, pluginError := instance.uploadStatus(params)
		if pluginError != nil {
			test.Fatal(pluginError)
		}
		if ready(result.(map[string]any)) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	test.Fatal("upload did not make progress")
}
