package webassets

import (
	"embed"
	"fmt"
	"path"
	"regexp"
	"strings"
)

//go:embed dist
var files embed.FS

var (
	stylePattern  = regexp.MustCompile(`<link[^>]+href="([^"]+\.css)"[^>]*>`)
	scriptPattern = regexp.MustCompile(`<script[^>]+src="([^"]+\.js)"[^>]*></script>`)
)

func HTML() (string, error) {
	index, err := files.ReadFile("dist/index.html")
	if err != nil {
		return "", fmt.Errorf("read embedded frontend: %w", err)
	}
	page := string(index)
	page, err = inline(page, stylePattern, "style")
	if err != nil {
		return "", err
	}
	page, err = inline(page, scriptPattern, "script")
	if err != nil {
		return "", err
	}
	return page, nil
}

func inline(page string, pattern *regexp.Regexp, kind string) (string, error) {
	var inlineErr error
	result := pattern.ReplaceAllStringFunc(page, func(tag string) string {
		match := pattern.FindStringSubmatch(tag)
		if len(match) != 2 {
			return tag
		}
		asset := strings.TrimPrefix(match[1], "/")
		asset = path.Clean(asset)
		if strings.HasPrefix(asset, "../") {
			inlineErr = fmt.Errorf("invalid frontend asset path %q", asset)
			return tag
		}
		data, err := files.ReadFile("dist/" + asset)
		if err != nil {
			inlineErr = fmt.Errorf("read frontend asset %q: %w", asset, err)
			return tag
		}
		if kind == "style" {
			return "<style>" + string(data) + "</style>"
		}
		content := strings.ReplaceAll(string(data), "</script", "<\\/script")
		return `<script type="module">` + content + `</script>`
	})
	if inlineErr != nil {
		return "", inlineErr
	}
	return result, nil
}
