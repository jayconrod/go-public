// Package ctxio contains utility functions for performing input and output
// operations that can be cancelled via context.
package ctxio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

// Copy is like io.Copy, but it can be cancelled via context.
//
// rc.Read and wc.Write may block indefinitely. However, both methods must
// return quickly after Close is called. Copy calls either rc.Close or wc.Close
// or both after it detects that the context has been cancelled. Copy does not
// call Close otherwise.
//
// Copy returns the number of bytes copied and an error. The error may be
// an error returned from wc.Write, wc.Close, rc.Read, rc.Close, or ctx.Err,
// or it may contain messages from those methods. If Copy observes that the
// context was cancelled, then errors.Is(err, ctx.Err()) is true.
func Copy(ctx context.Context, dst io.WriteCloser, src io.ReadCloser) (written int64, err error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	// Allocate two reusable buffers. The read and write threads swap these
	// back and forth. We use two buffers so they can work in parallel.
	const bufSize = 8192
	fullC := make(chan []byte, 1)
	fullC <- make([]byte, 0, bufSize)
	emptyC := make(chan []byte, 1)
	emptyC <- make([]byte, 0, bufSize)

	// Read thread.
	var bytesRead int64
	var readErr error
	cancelC := make(chan struct{})
	readDoneC := make(chan struct{})
	go func() {
		defer close(readDoneC)
		for {
			// Receive an empty buffer, resize it to its capacity.
			// This may block if the write thread is slow.
			var buf []byte
			select {
			case <-cancelC:
				return
			case buf = <-emptyC:
			}
			buf = buf[:bufSize]

			// Do the Read operation. This may block if the Reader is slow.
			var nr int
			nr, readErr = src.Read(buf)
			bytesRead += int64(nr)

			// Check for cancellation. If the reader was closed due to cancellation,
			// we should not send partially read bytes to the write thread.
			// We check ctx.Err() directly: cancelC may not be closed yet.
			if ctx.Err() != nil {
				return
			}

			// Resize the buffer to the number of bytes read,
			// send to the write thread.
			buf = buf[:nr]
			select {
			case <-cancelC:
				return
			case fullC <- buf:
			}

			// Break if there was an error, including io.EOF.
			if readErr != nil {
				return
			}
		}
	}()

	// Write thread.
	var writeErr error
	writeDoneC := make(chan struct{})
	go func() {
		defer close(writeDoneC)
		for {
			// Receive a full buffer from the read thread. This may block
			// if the read thread is slow.
			var buf []byte
			var ok bool
			select {
			case <-cancelC:
				return
			case buf, ok = <-fullC:
				if !ok {
					return
				}
			}

			// Do the Write operation. Skip empty writes. We start with an empty
			// buffer, but Read is also allowed to return 0, nil. This may block
			// if the Writer is slow.
			var nw int
			if len(buf) > 0 {
				nw, writeErr = dst.Write(buf)
			}
			written += int64(nw)
			if nw < len(buf) && writeErr == nil {
				writeErr = io.ErrShortWrite
			}

			// Resize the buffer to 0 length, and send it back to the read thread.
			// This may block if the read thread is slow.
			buf = buf[:0]
			select {
			case <-cancelC:
				return
			case emptyC <- buf:
			}

			// Break if there was an error.
			if writeErr != nil {
				return
			}
		}
	}()

	var readCloseErr, writeCloseErr error
	select {
	case <-ctx.Done():
		// Context cancelled. Close rc and wc, wait for other threads.
		readCloseErr = src.Close()
		writeCloseErr = dst.Close()
		close(cancelC)
		<-readDoneC
		<-writeDoneC

	case <-writeDoneC:
		// Write thread finished before read thread. This indicates an error.
		// To let the read thread finish, we close cancelC and drain fullC.
		close(cancelC)
	drainRead:
		for {
			select {
			case <-ctx.Done():
				// Context cancelled while draining.
				readCloseErr = src.Close()
				break drainRead

			case <-fullC:
				// Discard buffer, continue draining.

			case <-readDoneC:
				break drainRead
			}
		}
		<-readDoneC

	case <-readDoneC:
		// Read thread finished, either successfully or due to some other error.
		// We need to let the write thread finish though. We close fullC to tell
		// it there's no more data, and we drain emptyC.
		close(fullC)
	drainWrite:
		for {
			select {
			case <-ctx.Done():
				// Context cancelled while draining.
				writeCloseErr = dst.Close()
				close(cancelC)
				break drainWrite

			case <-emptyC:
				// Discard buffer, continue draining.

			case <-writeDoneC:
				break drainWrite
			}
		}
		<-writeDoneC
	}

	if readErr == io.EOF {
		readErr = nil
	}
	errs := [...]error{ctx.Err(), writeCloseErr, readCloseErr, writeErr, readErr}
	errCount := 0
	for _, err := range errs {
		if err != nil {
			errCount++
		}
	}
	if errCount > 1 {
		return written, &copyError{
			ctxError:        ctx.Err(),
			writeCloseError: writeCloseErr,
			readCloseError:  readCloseErr,
			writeError:      writeErr,
			readError:       readErr,
		}
	}
	for _, err := range errs {
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// CopyN is like Copy, but it reads no more than n bytes from src.
func CopyN(ctx context.Context, dst io.WriteCloser, src io.ReadCloser, n int64) (written int64, err error) {
	src = &limitReadCloser{ReadCloser: src, limit: n}
	return Copy(ctx, dst, src)
}

type limitReadCloser struct {
	io.ReadCloser
	limit int64
}

func (lrc *limitReadCloser) Read(buf []byte) (int, error) {
	if lrc.limit == 0 {
		return 0, io.EOF
	}
	if int64(len(buf)) > lrc.limit {
		buf = buf[:int(lrc.limit)]
	}
	n, err := lrc.ReadCloser.Read(buf)
	lrc.limit -= int64(n)
	if lrc.limit == 0 && err == nil {
		err = io.EOF
	}
	return n, err
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error {
	return nil
}

// ReadAll is like io.ReadAll, but the operation may be interrupted when
// the context is closed. src.Close must be safe to call concurrently with
// src.Read, and it must cause src.Read to return quickly. See Copy for
// other constraints.
func ReadAll(ctx context.Context, src io.ReadCloser) ([]byte, error) {
	buf := &bytes.Buffer{}
	dst := nopWriteCloser{buf}
	_, err := Copy(ctx, dst, src)
	return buf.Bytes(), err
}

// copyError is reported by Copy when multiple errors occur.
// For example, a context cancellation causes the reader and writer
// to be closed, which may cause other errors from Read and Write.
type copyError struct {
	ctxError, writeCloseError, readCloseError, writeError, readError error
}

func (e *copyError) Error() string {
	sb := &strings.Builder{}
	sep := ""
	if e.ctxError != nil {
		fmt.Fprintf(sb, "%scontext error during copy operation: %v", sep, e.ctxError)
		sep = "\n\t"
	}
	if e.writeCloseError != nil {
		fmt.Fprintf(sb, "%serror closing writer when cancelling copy operation: %v", sep, e.writeCloseError)
		sep = "\n\t"
	}
	if e.readCloseError != nil {
		fmt.Fprintf(sb, "%serror closing reader when cancelling copy operation: %v", sep, e.readCloseError)
		sep = "\n\t"
	}
	if e.writeError != nil {
		fmt.Fprintf(sb, "%serror writing during copy operation: %v", sep, e.writeError)
		sep = "\n\t"
	}
	if e.readError != nil {
		fmt.Fprintf(sb, "%serror reading during copy operation: %v", sep, e.readError)
		sep = "\n\t"
	}
	return sb.String()
}

func (e *copyError) Unwrap() error {
	return e.ctxError
}
