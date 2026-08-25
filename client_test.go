package myvocalmultiwss

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeSocket struct {
	mu             sync.Mutex
	writes         []map[string]any
	activeWrites   int32
	concurrentSeen int32
	closed         bool
	closeCodes     []int
}

func (socket *fakeSocket) WriteJSON(value any) error {
	if atomic.AddInt32(&socket.activeWrites, 1) > 1 {
		atomic.StoreInt32(&socket.concurrentSeen, 1)
	}
	time.Sleep(time.Millisecond)
	payload, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(payload, &cloned)
	socket.mu.Lock()
	socket.writes = append(socket.writes, cloned)
	socket.mu.Unlock()
	atomic.AddInt32(&socket.activeWrites, -1)
	return nil
}

func (socket *fakeSocket) Close() error {
	socket.closed = true
	return nil
}

func (socket *fakeSocket) WriteControl(
	messageType int,
	data []byte,
	deadline time.Time,
) error {
	if messageType == websocket.CloseMessage {
		code, _ := parseCloseFrame(data)
		socket.mu.Lock()
		socket.closeCodes = append(socket.closeCodes, code)
		socket.mu.Unlock()
	}
	return nil
}

func (socket *fakeSocket) snapshot() []map[string]any {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	result := make([]map[string]any, len(socket.writes))
	copy(result, socket.writes)
	return result
}

func (socket *fakeSocket) closeSnapshot() []int {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	result := make([]int, len(socket.closeCodes))
	copy(result, socket.closeCodes)
	return result
}

func newAttachedClient(t *testing.T, callbacks ClientCallbacks) (*Client, *fakeSocket) {
	t.Helper()
	client := NewClient(
		"test-key",
		DefaultConnectionOptions("voice"),
		callbacks,
	)
	socket := &fakeSocket{}
	client.attachSocket(socket)
	return client, socket
}

