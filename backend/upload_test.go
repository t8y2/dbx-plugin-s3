package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

func newTestPluginWithUpload(t *testing.T) (*plugin, *s3Upload, *io.PipeReader) {
	t.Helper()
	reader, writer := io.Pipe()
	upload := &s3Upload{
		id:        "test-upload",
		channel:   "s3.upload.test-upload",
		writer:    writer,
		cancel:    func() {},
		chunks:    make(chan []byte, uploadChunkSlots),
		done:      make(chan error, 1),
		pumpDone:  make(chan struct{}),
		dead:      make(chan struct{}),
		failures:  make(chan error, 1),
		lastWrite: time.Now(),
	}
	instance := &plugin{
		connections: map[string]*s3Connection{},
		streams:     map[string]*s3Stream{},
		uploads:     map[string]*s3Upload{"test-upload": upload},
	}
	// The real PutObject goroutine always runs shutdown(err) and reports on
	// done before returning; fake object bodies must keep that contract.
	t.Cleanup(func() {
		upload.shutdown(errors.New("test cleanup"))
		_ = reader.Close()
	})
	return instance, upload, reader
}

// startFakeObject runs a fake PutObject against the upload's pipe and starts
// the chunk pump, mirroring the goroutines openUpload launches.
func startFakeObject(upload *s3Upload, reader io.Reader, run func(io.Reader) error) chan struct{} {
	finished := make(chan struct{})
	go func() {
		err := run(reader)
		upload.shutdown(err)
		upload.done <- err
		close(finished)
	}()
	go upload.pump()
	return finished
}

func sendBinaryAsync(instance *plugin, channel string, data []byte) chan *dbxpluginsdk.PluginError {
	outcome := make(chan *dbxpluginsdk.PluginError, 1)
	go func() { outcome <- instance.HandleBinary(channel, data, nil) }()
	return outcome
}

func TestUploadFinishStreamsChunksInOrder(t *testing.T) {
	instance, upload, reader := newTestPluginWithUpload(t)
	var received strings.Builder
	startFakeObject(upload, reader, func(object io.Reader) error {
		_, err := io.Copy(&received, object)
		return err
	})

	for _, chunk := range []string{"aaa", "bbb", "ccc"} {
		if pluginError := instance.HandleBinary(upload.channel, []byte(chunk), nil); pluginError != nil {
			t.Fatalf("HandleBinary(%q) failed: %s", chunk, pluginError.Message)
		}
	}

	result, pluginError := instance.finishUpload(map[string]any{"uploadId": upload.id})
	if pluginError != nil {
		t.Fatalf("finishUpload failed: %s", pluginError.Message)
	}
	if success, ok := result.(map[string]any)["success"].(bool); !ok || !success {
		t.Fatalf("expected success response, got %#v", result)
	}
	if received.String() != "aaabbbccc" {
		t.Fatalf("expected object body aaabbbccc, got %q", received.String())
	}
}

// Regression test for the deadlock in t8y2/dbx-plugin-s3#12: when the object
// reader vanished mid-upload (timeout, network drop), a blocked pipe write in
// HandleBinary used to wedge the plugin's whole frame loop until DBX was
// restarted. It must now return an error promptly.
func TestHandleBinaryErrorsWhenObjectReaderVanishes(t *testing.T) {
	instance, upload, reader := newTestPluginWithUpload(t)
	finished := startFakeObject(upload, reader, func(object io.Reader) error {
		buffer := make([]byte, 3)
		if _, err := io.ReadFull(object, buffer); err != nil {
			return err
		}
		return errors.New("connection reset by peer")
	})

	if pluginError := instance.HandleBinary(upload.channel, []byte("aaa"), nil); pluginError != nil {
		t.Fatalf("first chunk failed: %s", pluginError.Message)
	}
	<-finished
	<-upload.pumpDone
	deadline := time.After(2 * time.Second)
	select {
	case <-deadline:
		t.Fatal("upload did not terminate after the object reader vanished")
	case outcome := <-sendBinaryAsync(instance, upload.channel, []byte("bbb")):
		if outcome == nil {
			t.Fatal("expected an error once the upload is dead")
		}
	}
	if _, pluginError := instance.finishUpload(map[string]any{"uploadId": upload.id}); pluginError == nil {
		t.Fatal("expected finishUpload to fail after the object reader vanished")
	}
}

// Abort must free a HandleBinary sender that is blocked on a full chunk buffer
// while the S3 side is stalled, instead of leaving it stuck forever.
func TestAbortUnblocksBlockedBinarySender(t *testing.T) {
	instance, upload, _ := newTestPluginWithUpload(t)
	cancelled := make(chan struct{})
	upload.cancel = func() { close(cancelled) }
	// Fake a PutObject that is parked mid-part-upload: it reports on done
	// once the upload context is canceled. The pump is intentionally not
	// started so the chunk buffer fills deterministically.
	go func() {
		<-cancelled
		err := errors.New("context canceled")
		upload.shutdown(err)
		upload.done <- err
	}()

	for i := 0; i < uploadChunkSlots; i++ {
		if pluginError := instance.HandleBinary(upload.channel, []byte("xx"), nil); pluginError != nil {
			t.Fatalf("buffered chunk %d failed: %s", i, pluginError.Message)
		}
	}
	blocked := sendBinaryAsync(instance, upload.channel, []byte("yy"))
	select {
	case outcome := <-blocked:
		t.Fatalf("sender should be blocked while the S3 side stalls, got %v", outcome)
	case <-time.After(100 * time.Millisecond):
	}

	abortDone := make(chan struct{})
	go func() {
		if _, pluginError := instance.abortUpload(map[string]any{"uploadId": upload.id}); pluginError != nil {
			t.Errorf("abortUpload failed: %s", pluginError.Message)
		}
		close(abortDone)
	}()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("abortUpload did not return")
	case <-abortDone:
	}
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("blocked HandleBinary sender was not released by abort")
	case outcome := <-blocked:
		if outcome == nil {
			t.Fatal("expected an error from the aborted upload")
		}
	}
}

func TestFinishUploadReportsObjectFailure(t *testing.T) {
	instance, upload, reader := newTestPluginWithUpload(t)
	finished := startFakeObject(upload, reader, func(io.Reader) error {
		return errors.New("put failed")
	})
	<-finished
	<-upload.pumpDone

	_, pluginError := instance.finishUpload(map[string]any{"uploadId": upload.id})
	if pluginError == nil {
		t.Fatal("expected finishUpload to fail")
	}
	if !strings.Contains(pluginError.Message, "put failed") {
		t.Fatalf("expected the PutObject error to surface, got %q", pluginError.Message)
	}
}

func TestWatchIdleCancelsStalledUpload(t *testing.T) {
	originalIdle, originalPoll := uploadIdleTimeout, uploadPollInterval
	uploadIdleTimeout, uploadPollInterval = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() {
		uploadIdleTimeout, uploadPollInterval = originalIdle, originalPoll
	})

	instance, upload, _ := newTestPluginWithUpload(t)
	cancelled := make(chan struct{})
	upload.cancel = func() { close(cancelled) }
	go upload.watchIdle()

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("idle watchdog did not cancel the stalled upload")
	case <-cancelled:
	}
	// In production PutObject returns once the context is canceled and its
	// goroutine runs shutdown; mirror that before asserting.
	upload.shutdown(errors.New("idle timeout"))
	if pluginError := instance.HandleBinary(upload.channel, []byte("zz"), nil); pluginError == nil {
		t.Fatal("HandleBinary must not accept data for a stalled upload after the watchdog fired")
	}
}
