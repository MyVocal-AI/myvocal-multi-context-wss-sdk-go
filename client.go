package myvocalmultiwss

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type ClientCallbacks struct {
	OnBinaryAudio   func(audio []byte)
	OnClose         func(code int, reason string)
	OnError         func(err error)
	OnCallbackError func(err error)
}

type writeSocket interface {
	WriteJSON(value any) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	Close() error
}

type readSocket interface {
	writeSocket
	ReadMessage() (messageType int, payload []byte, err error)
}

type callbackJob struct {
	run      func()
	critical bool
}

type Client struct {
	apiKey    string
	options   ConnectionOptions
	callbacks ClientCallbacks

	mu             sync.Mutex
	writeMu        sync.Mutex
	socket         writeSocket
	connected      bool
	closing        bool
	contexts       map[string]*Context
	sticky         error
	closeCode      int
	closeDone      chan struct{}
	closeOnce      sync.Once
	controlQueue   chan callbackJob
	controlOnce    sync.Once
	controlPending int32
	binaryQueue    chan func()
	binaryOnce     sync.Once
	binaryPending  int32
}

type Context struct {
	client    *Client
	id        string
	callbacks ContextCallbacks
	done      chan struct{}
	doneOnce  sync.Once
	queue     chan func()
	queueOnce sync.Once
	pending   int32

	mu             sync.Mutex
	closeRequested bool
	final          bool
	terminalError  error
}

func NewClient(
	apiKey string,
	options ConnectionOptions,
	callbacks ClientCallbacks,
) *Client {
	return &Client{
		apiKey:       apiKey,
		options:      options,
		callbacks:    callbacks,
		contexts:     make(map[string]*Context),
		closeCode:    -1,
		closeDone:    make(chan struct{}),
		controlQueue: make(chan callbackJob, 15),
		binaryQueue:  make(chan func(), 64),
	}
}

func (client *Client) Connect(ctx context.Context) error {
	if client.apiKey == "" {
		return &ValidationError{Message: "api_key must not be empty"}
	}
	rawURL, err := client.options.BuildURL()
	if err != nil {
		return err
	}
	client.mu.Lock()
	if client.socket != nil {
		client.mu.Unlock()
		return &StateError{Message: "client is already connected"}
	}
	client.mu.Unlock()

	header := http.Header{}
	header.Set("api-key", client.apiKey)
	conn, response, err := websocket.DefaultDialer.DialContext(
		ctx, rawURL, header)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil &&
			(response.StatusCode == http.StatusUnauthorized ||
				response.StatusCode == http.StatusForbidden) {
			return &AuthenticationError{
				Message: "WebSocket authentication failed",
			}
		}
		return &TransportError{
			Message: "WebSocket connection failed",
			Cause:   err,
		}
	}
	client.attachSocket(conn)
	go client.readLoop(conn)
	return nil
}

func (client *Client) attachSocket(socket writeSocket) {
	client.mu.Lock()
	client.socket = socket
	client.connected = true
	client.closing = false
	client.sticky = nil
	client.mu.Unlock()
	client.controlOnce.Do(func() { go client.controlLoop() })
	client.binaryOnce.Do(func() { go client.binaryLoop() })
}

func (client *Client) OpenContext(
	options ContextOptions,
) (*Context, error) {
	value, err := options.wire()
	if err != nil {
		return nil, err
	}
	if err := client.requireConnected(); err != nil {
		return nil, err
	}
	client.mu.Lock()
	if _, exists := client.contexts[options.ContextID]; exists {
		client.mu.Unlock()
		return nil, &StateError{Message: "context_id already exists"}
	}
	if len(client.contexts) >= 5 {
		client.mu.Unlock()
		return nil, &StateError{
			Message: "maximum active contexts is 5",
		}
	}
	state := &Context{
		client:    client,
		id:        options.ContextID,
		callbacks: options.Callbacks,
		done:      make(chan struct{}),
		queue:     make(chan func(), 65),
	}
	client.contexts[options.ContextID] = state
	client.mu.Unlock()
	state.queueOnce.Do(func() { go state.callbackLoop() })
	if err := client.send(value); err != nil {
		client.removeContext(options.ContextID)
		return nil, err
	}
	return state, nil
}

func (contextState *Context) SendText(
	text string,
	flush bool,
) error {
	if err := contextState.requireActive(); err != nil {
		return err
	}
	value := map[string]any{
		"context_id": contextState.id,
		"text":       text,
	}
	if flush {
		value["flush"] = true
	}
	return contextState.client.send(value)
}

func (contextState *Context) Flush() error {
	if err := contextState.requireActive(); err != nil {
		return err
	}
	return contextState.client.send(map[string]any{
		"context_id": contextState.id,
		"text":       "",
		"flush":      true,
	})
}