func TestFinishSendsFlushThenCloseAndWaitsForFinal(t *testing.T) {
	client, socket := newAttachedClient(t, ClientCallbacks{})
	ctx, err := client.OpenContext(ContextOptions{ContextID: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		wait, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- ctx.Finish(wait)
	}()

	deadline := time.Now().Add(time.Second)
	for len(socket.snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	writes := socket.snapshot()
	if len(writes) != 3 {
		t.Fatalf("got %d writes, want init+flush+close", len(writes))
	}
	if writes[1]["flush"] != true {
		t.Fatalf("second message was not flush: %#v", writes[1])
	}
	if writes[2]["close_context"] != true {
		t.Fatalf("third message was not close_context: %#v", writes[2])
	}
	client.handleEvent(map[string]any{
		"context_id": "ctx",
		"audio":      "",
		"isFinal":    true,
	})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCloseSocketSendsNormalCloseControlAndWaitsForPeer(t *testing.T) {
	client, socket := newAttachedClient(t, ClientCallbacks{})
	done := make(chan error, 1)
	go func() {
		wait, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- client.CloseSocket(wait)
	}()

	deadline := time.Now().Add(time.Second)
	for len(socket.closeSnapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	codes := socket.closeSnapshot()
	if len(codes) != 1 || codes[0] != 1000 {
		t.Fatalf("close control codes=%v, want [1000]", codes)
	}
	client.finishRead(&websocket.CloseError{Code: 1000})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func parseCloseFrame(data []byte) (int, string) {
	if len(data) < 2 {
		return -1, ""
	}
	code := int(data[0])<<8 | int(data[1])
	return code, string(data[2:])
}

func TestSlowCallbackDoesNotBlockAnotherContext(t *testing.T) {
	client, _ := newAttachedClient(t, ClientCallbacks{})
	started := make(chan struct{})
	release := make(chan struct{})
	fastDone := make(chan struct{})
	slow, _ := client.OpenContext(ContextOptions{
		ContextID: "slow",
		Callbacks: ContextCallbacks{
			OnAudio: func([]byte) {
				close(started)
				<-release
			},
		},
	})
	_, _ = slow, release
	_, err := client.OpenContext(ContextOptions{
		ContextID: "fast",
		Callbacks: ContextCallbacks{
			OnComplete: func() { close(fastDone) },
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	client.handleEvent(map[string]any{
		"context_id": "slow",
		"audio":      "AQI=",
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow callback did not start")
	}
	client.handleEvent(map[string]any{
		"context_id": "fast",
		"is_final":   true,
	})
	select {
	case <-fastDone:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("slow callback blocked another context")
	}
	close(release)
}

func TestCallbackPanicIsReportedWithoutChangingSuccess(t *testing.T) {
	callbackErrors := make(chan error, 1)
	client, _ := newAttachedClient(t, ClientCallbacks{
		OnCallbackError: func(err error) { callbackErrors <- err },
	})
	ctx, err := client.OpenContext(ContextOptions{
		ContextID: "ctx",
		Callbacks: ContextCallbacks{
			OnAudio: func([]byte) { panic("customer callback failed") },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.handleEvent(map[string]any{
		"context_id": "ctx",
		"audio":      "AQI=",
		"is_final":   true,
	})
	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ctx.WaitFinal(wait); err != nil {
		t.Fatal(err)
	}
	select {
	case <-callbackErrors:
	case <-time.After(time.Second):
		t.Fatal("callback panic was not reported")
	}
}

func TestContextErrorIsStickyAndIsolated(t *testing.T) {
	client, _ := newAttachedClient(t, ClientCallbacks{})
	failed, _ := client.OpenContext(ContextOptions{ContextID: "failed"})
	healthy, _ := client.OpenContext(ContextOptions{ContextID: "healthy"})

	client.handleEvent(map[string]any{
		"context_id": "failed",
		"error":      "INVALID_REQUEST",
		"message":    "bad context",
	})
	client.handleEvent(map[string]any{
		"context_id": "failed",
		"is_final":   true,
	})
	client.handleEvent(map[string]any{
		"context_id": "healthy",
		"is_final":   true,
	})

	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var protocol *ProtocolError
	if err := failed.WaitFinal(wait); !errors.As(err, &protocol) ||
		protocol.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected sticky error: %#v", err)
	}
	if err := healthy.WaitFinal(wait); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionErrorFailsEveryContext(t *testing.T) {
	client, _ := newAttachedClient(t, ClientCallbacks{})
	first, _ := client.OpenContext(ContextOptions{ContextID: "one"})
	second, _ := client.OpenContext(ContextOptions{ContextID: "two"})
	client.handleEvent(map[string]any{
		"error":   "UPSTREAM_UNAVAILABLE",
		"message": "temporary",
	})
	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if first.WaitFinal(wait) == nil || second.WaitFinal(wait) == nil {
		t.Fatal("connection error did not fail every context")
	}
}

func TestWritesAreSerialized(t *testing.T) {
	client, socket := newAttachedClient(t, ClientCallbacks{})
	ctx, _ := client.OpenContext(ContextOptions{ContextID: "ctx"})
	var wait sync.WaitGroup
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = ctx.SendText("hello", false)
		}()
	}
	wait.Wait()
	if atomic.LoadInt32(&socket.concurrentSeen) != 0 {
		t.Fatal("socket writes overlapped")
	}
}

func TestControlCallbackStormIsBounded(t *testing.T) {
	client, _ := newAttachedClient(t, ClientCallbacks{})
	release := make(chan struct{})
	for i := 0; i < 100; i++ {
		client.queueControl(func() { <-release }, false)
	}
	if client.pendingControlCallbacks() > 16 {
		t.Fatalf("pending callbacks=%d", client.pendingControlCallbacks())
	}
	close(release)
}

func TestErrorShapesAreTerminal(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		code    string
	}{
		{"string", map[string]any{"error": "STRING_ERROR"}, "STRING_ERROR"},
		{"code", map[string]any{"code": "CODE_ERROR"}, "CODE_ERROR"},
		{"error_code", map[string]any{"error_code": "ERROR_CODE"}, "ERROR_CODE"},
		{
			"nested",
			map[string]any{
				"error": map[string]any{
					"code":    "NESTED_ERROR",
					"message": "nested",
				},
			},
			"NESTED_ERROR",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newAttachedClient(t, ClientCallbacks{})
			state, err := client.OpenContext(
				ContextOptions{ContextID: "ctx"})
			if err != nil {
				t.Fatal(err)
			}
			test.payload["context_id"] = "ctx"
			client.handleEvent(test.payload)
			wait, cancel := context.WithTimeout(
				context.Background(), time.Second)
			defer cancel()
			var protocol *ProtocolError
			if err := state.WaitFinal(wait); !errors.As(err, &protocol) ||
				protocol.Code != test.code {
				t.Fatalf("unexpected protocol error: %#v", err)
			}
		})
	}
}

func TestDataCallbackStormIsBounded(t *testing.T) {
	release := make(chan struct{})
	client, _ := newAttachedClient(t, ClientCallbacks{})
	state, err := client.OpenContext(ContextOptions{
		ContextID: "ctx",
		Callbacks: ContextCallbacks{
			OnAudio: func([]byte) { <-release },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		client.handleEvent(map[string]any{
			"context_id": "ctx",
			"audio":      "AQI=",
		})
	}
	if atomic.LoadInt32(&state.pending) > 64 {
		t.Fatalf("pending data callbacks=%d", state.pending)
	}
	close(release)
	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var protocol *ProtocolError
	if err := state.WaitFinal(wait); !errors.As(err, &protocol) ||
		protocol.Code != "CALLBACK_QUEUE_FULL" {
		t.Fatalf("unexpected terminal error: %#v", err)
	}
}

func TestBinaryAudioCallbackStormIsBoundedAndNotSilentlyDropped(t *testing.T) {
	release := make(chan struct{})
	client, _ := newAttachedClient(t, ClientCallbacks{
		OnBinaryAudio: func([]byte) { <-release },
	})
	for i := 0; i < 100; i++ {
		client.invokeBinaryAudio([]byte{1, 2})
	}
	if client.pendingBinaryCallbacks() > 64 {
		t.Fatalf("pending binary callbacks=%d", client.pendingBinaryCallbacks())
	}
	var protocol *ProtocolError
	if err := client.stickyError(); !errors.As(err, &protocol) ||
		protocol.Code != "CALLBACK_QUEUE_FULL" {
		t.Fatalf("binary callback overflow was silent: %#v", err)
	}
	close(release)
}

func TestLateAudioAfterFinalIsIgnored(t *testing.T) {
	completeStarted := make(chan struct{})
	releaseComplete := make(chan struct{})
	var audioCallbacks int32
	client, _ := newAttachedClient(t, ClientCallbacks{})
	state, err := client.OpenContext(ContextOptions{
		ContextID: "ctx",
		Callbacks: ContextCallbacks{
			OnAudio: func([]byte) {
				atomic.AddInt32(&audioCallbacks, 1)
			},
			OnComplete: func() {
				close(completeStarted)
				<-releaseComplete
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.handleEvent(map[string]any{
		"context_id": "ctx",
		"is_final":   true,
	})
	select {
	case <-completeStarted:
	case <-time.After(time.Second):
		t.Fatal("complete callback did not start")
	}
	client.handleEvent(map[string]any{
		"context_id": "ctx",
		"audio":      "AQI=",
	})
	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&audioCallbacks) != 0 {
		t.Fatal("late audio callback was delivered")
	}
	close(releaseComplete)
	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := state.WaitFinal(wait); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&audioCallbacks) != 0 {
		t.Fatal("late audio callback ran after completion")
	}
}
