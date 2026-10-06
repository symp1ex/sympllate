package webviewresource

import (
	"bytes"
	"errors"
	"mime"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	webview2 "github.com/symp1ex/go-webview2"
)

const testOrigin = "https://app.sympllate.local"

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := New(testOrigin, fstest.MapFS{
		"dist/index.html":       {Data: []byte("<html>embedded</html>")},
		"dist/assets/app.js":    {Data: []byte("export const ready = true;")},
		"dist/assets/style.css": {Data: []byte("body { color: black; }")},
		"dist/assets/image.png": {Data: []byte{0x00, 0xff, 0x10, 0x80}},
	}, "dist")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return provider
}

func TestProviderServesEmbeddedResources(t *testing.T) {
	provider := newTestProvider(t)
	tests := []struct {
		name        string
		uri         string
		status      int
		contentType string
		body        []byte
	}{
		{name: "html", uri: testOrigin + "/index.html", status: 200, contentType: "text/html", body: []byte("<html>embedded</html>")},
		{name: "root", uri: testOrigin + "/", status: 200, contentType: "text/html", body: []byte("<html>embedded</html>")},
		{name: "javascript", uri: testOrigin + "/assets/app.js", status: 200, contentType: "text/javascript", body: []byte("export const ready = true;")},
		{name: "css", uri: testOrigin + "/assets/style.css", status: 200, contentType: "text/css", body: []byte("body { color: black; }")},
		{name: "binary", uri: testOrigin + "/assets/image.png", status: 200, contentType: "image/png", body: []byte{0x00, 0xff, 0x10, 0x80}},
		{name: "query", uri: testOrigin + "/assets/app.js?v=123", status: 200, contentType: "text/javascript", body: []byte("export const ready = true;")},
		{name: "fragment", uri: testOrigin + "/assets/app.js#ignored", status: 200, contentType: "text/javascript", body: []byte("export const ready = true;")},
		{name: "missing", uri: testOrigin + "/missing.js", status: 404, contentType: "text/plain", body: []byte(notFoundBody)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := provider.Handle(webview2.WebResourceRequest{URI: test.uri})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if response == nil {
				t.Fatal("Handle() response is nil")
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
			if !bytes.Equal(response.Content, test.body) {
				t.Fatalf("body = %v, want %v", response.Content, test.body)
			}
			mediaType, _, err := mime.ParseMediaType(headerValue(response.Headers, "Content-Type"))
			if err != nil {
				t.Fatalf("parse Content-Type: %v", err)
			}
			if mediaType != test.contentType {
				t.Fatalf("Content-Type = %q, want %q", mediaType, test.contentType)
			}
			if headerValue(response.Headers, "X-Content-Type-Options") != "nosniff" {
				t.Fatal("X-Content-Type-Options is missing")
			}
			if headerValue(response.Headers, "Cache-Control") != "no-store" {
				t.Fatal("Cache-Control is missing")
			}
		})
	}
}

func TestProviderLeavesForeignRequestsUnhandled(t *testing.T) {
	provider := newTestProvider(t)
	for _, uri := range []string{
		"http://app.sympllate.local/index.html",
		"https://other.sympllate.local/index.html",
		"https://app.sympllate.local:443/index.html",
		"https://github.com/",
	} {
		response, err := provider.Handle(webview2.WebResourceRequest{URI: uri})
		if err != nil {
			t.Fatalf("Handle(%q) error = %v", uri, err)
		}
		if response != nil {
			t.Fatalf("Handle(%q) response = %#v, want nil", uri, response)
		}
	}
}

func TestProviderRejectsTraversal(t *testing.T) {
	provider := newTestProvider(t)
	for _, resource := range []string{
		"../index.html",
		"./index.html",
		"%2e%2e/index.html",
		"%252e%252e/index.html",
		"assets%2fapp.js",
		"assets%252fapp.js",
		"assets%5capp.js",
		"assets%255capp.js",
		`assets\app.js`,
		"//index.html",
	} {
		response, err := provider.Handle(webview2.WebResourceRequest{URI: testOrigin + "/" + resource})
		if err != nil {
			t.Fatalf("Handle(%q) error = %v", resource, err)
		}
		if response == nil || response.StatusCode != 404 {
			t.Fatalf("Handle(%q) = %#v, want 404", resource, response)
		}
	}
}

func TestProviderRootCannotReadSiblingTree(t *testing.T) {
	source := fstest.MapFS{
		"app/index.html":    {Data: []byte("app")},
		"private/secret.js": {Data: []byte("secret")},
	}
	provider, err := New(testOrigin, source, "app")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := provider.Handle(webview2.WebResourceRequest{URI: testOrigin + "/private/secret.js"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response == nil || response.StatusCode != 404 {
		t.Fatalf("response = %#v, want 404", response)
	}
}

func TestProviderURL(t *testing.T) {
	provider := newTestProvider(t)
	got, err := provider.URL("assets/app.js", url.Values{"v": {"123"}})
	if err != nil {
		t.Fatalf("URL() error = %v", err)
	}
	if want := testOrigin + "/assets/app.js?v=123"; got != want {
		t.Fatalf("URL() = %q, want %q", got, want)
	}
	if _, err := provider.URL("../secret", nil); err == nil {
		t.Fatal("URL() accepted traversal")
	}
}

func TestProviderRegisterOrdersHandlerBeforeFilter(t *testing.T) {
	provider := newTestProvider(t)
	view := &fakeResourceWebView{}
	if err := provider.Register(view); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got, want := strings.Join(view.calls, ","), "handler,filter"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if view.filter != testOrigin+"/*" {
		t.Fatalf("filter = %q", view.filter)
	}
	if view.context != webview2.WebResourceContextAll {
		t.Fatalf("context = %v", view.context)
	}
}

func TestProviderRegisterClearsHandlerAfterFilterError(t *testing.T) {
	provider := newTestProvider(t)
	view := &fakeResourceWebView{filterErr: errors.New("filter failed")}
	if err := provider.Register(view); err == nil {
		t.Fatal("Register() error is nil")
	}
	if view.handler != nil {
		t.Fatal("handler was not cleared")
	}
}

type fakeResourceWebView struct {
	calls     []string
	handler   webview2.WebResourceHandler
	filter    string
	context   webview2.WebResourceContext
	filterErr error
}

func (w *fakeResourceWebView) SetWebResourceRequestedHandler(handler webview2.WebResourceHandler) {
	w.calls = append(w.calls, "handler")
	w.handler = handler
}

func (w *fakeResourceWebView) AddWebResourceRequestedFilter(filter string, context webview2.WebResourceContext) error {
	w.calls = append(w.calls, "filter")
	w.filter = filter
	w.context = context
	return w.filterErr
}

func headerValue(headers string, name string) string {
	for _, line := range strings.Split(headers, "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
