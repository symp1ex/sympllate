package webassets

import (
	"embed"
	"fmt"
	"sync"

	webview "github.com/symp1ex/go-webview2"
	"github.com/sympllate/translator/internal/webviewresource"
)

//go:embed dist
var files embed.FS

const origin = "https://app.sympllate.local"

var (
	providerOnce sync.Once
	provider     *webviewresource.Provider
	providerErr  error
)

func getProvider() (*webviewresource.Provider, error) {
	providerOnce.Do(func() {
		provider, providerErr = webviewresource.New(origin, files, "dist")
	})
	if providerErr != nil {
		return nil, fmt.Errorf("initialize embedded WebView resources: %w", providerErr)
	}
	return provider, nil
}

func Register(w webview.WebView) error {
	provider, err := getProvider()
	if err != nil {
		return err
	}
	if err := provider.Register(w); err != nil {
		return fmt.Errorf("register embedded WebView resources: %w", err)
	}
	return nil
}

func IndexURL() (string, error) {
	provider, err := getProvider()
	if err != nil {
		return "", err
	}
	return provider.URL("index.html", nil)
}
