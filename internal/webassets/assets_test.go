package webassets

import (
	"bytes"
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"testing"

	webview "github.com/symp1ex/go-webview2"
)

func TestIndexURLServesProductionAssets(t *testing.T) {
	indexURL, err := IndexURL()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := url.Parse(indexURL)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Scheme != "https" || entry.Host != "app.sympllate.local" || entry.Path != "/index.html" || entry.RawQuery != "" || entry.Fragment != "" {
		t.Fatalf("unexpected frontend entrypoint: %s", indexURL)
	}
	provider, err := getProvider()
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Handle(webview.WebResourceRequest{URI: indexURL})
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.StatusCode != 200 {
		t.Fatalf("index response = %#v, want 200", response)
	}
	assets := regexp.MustCompile(`(?:src|href)="([^"]+\.(?:js|css))"`).FindAllSubmatch(response.Content, -1)
	if len(assets) == 0 {
		t.Fatal("production entrypoint has no JS/CSS asset references")
	}
	var hasJS, hasCSS bool
	for _, asset := range assets {
		reference, err := url.Parse(string(asset[1]))
		if err != nil {
			t.Fatal(err)
		}
		assetURL := entry.ResolveReference(reference)
		assetResponse, err := provider.Handle(webview.WebResourceRequest{URI: assetURL.String()})
		if err != nil {
			t.Fatal(err)
		}
		if assetResponse == nil || assetResponse.StatusCode != 200 {
			t.Fatalf("asset %s response = %#v, want 200", assetURL, assetResponse)
		}
		hasJS = hasJS || strings.HasSuffix(assetURL.Path, ".js")
		hasCSS = hasCSS || strings.HasSuffix(assetURL.Path, ".css")
	}
	if !hasJS || !hasCSS {
		t.Fatal("production JS/CSS asset references are missing")
	}
}

func TestEmbeddedFrontendServedWithoutTransformation(t *testing.T) {
	provider, err := getProvider()
	if err != nil {
		t.Fatal(err)
	}
	var fileCount int
	err = fs.WalkDir(files, "dist", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		fileCount++
		t.Run(filePath, func(t *testing.T) {
			want, err := files.ReadFile(filePath)
			if err != nil {
				t.Fatal(err)
			}
			resourceURL, err := provider.URL(strings.TrimPrefix(filePath, "dist/"), nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := provider.Handle(webview.WebResourceRequest{URI: resourceURL})
			if err != nil {
				t.Fatal(err)
			}
			if response == nil || response.StatusCode != 200 {
				t.Fatalf("response = %#v, want 200", response)
			}
			if !bytes.Equal(response.Content, want) {
				t.Fatal("provider changed embedded frontend bytes")
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fileCount == 0 {
		t.Fatal("embedded frontend is empty")
	}
}

func TestPopupUsesAutomaticTargetTranslationWithoutSourceText(t *testing.T) {
	data, err := files.ReadFile("dist/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if strings.Contains(page, "Translate again") {
		t.Fatal("popup retry button remains in production assets")
	}
	if strings.Contains(page, "popup-source") {
		t.Fatal("popup source text remains in production assets")
	}
	if !strings.Contains(page, "SetQuickTranslationTarget") {
		t.Fatal("popup target language does not call the automatic translation binding")
	}
}
