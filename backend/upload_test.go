package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
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
		size:      -1,
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

func TestFullQueueRejectsWithoutBlockingControlLoop(test *testing.T) {
	instance, upload, _ := newTestPluginWithUpload(test)
	for index := 0; index < uploadChunkSlots; index++ {
		if pluginError := instance.HandleBinary(upload.channel, []byte("xx"), nil); pluginError != nil {
			test.Fatalf("buffered chunk %d failed: %s", index, pluginError.Message)
		}
	}
	payload := make([]byte, 2+len(upload.channel)+1)
	binary.BigEndian.PutUint16(payload, uint16(len(upload.channel)))
	copy(payload[2:], upload.channel)
	var input bytes.Buffer
	for _, frame := range []struct {
		kind    byte
		payload []byte
	}{
		{1, payload},
		{0, []byte(`{"jsonrpc":"2.0","id":42,"method":"probe","params":{}}`)},
	} {
		header := make([]byte, 5)
		header[0] = frame.kind
		binary.BigEndian.PutUint32(header[1:], uint32(len(frame.payload)))
		input.Write(header)
		input.Write(frame.payload)
	}
	handler := &uploadProbeHandler{plugin: instance, handled: make(chan struct{})}
	server := dbxpluginsdk.NewServer(dbxpluginsdk.Metadata{ID: pluginID}, handler).WithTransport(dbxpluginsdk.TransportFramed).WithIO(&input, io.Discard, io.Discard)
	finished := make(chan error, 1)
	go func() {
		finished <- server.Serve()
	}()
	select {
	case <-time.After(2 * time.Second):
		test.Fatal("full upload queue blocked the SDK control loop")
	case <-handler.handled:
	}
	if err := <-finished; err != nil {
		test.Fatal(err)
	}
	if _, pluginError := instance.uploadStatus(map[string]any{"uploadId": upload.id}); pluginError == nil || !strings.Contains(pluginError.Message, "queue is full") {
		test.Fatalf("expected queue overflow to remain visible: %v", pluginError)
	}
}

type uploadProbeHandler struct {
	*plugin
	handled chan struct{}
}

func (handler *uploadProbeHandler) Handle(_ dbxpluginsdk.RequestContext, _ string, _ json.RawMessage, _ *dbxpluginsdk.Emitter) (any, *dbxpluginsdk.PluginError) {
	close(handler.handled)
	return map[string]any{"success": true}, nil
}

func TestUploadSizeValidation(test *testing.T) {
	for _, value := range []any{-1.0, 1.5, math.NaN(), math.Inf(1), "10", float64(6 * 1024 * 1024 * 1024 * 1024)} {
		if _, pluginError := uploadSize(map[string]any{"size": value}); pluginError == nil {
			test.Fatalf("accepted invalid size: %v", value)
		}
	}
	for _, expected := range []float64{0, 1535186640} {
		if size, pluginError := uploadSize(map[string]any{"size": expected}); pluginError != nil || size != int64(expected) {
			test.Fatalf("size mismatch: %d %v", size, pluginError)
		}
	}
	if size, pluginError := uploadSize(nil); pluginError != nil || size != -1 {
		test.Fatal("legacy unknown-size uploads must remain supported")
	}
}

func TestUploadSealingAndLengthGuards(test *testing.T) {
	instance, upload, reader := newTestPluginWithUpload(test)
	upload.size = 3
	startFakeObject(upload, reader, func(source io.Reader) error { _, err := io.Copy(io.Discard, source); return err })
	if err := upload.seal(); err == nil {
		test.Fatal("accepted a truncated upload")
	}
	if pluginError := instance.HandleBinary(upload.channel, []byte("four"), nil); pluginError == nil {
		test.Fatal("accepted more than the declared size")
	}
	if pluginError := instance.HandleBinary(upload.channel, []byte("abc"), nil); pluginError != nil {
		test.Fatal(pluginError)
	}
	if _, pluginError := instance.uploadStatus(map[string]any{"uploadId": upload.id, "seal": true}); pluginError != nil {
		test.Fatal(pluginError)
	}
	if pluginError := instance.HandleBinary(upload.channel, []byte("x"), nil); pluginError == nil {
		test.Fatal("accepted data after sealing")
	}
	if _, pluginError := instance.finishUpload(map[string]any{"uploadId": upload.id}); pluginError != nil {
		test.Fatal(pluginError)
	}
}

func TestAbortUploadStillCancelsStalledWriter(test *testing.T) {
	instance, upload, reader := newTestPluginWithUpload(test)
	startFakeObject(upload, reader, func(source io.Reader) error { _, err := io.Copy(io.Discard, source); return err })
	if _, pluginError := instance.abortUpload(map[string]any{"uploadId": upload.id}); pluginError != nil {
		test.Fatal(pluginError)
	}
	if pluginError := instance.HandleBinary(upload.channel, []byte("x"), nil); pluginError == nil {
		test.Fatal("accepted bytes after abort")
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
