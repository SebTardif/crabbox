package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
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
	if os.Getenv("CRABBOX_CLONE_TRANSPORT_PROXY_CHILD") == "1" {
		runCloneDefaultTransportProxyChild(t)
		return
	}

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

	cmd := exec.Command(os.Args[0], "-test.run=^TestCloneDefaultTransportFallbackRoutesThroughProxy$", "-test.count=1")
	cmd.Env = cloneTransportProxyChildEnv(proxy.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child process: %v\n%s", err, out)
	}
	if proxied == 0 {
		t.Fatalf("parent proxy received no request\n%s", out)
	}
}

func runCloneDefaultTransportProxyChild(t *testing.T) {
	http.DefaultTransport = unusedDefaultRoundTripper{}
	transport := CloneDefaultTransport()
	if transport.Proxy == nil {
		t.Fatal("fallback Proxy is nil, want ProxyFromEnvironment")
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Get("http://example.invalid/fallback")
	if err != nil {
		t.Fatalf("fallback request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" {
		t.Fatalf("body=%q, want proxied", body)
	}
}

func cloneTransportProxyChildEnv(proxyURL string) []string {
	skip := map[string]bool{
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true, "all_proxy": true,
	}
	env := make([]string, 0, 16)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if skip[key] {
			continue
		}
		env = append(env, item)
	}
	return append(env,
		"CRABBOX_CLONE_TRANSPORT_PROXY_CHILD=1",
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"NO_PROXY=",
		"ALL_PROXY=",
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
		"no_proxy=",
	)
}
