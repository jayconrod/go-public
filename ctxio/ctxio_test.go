package ctxio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestCopy verifies that Copy behaves as described. Since Copy relies on
// several communicating goroutines and a caller that could cancel the context
// at any time, this is difficult to verify, but we do our best.
//
// copyTests contains the test cases, each containing a mock reader and writer
// and a sequence of operations happening in a specific order, even if there
// is no happens-before relationship otherwise established.
func TestCopy(t *testing.T) {
	for _, test := range copyTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if test.skipReason != "" {
				t.Skip(test.skipReason)
			}
			t.Parallel()

			events := make([]syncEvent, len(test.ops))
			for i, op := range test.ops {
				events[i] = syncEvent{actor: op.actor, value: op}
			}
			syn := newSyncer(t, events)

			ctx, cancel := context.WithCancel(context.Background())
			r := newMockFile(readActor, syn, cancel, test.readCloseErr)
			w := newMockFile(writeActor, syn, cancel, test.writeCloseErr)

			n, err := Copy(ctx, w, r)
			if n != test.wantN {
				t.Errorf("copied %d byte(s); want %d", n, test.wantN)
			}
			if test.wantErr == nil && err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if test.wantErr != nil && err == nil {
				t.Errorf("unexpected success: %v", err)
			} else if !errors.Is(test.wantErr, err) {
				t.Errorf("got error %v; want error %v", err, test.wantErr)
			}
			if test.wantReaderClosed != r.isClosed() {
				t.Errorf("reader closed: want %t, got %t", test.wantReaderClosed, r.isClosed())
			}
			if test.wantWriterClosed != w.isClosed() {
				t.Errorf("writer closed: want %t, got %t", test.wantWriterClosed, w.isClosed())
			}
		})
	}
}

func TestCopyCancelBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Copy(ctx, nil, nil); err != ctx.Err() {
		t.Errorf("Copy should return ctx.Err() when called with a canceled context. Got %v", err)
	}
}

