package cli

import (
	"fmt"
	"net/http"
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
