//go:build windows

package window

import (
	"errors"
	"runtime"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
)

func RunMain(cfg config.Config, html string, service *app.Service, clip *clipboard.Manager, popup *Popup) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w := webview.NewWithOptions(webview.WebViewOptions{AutoFocus: true, WindowOptions: webview.WindowOptions{Title: "Sympllate", Width: uint(cfg.UI.MainWindowWidth), Height: uint(cfg.UI.MainWindowHeight), Center: true}})
	if w == nil {
		return errors.New("не удалось создать основное окно WebView2; проверьте наличие Microsoft Edge WebView2 Runtime")
	}
	defer w.Destroy()
	if err := bindCommon(w, "main", cfg, service, clip, popup); err != nil {
		return err
	}
	w.SetHtml(html)
	w.Run()
	return nil
}
