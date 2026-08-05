//go:build windows

package window

import (
	"context"
	"encoding/json"
	"fmt"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/updater"
)

func bindApplicationUpdater(w webview.WebView, mainWindow *MainWindow) error {
	if err := w.Bind("getApplicationInfo", func() map[string]any {
		return map[string]any{
			"version":        mainWindow.version,
			"updaterEnabled": mainWindow.cfg.Updater.Enabled,
		}
	}); err != nil {
		return fmt.Errorf("bind getApplicationInfo: %w", err)
	}

	if err := w.Bind("checkApplicationUpdate", func() map[string]any {
		if !mainWindow.cfg.Updater.Enabled {
			mainWindow.logWarning("[Updater] Update check request ignored because updater is disabled")
			return map[string]any{
				"ok":              false,
				"updateAvailable": false,
				"message":         "updater is disabled",
			}
		}
		if !mainWindow.beginApplicationUpdateCheck() {
			mainWindow.logWarning("[Updater] Update check request ignored because another check is already running")
			return map[string]any{
				"ok":              false,
				"updateAvailable": false,
				"message":         "update check is already running",
			}
		}

		go func() {
			defer mainWindow.endApplicationUpdateCheck()
			result := updater.DefaultService.Check(context.Background())
			mainWindow.dispatchApplicationUpdateCheckResult(result)
		}()

		return map[string]any{
			"ok":              true,
			"updateAvailable": false,
			"message":         "update check started",
		}
	}); err != nil {
		return fmt.Errorf("bind checkApplicationUpdate: %w", err)
	}

	if err := w.Bind("installApplicationUpdate", func() map[string]any {
		if !mainWindow.cfg.Updater.Enabled {
			mainWindow.logWarning("[Updater] Update installation request ignored because updater is disabled")
			return map[string]any{"ok": false, "message": "updater is disabled"}
		}
		result := updater.DefaultService.Install()
		return map[string]any{"ok": result.OK, "message": result.Message}
	}); err != nil {
		return fmt.Errorf("bind installApplicationUpdate: %w", err)
	}

	return nil
}

func (m *MainWindow) beginApplicationUpdateCheck() bool {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	if m.updateCheckRunning {
		return false
	}
	m.updateCheckRunning = true
	return true
}

func (m *MainWindow) endApplicationUpdateCheck() {
	m.updateMu.Lock()
	m.updateCheckRunning = false
	m.updateMu.Unlock()
}

func (m *MainWindow) dispatchApplicationUpdateCheckResult(result updater.CheckResult) {
	m.mu.Lock()
	w := m.w
	running := m.state == mainWindowRunning
	m.mu.Unlock()
	if !running || w == nil {
		m.logDebug("[Updater] Update check result skipped because main window is closed")
		return
	}

	payload, err := json.Marshal(result)
	if err != nil {
		m.logWarning("[Updater] Failed to marshal update check result: %v", err)
		return
	}
	m.logDebug("[Updater] Dispatching update check result")
	w.Dispatch(func() {
		w.Eval(fmt.Sprintf(
			`window.dispatchEvent(new CustomEvent("application-update-check-result", { detail: %s }));`,
			payload,
		))
	})
}

func (m *MainWindow) logDebug(format string, args ...any) {
	if logger, ok := m.logger.(interface{ Debugf(string, ...any) }); ok {
		logger.Debugf(format, args...)
		return
	}
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}

func (m *MainWindow) logWarning(format string, args ...any) {
	if logger, ok := m.logger.(interface{ Warnf(string, ...any) }); ok {
		logger.Warnf(format, args...)
		return
	}
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}