func (contextState *Context) KeepAlive() error {
	if err := contextState.requireActive(); err != nil {
		return err
	}
	return contextState.client.send(map[string]any{
		"context_id": contextState.id,
		"text":       "",
	})
}

func (contextState *Context) CloseContext() error {
	contextState.mu.Lock()
	if contextState.final || contextState.terminalError != nil {
		contextState.mu.Unlock()
		return &StateError{Message: "context is terminal"}
	}
	if contextState.closeRequested {
		contextState.mu.Unlock()
		return nil
	}
	contextState.closeRequested = true
	contextState.mu.Unlock()
	return contextState.client.send(map[string]any{
		"context_id":    contextState.id,
		"close_context": true,
	})
}

func (contextState *Context) Finish(ctx context.Context) error {
	if err := contextState.Flush(); err != nil {
		return err
	}
	if err := contextState.CloseContext(); err != nil {
		return err
	}
	return contextState.WaitFinal(ctx)
}

func (contextState *Context) Cancel() error {
	err := contextState.CloseContext()
	if err != nil {
		return err
	}
	contextState.fail(&StateError{Message: "context cancelled"})
	return nil
}

func (contextState *Context) WaitFinal(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return &TimeoutError{
			Message: "timed out waiting for context final",
			Cause:   ctx.Err(),
		}
	case <-contextState.done:
	}
	contextState.mu.Lock()
	defer contextState.mu.Unlock()
	return contextState.terminalError
}

func (contextState *Context) requireActive() error {
	contextState.mu.Lock()
	defer contextState.mu.Unlock()
	if contextState.final || contextState.terminalError != nil ||
		contextState.closeRequested {
		return &StateError{Message: "context is terminal or closing"}
	}
	return nil
}

func (client *Client) CloseSocket(ctx context.Context) error {
	if err := client.stickyError(); err != nil {
		return err
	}
	client.mu.Lock()
	if client.closing {
		client.mu.Unlock()
		return client.waitClosed(ctx)
	}
	if !client.connected || client.socket == nil {
		client.mu.Unlock()
		return &StateError{Message: "client is not connected"}
	}
	client.closing = true
	client.mu.Unlock()
	if err := client.send(map[string]any{"close_socket": true}); err != nil {
		return err
	}
	if err := client.sendNormalCloseControl(); err != nil {
		return err
	}
	return client.waitClosed(ctx)
}

func (client *Client) sendNormalCloseControl() error {
	client.mu.Lock()
	socket := client.socket
	connected := client.connected
	client.mu.Unlock()
	if socket == nil || !connected {
		return &StateError{Message: "client is not connected"}
	}
	client.writeMu.Lock()
	err := socket.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(1000, "client disconnect"),
		time.Now().Add(time.Second),
	)
	client.writeMu.Unlock()
	if err != nil {
		failure := &TransportError{
			Message: "WebSocket close control failed",
			Cause:   err,
		}
		client.failConnection(failure)
		return failure
	}
	return nil
}

func (client *Client) waitClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		client.Abort()
		return &TimeoutError{
			Message: "timed out waiting for socket close",
			Cause:   ctx.Err(),
		}
	case <-client.closeDone:
	}
	if err := client.stickyError(); err != nil {
		return err
	}
	client.mu.Lock()
	code := client.closeCode
	client.mu.Unlock()
	if code != 1000 {
		return &TransportError{
			Message: "WebSocket closed with non-normal code",
		}
	}
	return nil
}

func (client *Client) Abort() {
	client.mu.Lock()
	socket := client.socket
	client.connected = false
	client.mu.Unlock()
	if socket != nil {
		_ = socket.Close()
	}
}

func (client *Client) send(value map[string]any) error {
	if err := client.stickyError(); err != nil {
		return err
	}
	client.mu.Lock()
	socket := client.socket
	connected := client.connected
	client.mu.Unlock()
	if socket == nil || !connected {
		return &StateError{Message: "client is not connected"}
	}
	client.writeMu.Lock()
	err := socket.WriteJSON(value)
	client.writeMu.Unlock()
	if err != nil {
		failure := &TransportError{
			Message: "WebSocket send failed",
			Cause:   err,
		}
		client.failConnection(failure)
		return failure
	}
	return nil
}

func (client *Client) readLoop(socket readSocket) {
	for {
		messageType, payload, err := socket.ReadMessage()
		if err != nil {
			client.finishRead(err)
			return
		}
		if messageType == websocket.BinaryMessage &&
			!looksLikeJSON(payload) {
			client.invokeBinaryAudio(payload)
			continue
		}
		if messageType != websocket.TextMessage &&
			messageType != websocket.BinaryMessage {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
			client.invokeError(&ProtocolError{
				Code:    "INVALID_JSON",
				Message: "server frame was not valid JSON",
			})
			continue
		}
		client.handleEvent(event)
	}
}

