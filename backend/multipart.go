package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/minio/minio-go/v7"
)

type uploadPartReader struct {
	*bytes.Reader
	upload *s3Upload
}

func (reader uploadPartReader) Read(data []byte) (int, error) {
	count, err := reader.Reader.Read(data)
	reader.upload.markWrite()
	return count, err
}

func (upload *s3Upload) putObject(ctx context.Context, client *minio.Client, remote objectPath, reader io.Reader, size int64, options minio.PutObjectOptions) (minio.UploadInfo, error) {
	if size >= 0 && size <= 16*1024*1024 {
		return client.PutObject(ctx, remote.bucket, remote.key, reader, size, options)
	}
	maximumParts, partSize, _, err := minio.OptimalPartInfo(size, options.PartSize)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	core := minio.Core{Client: client}
	uploadID, err := core.NewMultipartUpload(ctx, remote.bucket, remote.key, options)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	completed := false
	defer func() {
		if !completed {
			cleanup, cancel := operationContext()
			defer cancel()
			_ = core.AbortMultipartUpload(cleanup, remote.bucket, remote.key, uploadID)
		}
	}()
	buffer := make([]byte, partSize)
	parts := make([]minio.CompletePart, 0)
	var total int64
	for {
		count, readErr := io.ReadFull(reader, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return minio.UploadInfo{}, readErr
		}
		if count == 0 {
			break
		}
		if len(parts) >= maximumParts {
			return minio.UploadInfo{}, errors.New("S3 upload exceeds the multipart limit")
		}
		checksum := sha256.Sum256(buffer[:count])
		body := uploadPartReader{Reader: bytes.NewReader(buffer[:count]), upload: upload}
		part, err := core.PutObjectPart(ctx, remote.bucket, remote.key, uploadID, len(parts)+1, body, int64(count), minio.PutObjectPartOptions{Sha256Hex: hex.EncodeToString(checksum[:])})
		if err != nil {
			return minio.UploadInfo{}, err
		}
		parts = append(parts, minio.CompletePart{PartNumber: part.PartNumber, ETag: part.ETag})
		total += int64(count)
		if readErr != nil {
			break
		}
	}
	if size >= 0 && total != size {
		return minio.UploadInfo{}, io.ErrUnexpectedEOF
	}
	if len(parts) == 0 {
		return client.PutObject(ctx, remote.bucket, remote.key, bytes.NewReader(nil), 0, options)
	}
	result, err := core.CompleteMultipartUpload(ctx, remote.bucket, remote.key, uploadID, parts, options)
	completed = err == nil
	return result, err
}
