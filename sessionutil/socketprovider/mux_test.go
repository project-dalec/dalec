package socketprovider

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// mockSSHServer implements the sshforward.SSHServer interface for testing
type mockSSHServer struct {
	id                 string
	checkAgentFunc     func(context.Context, *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error)
	forwardAgentFunc   func(sshforward.SSH_ForwardAgentServer) error
	checkAgentCalled   bool
	forwardAgentCalled bool
}

func (m *mockSSHServer) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	m.checkAgentCalled = true
	if m.checkAgentFunc != nil {
		return m.checkAgentFunc(ctx, req)
	}
	return &sshforward.CheckAgentResponse{}, nil
}

func (m *mockSSHServer) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	m.forwardAgentCalled = true
	if m.forwardAgentFunc != nil {
		return m.forwardAgentFunc(stream)
	}
	return nil
}

// mockForwardAgentServer implements SSH_ForwardAgentServer for testing
type mockForwardAgentServer struct {
	grpc.ServerStream
	ctx       context.Context
	recvMsgs  []*sshforward.BytesMessage
	sentMsgs  []*sshforward.BytesMessage
	recvIndex int
	recvErr   error
	sendErr   error
}

func (m *mockForwardAgentServer) Context() context.Context {
	return m.ctx
}

func (m *mockForwardAgentServer) Send(msg *sshforward.BytesMessage) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sentMsgs = append(m.sentMsgs, msg)
	return nil
}

func (m *mockForwardAgentServer) Recv() (*sshforward.BytesMessage, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	if m.recvIndex >= len(m.recvMsgs) {
		return nil, fmt.Errorf("no more messages")
	}
	msg := m.recvMsgs[m.recvIndex]
	m.recvIndex++
	return msg, nil
}

func TestNewMux(t *testing.T) {
	// Test creating a new mux without options
	mux := NewMux()
	assert.Assert(t, mux != nil)
	assert.Assert(t, cmp.Len(mux.routes, 0))
	assert.Assert(t, cmp.Len(mux.ordered, 0))
	assert.Assert(t, mux.defaultHandler == nil)

	// Test with options
	handler1 := &mockSSHServer{id: "handler1"}
	handler2 := &mockSSHServer{id: "handler2"}
	defaultHandler := &mockSSHServer{id: "default"}

	mux = NewMux(
		WithMuxRoute("prefix1", handler1),
		WithMuxRoute("prefix2", handler2),
		WithDefaultHandler(defaultHandler),
	)

	assert.Assert(t, mux != nil)
	assert.Assert(t, cmp.Len(mux.routes, 2))
	assert.Assert(t, cmp.Len(mux.ordered, 2))
	assert.Equal(t, mux.defaultHandler, defaultHandler)

	// Check that routes are stored correctly
	assert.Equal(t, mux.routes["prefix1"], handler1)
	assert.Equal(t, mux.routes["prefix2"], handler2)
}

func TestMuxCheckAgent(t *testing.T) {
	// Create multiple handlers to ensure the right one is called
	handler1 := &mockSSHServer{id: "handler1"}
	handler2 := &mockSSHServer{id: "handler2"}
	targetHandler := &mockSSHServer{
		id: "test-handler",
		checkAgentFunc: func(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
			assert.Equal(t, req.ID, "test-id")
			return &sshforward.CheckAgentResponse{}, nil
		},
	}

	mux := NewMux(
		WithMuxRoute("handler1", handler1),
		WithMuxRoute("test", targetHandler),
		WithMuxRoute("handler2", handler2),
	)

	_, err := mux.CheckAgent(context.Background(), &sshforward.CheckAgentRequest{ID: "test-id"})
	assert.NilError(t, err)

	// Verify only the target handler was called
	assert.Assert(t, targetHandler.checkAgentCalled, "Target handler should have been called")
	assert.Assert(t, !handler1.checkAgentCalled, "Handler1 should not have been called")
	assert.Assert(t, !handler2.checkAgentCalled, "Handler2 should not have been called")
}

