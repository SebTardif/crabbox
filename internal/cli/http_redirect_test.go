package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unusedDefaultRoundTripper struct{}

func (unusedDefaultRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("unused default transport")
}

func TestCloneDefaultTransportAcceptsNonTransportDefault(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = original
	})
	http.DefaultTransport = unusedDefaultRoundTripper{}

	transport := CloneDefaultTransport()
	if transport == nil {
		t.Fatal("cloned transport is nil")
	}
}

func TestCloneDefaultTransportCopiesRealDefaultTransport(t *testing.T) {
	original, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not *http.Transport")
	}

	cloned := CloneDefaultTransport()
	if cloned == nil {
		t.Fatal("cloned transport is nil")
	}
	if cloned == original {
		t.Fatal("clone reused http.DefaultTransport")
	}
}

func TestCloneDefaultTransportFallbackRoutesThroughProxy(t *testing.T) {
	proxied := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied++
		if r.Method != http.MethodGet || r.URL.Host == "" {
			t.Errorf("proxy request method=%s host=%q", r.Method, r.URL.Host)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "proxied")
	}))
	t.Cleanup(proxy.Close)

	original := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = original
	})
	http.DefaultTransport = unusedDefaultRoundTripper{}
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("ALL_PROXY", "")

	client := &http.Client{Transport: CloneDefaultTransport()}
	resp, err := client.Get("http://example.invalid/fallback")
	if err != nil {
		t.Fatalf("fallback request: %v", err)
	}
	defer resp.Body.Close()
	if proxied == 0 {
		t.Fatal("fallback transport did not send the request through HTTP_PROXY")
	}
}
