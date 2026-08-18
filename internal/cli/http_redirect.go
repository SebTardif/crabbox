package cli

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CloneDefaultTransport copies http.DefaultTransport when it is a *http.Transport.
// A replaced non-transport RoundTripper falls back to standard Transport defaults,
// including environment proxy lookup.
func CloneDefaultTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return transport.Clone()
	}
	return newStandardHTTPTransport()
}

func newStandardHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// redirectCheckedHTTPClient clones source so callers can constrain redirects
// without mutating a shared client or discarding its transport and timeouts.
func redirectCheckedHTTPClient(source *http.Client, check func(*http.Request) error) *http.Client {
	if source == nil {
		source = http.DefaultClient
	}
	client := *source
	originalCheckRedirect := source.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := check(req); err != nil {
			return err
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

func sameHTTPOrigin(a, b *url.URL) bool {
	return a != nil && b != nil &&
		strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectiveHTTPPort(a) == effectiveHTTPPort(b)
}

func effectiveHTTPPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