var copyTests = []copyTest{
	{
		name: "empty",
		ops: []copyTestOp{
			{actor: readActor, err: io.EOF},
		},
		wantN: 0,
	},
	{
		name: "single_read_write_same_size",
		ops: []copyTestOp{
			{actor: readActor, data: "lorem ipsum"},
			{actor: readActor, err: io.EOF},
			{actor: writeActor, data: "lorem ipsum"},
		},
		wantN: int64(len("lorem ipsum")),
	},
	{
		name: "single_read_eof_write_same_size",
		ops: []copyTestOp{
			{actor: readActor, data: "lorem ipsum", err: io.EOF},
			{actor: writeActor, data: "lorem ipsum"},
		},
		wantN: int64(len("lorem ipsum")),
	},
	{
		name: "small_reads",
		ops: []copyTestOp{
			{actor: readActor, data: "a"},
			{actor: writeActor, data: "a"},
			{actor: readActor, data: "b"},
			{actor: writeActor, data: "b"},
			{actor: readActor, data: "c"},
			{actor: writeActor, data: "c"},
			{actor: readActor, data: "d"},
			{actor: writeActor, data: "d"},
			{actor: readActor, data: "e"},
			{actor: writeActor, data: "e"},
			{actor: readActor, err: io.EOF},
		},
		wantN: 5,
	},
	{
		name: "big_read_small_write",
		ops: []copyTestOp{
			{actor: readActor, data: "abcde"},
			{actor: writeActor, data: "a"},
			{actor: readActor, err: io.EOF},
			{actor: writeActor, data: "b"},
		},
		wantN:   1,
		wantErr: io.ErrShortWrite,
	},
	{
		name: "read_error_at_start",
		ops: []copyTestOp{
			{actor: readActor, err: errTestRead},
		},
		wantErr: errTestRead,
	},
	{
		name: "read_error_with_write",
		ops: []copyTestOp{
			{actor: readActor, data: "abc", err: errTestRead},
			{actor: writeActor, data: "abc"},
		},
		wantN:   int64(len("abc")),
		wantErr: errTestRead,
	},
	{
		name: "read_error_after_write",
		ops: []copyTestOp{
			{actor: readActor, data: "abc"},
			{actor: writeActor, data: "abc"},
			{actor: readActor, err: errTestRead},
		},
		wantN:   int64(len("abc")),
		wantErr: errTestRead,
	},
	{
		name: "write_error",
		ops: []copyTestOp{
			{actor: readActor, data: "abc"},
			{actor: writeActor, err: errTestWrite},
			{actor: readActor, data: "def"},
			{actor: readActor, data: "ghi"},
		},
		wantErr: errTestWrite,
	},
	{
		name: "cancel_while_reading",
		ops: []copyTestOp{
			{actor: readActor, data: "abc", cancel: true},
		},
		wantReaderClosed: true,
		wantWriterClosed: true,
		wantErr:          context.Canceled,
	},
	{
		name: "cancel_while_writing",
		ops: []copyTestOp{
			{actor: readActor, data: "abc"},
			{actor: writeActor, data: "ab", cancel: true},
			{actor: readActor, waitClose: true},
		},
		wantReaderClosed: true,
		wantWriterClosed: true,
		wantN:            2,
		wantErr:          &matchError{"(?s)cancel.*short write"},
	},
	{
		name: "cancel_while_draining_read",
		ops: []copyTestOp{
			{actor: readActor, data: "abc"},
			{actor: readActor, checkpoint: true},
			{actor: writeActor, data: "ab", err: errTestWrite},
			{actor: readActor, err: errReadClosed, cancel: true},
		},
		wantN:            2,
		wantReaderClosed: true,
		wantWriterClosed: false,
		wantErr:          &matchError{"(?s)cancel.*test write error"},
		skipReason:       "for this test to pass, the control thread must be notified of the write error and must take the writeDoneC case. But the read cancellation races with that.",
	},
	{
		name: "cancel_while_draining_write",
		ops: []copyTestOp{
			{actor: readActor, data: "abc"},
			{actor: writeActor, checkpoint: true},
			{actor: readActor, err: errTestRead},
			{actor: writeActor, data: "ab", err: errWriteClosed, cancel: true},
		},
		wantN:            2,
		wantReaderClosed: false,
		wantWriterClosed: true,
		skipReason:       "for this test to pass, the control thread must be notified of the read error and must take the readDoneC case. But the write cancellation races with that.",
	},
	{
		name: "cancel_read_close_error",
		ops: []copyTestOp{
			{actor: readActor, cancel: true, err: errTestReadClose},
		},
		wantReaderClosed: true,
		wantWriterClosed: true,
		wantErr:          &matchError{"(?s)cancel.*test read close"},
	},
	{
		name: "cancel_write_close_error",
		ops: []copyTestOp{
			{actor: readActor, data: "abc"},
			{actor: writeActor, cancel: true, err: errTestWriteClose},
			{actor: readActor, data: "def"},
		},
		wantReaderClosed: true,
		wantWriterClosed: true,
		wantErr:          &matchError{"(?s)cancel.*test write close"},
	},
}

// copyTest describes a test case for TestCopy.
type copyTest struct {
	// name of the test, used in T.Run.
	name string

	// ops is a sequence of read and write operations that happen in
	// a specific order.
	ops []copyTestOp

	// readCloseErr and writeCloseErr are returned by the reader's and
	// writer's Close methods.
	readCloseErr  error
	writeCloseErr error

	// wantN is the expected number of bytes copied.
	wantN int64

	// wantErr is the expected error returned. Matched using errors.Is.
	wantErr error

	// wantReaderClosed and wantWriterClosed say whether the reader's and writer's
	// Close methods should have been called.
	wantReaderClosed bool
	wantWriterClosed bool

	// If skipReason is not empty, the test is skipped with this message.
	skipReason string
}

// copyTestOp is a read or write operation in a fixed sequence.
// Copy acts as if read and write operations run concurrently, but this lets
// us test what happens when they occur in a specific order.
type copyTestOp struct {
	// Either readActor or writeActor.
	actor int

	// For read operations, data to copy into the read buffer.
	// For write operations, data the write buffer must contain.
	data string

	// Error to return from Read or Write.
	err error

	// If set, wait for the next operation from the same actor.
	// Ignore other flags.
	checkpoint bool

	// If set, act as if the context was cancelled while the Read or Write
	// operation was blocked. Call the cancel function, wait for Close, then
	// proceed with data, err.
	cancel bool

	// If set, block until Close is called, then proceed with data, err.
	waitClose bool
}