func TestMuxForwardAgent(t *testing.T) {
	// Create test data for bidirectional communication
	testData1 := []byte("request-data-1")
	testData2 := []byte("response-data-1")
	testData3 := []byte("request-data-2")

	// Create multiple handlers to ensure the right one is called
	handler1 := &mockSSHServer{id: "handler1"}
	handler2 := &mockSSHServer{id: "handler2"}

	targetHandler := &mockSSHServer{
		id: "test-handler",
		forwardAgentFunc: func(stream sshforward.SSH_ForwardAgentServer) error {
			// Test receiving data from client
			msg, err := stream.Recv()
			assert.NilError(t, err)
			assert.DeepEqual(t, testData1, msg.Data)

			// Test sending data to client
			err = stream.Send(&sshforward.BytesMessage{Data: testData2})
			assert.NilError(t, err)

			// Test another round of communication
			msg, err = stream.Recv()
			assert.NilError(t, err)
			assert.DeepEqual(t, testData3, msg.Data)

			return nil
		},
	}

	mux := NewMux(
		WithMuxRoute("handler1", handler1),
		WithMuxRoute("test-id", targetHandler),
		WithMuxRoute("handler2", handler2),
	)

	// Create context with metadata containing the SSH ID
	md := metadata.New(map[string]string{sshforward.KeySSHID: "test-id"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	// Create mock stream with prepared messages
	stream := &mockForwardAgentServer{
		ctx: ctx,
		recvMsgs: []*sshforward.BytesMessage{
			{Data: testData1},
			{Data: testData3},
		},
	}

	err := mux.ForwardAgent(stream)
	assert.NilError(t, err)

	// Verify only the target handler was called
	assert.Assert(t, targetHandler.forwardAgentCalled, "Target handler should have been called")
	assert.Assert(t, !handler1.forwardAgentCalled, "Handler1 should not have been called")
	assert.Assert(t, !handler2.forwardAgentCalled, "Handler2 should not have been called")

	// Verify the response was sent back by the handler
	assert.Assert(t, cmp.Len(stream.sentMsgs, 1))
	assert.DeepEqual(t, testData2, stream.sentMsgs[0].Data)

	// Test with error during stream communication
	recvErrStream := &mockForwardAgentServer{
		ctx:     ctx,
		recvErr: fmt.Errorf("simulated receive error"),
	}

	errHandler := &mockSSHServer{
		id: "err-handler",
		forwardAgentFunc: func(stream sshforward.SSH_ForwardAgentServer) error {
			_, err := stream.Recv()
			return err
		},
	}

	errMux := NewMux(WithMuxRoute("test-id", errHandler))
	err = errMux.ForwardAgent(recvErrStream)
	assert.ErrorContains(t, err, "simulated receive error")

	// Test with error during send
	sendErrStream := &mockForwardAgentServer{
		ctx:      ctx,
		recvMsgs: []*sshforward.BytesMessage{{Data: []byte("test")}},
		sendErr:  fmt.Errorf("simulated send error"),
	}

	sendErrHandler := &mockSSHServer{
		id: "send-err-handler",
		forwardAgentFunc: func(stream sshforward.SSH_ForwardAgentServer) error {
			_, _ = stream.Recv()
			return stream.Send(&sshforward.BytesMessage{Data: []byte("response")})
		},
	}

	sendErrMux := NewMux(WithMuxRoute("test-id", sendErrHandler))
	err = sendErrMux.ForwardAgent(sendErrStream)
	assert.ErrorContains(t, err, "simulated send error")
}

func TestPrefixOrdering(t *testing.T) {
	handler1 := &mockSSHServer{id: "handler1"}
	handler2 := &mockSSHServer{id: "handler2"}
	handler3 := &mockSSHServer{id: "handler3"}

	// The mux should sort prefixes so that longer prefixes are checked first
	mux := NewMux(
		WithMuxRoute("prefix", handler1),
		WithMuxRoute("prefix/longer", handler2),
		WithMuxRoute("a/different/prefix", handler3),
	)

	// This should match "prefix/longer" and not just "prefix"
	h := mux.getHandler("prefix/longer/path")
	assert.Equal(t, h, handler2)
}

func TestMultipleMatchingPrefixes(t *testing.T) {
	// Test that when multiple prefixes could match, the longest one is used
	shortHandler := &mockSSHServer{id: "short"}
	mediumHandler := &mockSSHServer{id: "medium"}
	longHandler := &mockSSHServer{id: "long"}

	mux := NewMux(
		WithMuxRoute("prefix", shortHandler),
		WithMuxRoute("prefix/med", mediumHandler),
		WithMuxRoute("prefix/med/long", longHandler),
	)

	// This should match the longest prefix (prefix/med/long)
	testID := "prefix/med/long/extra"
	h := mux.getHandler(testID)
	assert.Equal(t, h, longHandler)

	// Test with CheckAgent to verify the right handler is called
	_, err := mux.CheckAgent(context.Background(), &sshforward.CheckAgentRequest{ID: testID})
	assert.NilError(t, err)

	assert.Assert(t, longHandler.checkAgentCalled)
	assert.Assert(t, !mediumHandler.checkAgentCalled)
	assert.Assert(t, !shortHandler.checkAgentCalled)
}