func (client *Client) handleEvent(event map[string]any) {
	if protocol := protocolErrorFrom(event); protocol != nil {
		client.mu.Lock()
		state := client.contexts[protocol.ContextID]
		client.mu.Unlock()
		if protocol.ContextID != "" && state != nil {
			state.fail(protocol)
		} else {
			client.failConnection(protocol)
		}
		return
	}
	contextID := stringValue(event["context_id"])
	if contextID == "" {
		contextID = stringValue(event["contextId"])
	}
	if contextID == "" {
		return
	}
	client.mu.Lock()
	state := client.contexts[contextID]
	client.mu.Unlock()
	if state == nil {
		return
	}
	if encoded, ok := event["audio"].(string); ok && encoded != "" {
		audio, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			state.fail(&ProtocolError{
				Code:      "INVALID_AUDIO",
				Message:   "audio was not valid Base64",
				ContextID: contextID,
			})
			return
		}
		state.emitAudio(audio)
	}
	if value, ok := event["timestamps"]; ok && value != nil {
		state.emitTimestamps(value)
	} else if value, ok := event["alignment"]; ok && value != nil {
		state.emitTimestamps(value)
	}
	final, _ := event["is_final"].(bool)
	if alias, ok := event["isFinal"].(bool); !final && ok {
		final = alias
	}
	if final {
		state.complete()
	}
}

func (contextState *Context) emitAudio(audio []byte) {
	contextState.mu.Lock()
	terminal := contextState.final || contextState.terminalError != nil
	contextState.mu.Unlock()
	if terminal {
		return
	}
	callback := contextState.callbacks.OnAudio
	if callback == nil {
		return
	}
	copied := append([]byte(nil), audio...)
	contextState.dispatchData(func() { callback(copied) }, "OnAudio")
}

func (contextState *Context) emitTimestamps(value any) {
	contextState.mu.Lock()
	terminal := contextState.final || contextState.terminalError != nil
	contextState.mu.Unlock()
	if terminal {
		return
	}
	callback := contextState.callbacks.OnTimestamps
	if callback == nil {
		return
	}
	contextState.dispatchData(func() { callback(value) }, "OnTimestamps")
}

func (contextState *Context) complete() {
	contextState.mu.Lock()
	if contextState.final || contextState.terminalError != nil {
		contextState.mu.Unlock()
		return
	}
	contextState.final = true
	contextState.mu.Unlock()
	contextState.dispatchTerminal(func() {
		if callback := contextState.callbacks.OnComplete; callback != nil {
			contextState.safeCallback("OnComplete", callback)
		}
		contextState.client.removeContext(contextState.id)
		contextState.doneOnce.Do(func() { close(contextState.done) })
		close(contextState.queue)
	})
}

func (contextState *Context) fail(err error) {
	contextState.mu.Lock()
	if contextState.final || contextState.terminalError != nil {
		contextState.mu.Unlock()
		return
	}
	contextState.terminalError = err
	contextState.mu.Unlock()
	contextState.dispatchTerminal(func() {
		if callback := contextState.callbacks.OnError; callback != nil {
			contextState.safeCallback("OnError", func() { callback(err) })
		}
		contextState.client.removeContext(contextState.id)
		contextState.doneOnce.Do(func() { close(contextState.done) })
		close(contextState.queue)
	})
}

func (contextState *Context) dispatchData(
	callback func(),
	name string,
) {
	if atomic.LoadInt32(&contextState.pending) >= 64 {
		contextState.fail(&ProtocolError{
			Code:      "CALLBACK_QUEUE_FULL",
			Message:   "context callback queue is full",
			ContextID: contextState.id,
		})
		return
	}
	atomic.AddInt32(&contextState.pending, 1)
	select {
	case contextState.queue <- func() {
		defer atomic.AddInt32(&contextState.pending, -1)
		contextState.safeCallback(name, callback)
	}:
	default:
		atomic.AddInt32(&contextState.pending, -1)
		contextState.fail(&ProtocolError{
			Code:      "CALLBACK_QUEUE_FULL",
			Message:   "context callback queue is full",
			ContextID: contextState.id,
		})
	}
}

func (contextState *Context) dispatchTerminal(callback func()) {
	select {
	case contextState.queue <- callback:
	default:
		contextState.client.invokeCallbackError(&CallbackError{
			Callback: "terminal",
			Cause:    "context callback queue is full",
		})
		contextState.client.removeContext(contextState.id)
		contextState.doneOnce.Do(func() { close(contextState.done) })
	}
}

func (contextState *Context) callbackLoop() {
	for callback := range contextState.queue {
		callback()
	}
}

func (contextState *Context) safeCallback(name string, callback func()) {
	defer func() {
		if failure := recover(); failure != nil {
			contextState.client.invokeCallbackError(&CallbackError{
				Callback: name,
				Cause:    failure,
			})
		}
	}()
	callback()
}