const (
	readActor = iota
	writeActor
)

func TestCopyN(t *testing.T) {
	for _, test := range []struct {
		name  string
		reads []string
		limit int
	}{
		{
			name: "empty",
		},
		{
			name:  "nothing_to_read",
			limit: 10,
		},
		{
			name:  "empty_reads",
			reads: []string{"", ""},
			limit: 10,
		},
		{
			name:  "equal_read",
			reads: []string{"aaa"},
			limit: 3,
		},
		{
			name:  "equal_read_multiple",
			reads: []string{"a", "a", "a"},
			limit: 3,
		},
		{
			name:  "read_less",
			reads: []string{"abcdef"},
			limit: 3,
		},
		{
			name:  "read_less_multiple",
			reads: []string{"a", "bcd"},
			limit: 3,
		},
		{
			name:  "read_more",
			reads: []string{"abc"},
			limit: 6,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			src := io.NopCloser(&pieceReader{pieces: test.reads})
			sb := &strings.Builder{}
			dst := nopWriteCloser{Writer: sb}
			want := strings.Join(test.reads, "")
			if len(want) > test.limit {
				want = want[:test.limit]
			}

			gotN, err := CopyN(context.Background(), dst, src, int64(test.limit))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gotN != int64(len(want)) {
				t.Errorf("read %d bytes; want %d", gotN, len(want))
			}
			if got := sb.String(); got != want {
				t.Errorf("got %q; want %q", got, want)
			}
		})
	}
}

func TestReadAll(t *testing.T) {
	for _, test := range []struct {
		name  string
		reads []string
	}{
		{
			name: "empty",
		},
		{
			name:  "one",
			reads: []string{"a"},
		},
		{
			name:  "multiple",
			reads: []string{"abc", "def"},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			want := strings.Join(test.reads, "")
			src := io.NopCloser(&pieceReader{test.reads})
			got, err := ReadAll(context.Background(), src)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != want {
				t.Errorf("got %q; want %q", got, want)
			}
		})
	}
}

type pieceReader struct {
	pieces []string
}

func (r *pieceReader) Read(buf []byte) (int, error) {
	if len(r.pieces) == 0 {
		return 0, io.EOF
	}
	n := copy(buf, []byte(r.pieces[0]))
	if n < len(r.pieces[0]) {
		r.pieces[0] = r.pieces[0][n:]
	} else {
		r.pieces = r.pieces[1:]
	}
	return n, nil
}

// Check that we can cancel a Copy operation writing on a socket to a slow peer.
func TestCopyToSocket(t *testing.T) {
	// Start a server in the background. It will accept a connection but
	// never actually read anything from it.
	lis, err := net.Listen("tcp", "localhost:")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	peerStopC := make(chan struct{})
	peerErrC := make(chan error)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			peerErrC <- err
			return
		}
		<-peerStopC
		peerErrC <- conn.Close()
	}()
	defer func() {
		close(peerStopC)
		err := <-peerErrC
		if err != nil {
			t.Error(err)
		}
	}()

	// Connect to the server. Try to send zeroes with a short deadline.
	// We need to send enough to exceed the connection's buffer, which
	// might be a few MB.
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 32<<20)
	buf := bytes.NewBuffer(data)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := Copy(ctx, conn, io.NopCloser(buf)); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected error: %v", err)
	}
}

// Check that we can cancel a Copy operation reading a socket from a slow peer.
func TestCopyFromSocket(t *testing.T) {
	// Start a server in the background. It will accept a connection but never
	// actually send anything to it.
	lis, err := net.Listen("tcp", "localhost:")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	peerStopC := make(chan struct{})
	peerErrC := make(chan error)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			peerErrC <- err
			return
		}
		<-peerStopC
		peerErrC <- conn.Close()
	}()
	defer func() {
		close(peerStopC)
		err := <-peerErrC
		if err != nil {
			t.Error(err)
		}
	}()

	// Connect to the server. Try to read from it with a short deadline.
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	w := nopWriteCloser{bytes.NewBuffer(nil)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := Copy(ctx, w, conn); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected error: %v", err)
	}
}

