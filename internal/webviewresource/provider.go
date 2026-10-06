package webviewresource

import (
	"fmt"
	"io/fs"
	"mime"
	"net/url"
	"path"
	"strings"

	webview2 "github.com/symp1ex/go-webview2"
)

const (
	notFoundBody = "404 page not found\n"
	baseHeaders  = "X-Content-Type-Options: nosniff\r\nCache-Control: no-store\r\n"
)

type resourceWebView interface {
	SetWebResourceRequestedHandler(handler webview2.WebResourceHandler)
	AddWebResourceRequestedFilter(uriPattern string, resourceContext webview2.WebResourceContext) error
}

// Provider serves one embedded filesystem tree from one synthetic HTTPS origin.
type Provider struct {
	origin string
	host   string
	fs     fs.FS
}

func New(origin string, source fs.FS, root string) (*Provider, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return nil, fmt.Errorf("parse embedded resource origin: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("embedded resource origin must be an HTTPS origin without path, query, or fragment")
	}
	if parsed.Hostname() != parsed.Host {
		return nil, fmt.Errorf("embedded resource origin must not contain a port")
	}

	rootFS, err := fs.Sub(source, root)
	if err != nil {
		return nil, fmt.Errorf("open embedded resource root %q: %w", root, err)
	}

	return &Provider{
		origin: "https://" + parsed.Host,
		host:   parsed.Host,
		fs:     rootFS,
	}, nil
}

// Register installs the handler before adding the narrow origin filter.
func (p *Provider) Register(w resourceWebView) error {
	if w == nil {
		return fmt.Errorf("webview is nil")
	}

	w.SetWebResourceRequestedHandler(p.Handle)
	if err := w.AddWebResourceRequestedFilter(p.origin+"/*", webview2.WebResourceContextAll); err != nil {
		w.SetWebResourceRequestedHandler(nil)
		return fmt.Errorf("add embedded resource filter: %w", err)
	}
	return nil
}

func (p *Provider) URL(resourcePath string, query url.Values) (string, error) {
	resourcePath = strings.TrimPrefix(resourcePath, "/")
	if resourcePath != "" && !fs.ValidPath(resourcePath) {
		return "", fmt.Errorf("invalid embedded resource path %q", resourcePath)
	}

	result := p.origin + "/" + resourcePath
	if encoded := query.Encode(); encoded != "" {
		result += "?" + encoded
	}
	return result, nil
}

func (p *Provider) Handle(request webview2.WebResourceRequest) (*webview2.WebResourceResponse, error) {
	requestURL, err := url.Parse(request.URI)
	if err != nil || !p.matchesOrigin(requestURL) {
		return nil, nil
	}

	resourcePath, ok := resourcePath(requestURL)
	if !ok {
		return notFoundResponse(), nil
	}

	body, err := fs.ReadFile(p.fs, resourcePath)
	if err != nil {
		return notFoundResponse(), nil
	}

	return &webview2.WebResourceResponse{
		Content:      body,
		StatusCode:   200,
		ReasonPhrase: "OK",
		Headers:      "Content-Type: " + contentType(resourcePath) + "\r\n" + baseHeaders,
	}, nil
}

func (p *Provider) matchesOrigin(requestURL *url.URL) bool {
	return requestURL != nil &&
		requestURL.Scheme == "https" &&
		requestURL.User == nil &&
		strings.EqualFold(requestURL.Host, p.host)
}

func resourcePath(requestURL *url.URL) (string, bool) {
	if requestURL.Opaque != "" ||
		(requestURL.RawPath != "" && invalidEscapedPath(requestURL.EscapedPath())) {
		return "", false
	}

	decodedPath := requestURL.Path
	if strings.Contains(decodedPath, `\`) {
		return "", false
	}

	for strings.Contains(decodedPath, "%") {
		next, err := url.PathUnescape(decodedPath)
		if err != nil || next == decodedPath {
			break
		}
		if strings.Contains(next, `\`) || !validAbsoluteResourcePath(next) {
			return "", false
		}
		decodedPath = next
	}

	if requestURL.Path == "/" || requestURL.Path == "" {
		return "index.html", true
	}
	if !validAbsoluteResourcePath(requestURL.Path) {
		return "", false
	}

	resourcePath := strings.TrimPrefix(requestURL.Path, "/")
	return resourcePath, fs.ValidPath(resourcePath)
}

func invalidEscapedPath(escapedPath string) bool {
	escapedPath = strings.ToLower(escapedPath)
	return strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c")
}

func validAbsoluteResourcePath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	trimmed := strings.TrimPrefix(value, "/")
	return trimmed == "" || fs.ValidPath(trimmed)
}

func contentType(resourcePath string) string {
	extension := path.Ext(resourcePath)
	switch strings.ToLower(extension) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".wasm":
		return "application/wasm"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	}

	if detected := mime.TypeByExtension(extension); detected != "" {
		return detected
	}
	return "application/octet-stream"
}

func notFoundResponse() *webview2.WebResourceResponse {
	return &webview2.WebResourceResponse{
		Content:      []byte(notFoundBody),
		StatusCode:   404,
		ReasonPhrase: "Not Found",
		Headers:      "Content-Type: text/plain; charset=utf-8\r\n" + baseHeaders,
	}
}
