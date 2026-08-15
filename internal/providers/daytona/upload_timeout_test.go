package daytona

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransferAwareHTTPClientHasNoOverallTimeout(t *testing.T) {
	client := transferAwareHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("overall Timeout = %v, want 0 for streaming archive uploads", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 60s", transport.ResponseHeaderTimeout)
	}
}

func TestTransferAwareHTTPClientPreservesDefaultTransportSemantics(t *testing.T) {
	defaults := http.DefaultTransport.(*http.Transport)
	transport := transferAwareHTTPClient().Transport.(*http.Transport)
	if transport == defaults {
		t.Fatal("transfer client reused the mutable default transport")
	}
	if transport.ForceAttemptHTTP2 != defaults.ForceAttemptHTTP2 {
		t.Fatalf("ForceAttemptHTTP2 = %t, want default %t", transport.ForceAttemptHTTP2, defaults.ForceAttemptHTTP2)
	}
	if transport.MaxIdleConns != defaults.MaxIdleConns || transport.MaxIdleConnsPerHost != defaults.MaxIdleConnsPerHost || transport.MaxConnsPerHost != defaults.MaxConnsPerHost {
		t.Fatalf("pool limits = (%d, %d, %d), want defaults (%d, %d, %d)", transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.MaxConnsPerHost, defaults.MaxIdleConns, defaults.MaxIdleConnsPerHost, defaults.MaxConnsPerHost)
	}
	if transport.IdleConnTimeout != defaults.IdleConnTimeout || transport.TLSHandshakeTimeout != defaults.TLSHandshakeTimeout || transport.ExpectContinueTimeout != defaults.ExpectContinueTimeout {
		t.Fatalf("transport timeouts = (%v, %v, %v), want defaults (%v, %v, %v)", transport.IdleConnTimeout, transport.TLSHandshakeTimeout, transport.ExpectContinueTimeout, defaults.IdleConnTimeout, defaults.TLSHandshakeTimeout, defaults.ExpectContinueTimeout)
	}
	if (transport.Proxy == nil) != (defaults.Proxy == nil) || (transport.DialContext == nil) != (defaults.DialContext == nil) {
		t.Fatal("transfer transport did not preserve default proxy/dial behavior")
	}
}

func TestTransferAwareHTTPClientBoundsResponseHeaders(t *testing.T) {
	const headerTimeout = 20 * time.Millisecond
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer close(release)

	started := time.Now()
	resp, err := newDaytonaTransferHTTPClient(headerTimeout).Get(server.URL)
	if resp != nil {
		resp.Body.Close()
	}
	var netErr net.Error
	if err == nil || !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("Get error = %v, want response-header timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("response-header timeout took %v", elapsed)
	}
}

func TestUploadDaytonaFileStreamCancellationStopsProducer(t *testing.T) {
	requestStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		close(requestStarted)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	sourceReader, sourceWriter := io.Pipe()
	defer sourceWriter.Close()
	source := newTrackingReadCloser(sourceReader)
	errCh := make(chan error, 1)
	go func() {
		errCh <- uploadDaytonaFileStream(ctx, server.Client(), server.URL, nil, source, "archive.tgz")
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("upload request did not start")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upload error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled upload did not return promptly")
	}
	select {
	case <-source.closed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close the upload source")
	}
	if got := source.closeCalls.Load(); got != 1 {
		t.Fatalf("source close calls = %d, want 1", got)
	}
	if _, err := sourceWriter.Write([]byte("producer leak probe")); err == nil || !strings.Contains(err.Error(), "closed pipe") {
		t.Fatalf("source write error = %v, want closed pipe after producer shutdown", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("server handler did not observe cancellation or connection closure")
	}
}

func TestUploadDaytonaFileStreamSuccessDoesNotCloseSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	source := newTrackingReadCloser(io.NopCloser(strings.NewReader("archive")))
	if err := uploadDaytonaFileStream(t.Context(), server.Client(), server.URL, nil, source, "archive.tgz"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.closed:
		t.Fatal("successful upload closed the caller-owned source")
	default:
	}
	if got := source.closeCalls.Load(); got != 0 {
		t.Fatalf("source close calls = %d, want 0", got)
	}
}

type trackingReadCloser struct {
	io.ReadCloser
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func newTrackingReadCloser(reader io.ReadCloser) *trackingReadCloser {
	return &trackingReadCloser{ReadCloser: reader, closed: make(chan struct{})}
}

func (r *trackingReadCloser) Close() error {
	r.closeCalls.Add(1)
	err := r.ReadCloser.Close()
	r.closeOnce.Do(func() { close(r.closed) })
	return err
}