// Check that we can cancel a Copy operation writing to a pipe with a slow
// peer. A better test might be a file on a slow network share, but a pipe
// has the same interface and is easier to test.
func TestCopyToPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer r.Close()

	data := make([]byte, 32<<20)
	buf := bytes.NewBuffer(data)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := Copy(ctx, w, io.NopCloser(buf)); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected error: %v", err)
	}
}

// Check that we can cancel a Copy operation reading from a pipe with a
// slow peer.
func TestCopyFromPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer r.Close()

	buf := bytes.NewBuffer(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := Copy(ctx, nopWriteCloser{buf}, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected error: %v", err)
	}
}

func BenchmarkCopy(b *testing.B) {
	const size = 1 << 20
	src := bytes.NewBuffer(make([]byte, size))
	dst := bytes.NewBuffer(make([]byte, size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Copy(context.Background(), nopWriteCloser{dst}, io.NopCloser(src))
	}
	b.SetBytes(size)
}

type mockFile struct {
	actor    int
	syn      *syncer
	cancel   func()
	closedC  chan struct{}
	closeErr error
}

func newMockFile(actor int, syn *syncer, cancel func(), closeErr error) *mockFile {
	return &mockFile{
		actor:    actor,
		syn:      syn,
		cancel:   cancel,
		closedC:  make(chan struct{}),
		closeErr: closeErr,
	}
}

func (f *mockFile) Read(buf []byte) (int, error) {
	if f.actor != readActor {
		panic("Read called on Writer")
	}
	if f.isClosed() {
		return 0, errReadClosed
	}

	// Block until the read is supposed to happen.
	op := f.syn.sync(readActor).(copyTestOp)
	for op.checkpoint {
		op = f.syn.sync(readActor).(copyTestOp)
	}
	if op.cancel {
		// op.cancel means the context should be cancelled during the read.
		// Call the cancellation function, then wait for Close to be called.
		f.cancel()
		f.waitClosed()
	} else if op.waitClose {
		f.waitClosed()
	}

	// Return whatever the read is supposed to return.
	if len(buf) < len(op.data) {
		panic(fmt.Sprintf("Read buffer has length %d, operation expects at least %d", len(buf), len(op.data)))
	}
	n := copy(buf, op.data)
	return n, op.err
}

func (f *mockFile) Write(buf []byte) (int, error) {
	if f.actor != writeActor {
		panic("Write called on Reader")
	}
	if f.isClosed() {
		return 0, errWriteClosed
	}

	// Block until the write is supposed to happen.
	op := f.syn.sync(writeActor).(copyTestOp)
	for op.checkpoint {
		op = f.syn.sync(writeActor).(copyTestOp)
	}
	if op.cancel {
		// op.cancel means the context should be cancelled during the write.
		// Call the cancellation function, then wait for Close to be called.
		f.cancel()
		f.waitClosed()
	} else if op.waitClose {
		f.waitClosed()
	}

	n := len(op.data)
	if len(buf) < n {
		panic(fmt.Sprintf("Write buffer has length %d, operation expects at least %d", len(buf), n))
	}
	if string(buf[:n]) != op.data {
		panic(fmt.Sprintf("Write: got %q, want %q", buf[:n], op.data))
	}
	return n, op.err
}

func (f *mockFile) Close() error {
	if f.isClosed() {
		panic("Close called more than once")
	}
	close(f.closedC)
	return f.closeErr
}

func (f *mockFile) isClosed() bool {
	select {
	case <-f.closedC:
		return true
	default:
		return false
	}
}

func (f *mockFile) waitClosed() {
	<-f.closedC
}

var (
	errReadClosed  = errors.New("read closed")
	errWriteClosed = errors.New("write closed")

	errTestRead       = errors.New("test read error")
	errTestWrite      = errors.New("test write error")
	errTestReadClose  = errors.New("test read close")
	errTestWriteClose = errors.New("test write close")
)

type matchError struct {
	want string
}

func (e *matchError) Error() string {
	return fmt.Sprintf("matching %q", e.want)
}

func (e *matchError) Is(err error) bool {
	matched, err := regexp.MatchString(e.want, err.Error())
	if err != nil {
		panic(err)
	}
	return matched
}