func (client *Client) removeContext(contextID string) {
	client.mu.Lock()
	delete(client.contexts, contextID)
	client.mu.Unlock()
}

func (client *Client) failConnection(err error) {
	client.mu.Lock()
	if client.sticky == nil {
		client.sticky = err
	}
	sticky := client.sticky
	contexts := make([]*Context, 0, len(client.contexts))
	for _, state := range client.contexts {
		contexts = append(contexts, state)
	}
	client.mu.Unlock()
	for _, state := range contexts {
		state.fail(sticky)
	}
	client.invokeError(sticky)
}

func (client *Client) finishRead(readErr error) {
	code := -1
	reason := ""
	var closeErr *websocket.CloseError
	if errors.As(readErr, &closeErr) {
		code = closeErr.Code
		reason = closeErr.Text
	}
	client.mu.Lock()
	client.connected = false
	client.closeCode = code
	closing := client.closing
	unfinished := len(client.contexts) > 0
	client.mu.Unlock()
	if code != 1000 && !closing {
		client.failConnection(&TransportError{
			Message: "WebSocket closed with non-normal code",
			Cause:   readErr,
		})
	} else if code == 1000 && unfinished {
		client.failConnection(&ProtocolError{
			Code:    "MISSING_FINAL",
			Message: "socket closed before every context was final",
		})
	}
	client.invokeClose(code, reason)
	client.closeOnce.Do(func() { close(client.closeDone) })
}

func (client *Client) requireConnected() error {
	if err := client.stickyError(); err != nil {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.socket == nil || !client.connected {
		return &StateError{Message: "client is not connected"}
	}
	if client.closing {
		return &StateError{Message: "socket is closing"}
	}
	return nil
}

func (client *Client) stickyError() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.sticky
}

func (client *Client) invokeBinaryAudio(audio []byte) {
	callback := client.callbacks.OnBinaryAudio
	if callback == nil {
		return
	}
	if atomic.LoadInt32(&client.binaryPending) >= 64 {
		client.failConnection(&ProtocolError{
			Code:    "CALLBACK_QUEUE_FULL",
			Message: "binary audio callback queue is full",
		})
		return
	}
	copied := append([]byte(nil), audio...)
	atomic.AddInt32(&client.binaryPending, 1)
	select {
	case client.binaryQueue <- func() {
		defer atomic.AddInt32(&client.binaryPending, -1)
		defer client.recoverCallback("OnBinaryAudio")
		callback(copied)
	}:
	default:
		atomic.AddInt32(&client.binaryPending, -1)
		client.failConnection(&ProtocolError{
			Code:    "CALLBACK_QUEUE_FULL",
			Message: "binary audio callback queue is full",
		})
	}
}

func (client *Client) invokeError(err error) {
	callback := client.callbacks.OnError
	if callback == nil {
		return
	}
	client.queueControl(func() {
		defer client.recoverCallback("OnError")
		callback(err)
	}, true)
}

func (client *Client) invokeCallbackError(err error) {
	callback := client.callbacks.OnCallbackError
	if callback == nil {
		return
	}
	client.queueControl(func() {
		defer func() { _ = recover() }()
		callback(err)
	}, true)
}

func (client *Client) invokeClose(code int, reason string) {
	callback := client.callbacks.OnClose
	if callback == nil {
		return
	}
	client.queueControl(func() {
		defer client.recoverCallback("OnClose")
		callback(code, reason)
	}, true)
}

func (client *Client) recoverCallback(name string) {
	if failure := recover(); failure != nil {
		client.invokeCallbackError(&CallbackError{
			Callback: name,
			Cause:    failure,
		})
	}
}

func (client *Client) queueControl(callback func(), critical bool) {
	job := callbackJob{run: callback, critical: critical}
	if atomic.LoadInt32(&client.controlPending) >= 16 {
		if !critical {
			return
		}
		select {
		case <-client.controlQueue:
			atomic.AddInt32(&client.controlPending, -1)
		default:
			return
		}
	}
	atomic.AddInt32(&client.controlPending, 1)
	select {
	case client.controlQueue <- job:
	default:
		atomic.AddInt32(&client.controlPending, -1)
	}
}

func (client *Client) pendingControlCallbacks() int {
	return int(atomic.LoadInt32(&client.controlPending))
}

func (client *Client) pendingBinaryCallbacks() int {
	return int(atomic.LoadInt32(&client.binaryPending))
}

func (client *Client) controlLoop() {
	for job := range client.controlQueue {
		job.run()
		atomic.AddInt32(&client.controlPending, -1)
	}
}

func (client *Client) binaryLoop() {
	for callback := range client.binaryQueue {
		callback()
	}
}

func looksLikeJSON(payload []byte) bool {
	for _, value := range payload {
		switch value {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}
