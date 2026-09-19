package main

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestCloseStreamCancelsDownloadAndReleasesReader(test *testing.T) {
	streamContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	stream := &s3Stream{id: "download-1", connectionID: "connection-1", reader: reader, context: streamContext, cancel: cancel}
	plugin := &plugin{streams: map[string]*s3Stream{stream.id: stream}}
	readDone := make(chan error, 1)
	go func() {
		_, err := reader.Read(make([]byte, 1))
		readDone <- err
	}()
	for attempt := 0; attempt < 2; attempt++ {
		if _, pluginError := plugin.closeStream(map[string]any{"streamId": stream.id}); pluginError != nil {
			test.Fatal(pluginError.Message)
		}
	}
	if streamContext.Err() != context.Canceled || len(plugin.streams) != 0 {
		test.Fatal("download cancellation did not release the context and stream registration")
	}
	select {
	case err := <-readDone:
		if err == nil {
			test.Fatal("cancelled read unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		test.Fatal("download reader remained blocked after cancellation")
	}
}

func TestCloseStreamDoesNotCancelOtherDownloads(test *testing.T) {
	firstContext, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	firstReader, firstWriter := io.Pipe()
	defer firstWriter.Close()
	secondReader, secondWriter := io.Pipe()
	defer secondReader.Close()
	defer secondWriter.Close()
	plugin := &plugin{streams: map[string]*s3Stream{
		"first":  {id: "first", reader: firstReader, context: firstContext, cancel: cancelFirst},
		"second": {id: "second", reader: secondReader, context: secondContext, cancel: cancelSecond},
	}}
	if _, pluginError := plugin.closeStream(map[string]any{"streamId": "first"}); pluginError != nil {
		test.Fatal(pluginError.Message)
	}
	if firstContext.Err() != context.Canceled || secondContext.Err() != nil || plugin.streams["second"] == nil {
		test.Fatal("cancelling one download affected another stream")
	}
}
