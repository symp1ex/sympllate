//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/hotkeys"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/ollama"
	"github.com/sympllate/translator/internal/webassets"
	"github.com/sympllate/translator/internal/window"
)

func main() {
	if err := run(); err != nil {
		showError(err)
		os.Exit(1)
	}
}

func run() error {
	configPath, err := config.ExecutablePath()
	if err != nil {
		return err
	}
	logger, closeLog := newLogger(filepath.Dir(configPath))
	defer closeLog()
	logger.Printf("application starting: config=%s", configPath)
	cfg, created, err := config.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	if created {
		logger.Printf("default config created: path=%s", configPath)
	}
	showCombination, err := hotkeys.Parse(cfg.Hotkeys.ShowTranslation)
	if err != nil {
		return fmt.Errorf("неверная hotkeys.showTranslation: %w", err)
	}
	replaceCombination, err := hotkeys.Parse(cfg.Hotkeys.ReplaceSelection)
	if err != nil {
		return fmt.Errorf("неверная hotkeys.replaceSelection: %w", err)
	}
	client, err := ollama.New(cfg.Ollama, cfg.Limits.MaxInputCharacters)
	if err != nil {
		return err
	}
	html, err := webassets.HTML()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	detector := language.SimpleDetector{}
	service := app.NewService(ctx, client, detector, logger)
	clip := clipboard.New(logger)
	popup := window.NewPopup(cfg, html, service, clip)
	if err := popup.Start(); err != nil {
		return err
	}
	targets := window.NewOriginTargetManager()
	controller := app.NewHotkeyController(ctx, cfg, service, detector, clip, targets, popup, logger)
	popup.SetQuickTranslationHandler(controller)
	hotkeyManager := hotkeys.NewManager(showCombination, replaceCombination, controller.ShowTranslation, controller.ReplaceSelection)
	if err := hotkeyManager.Start(); err != nil {
		controller.Close()
		popup.Close()
		return err
	}
	logger.Printf("global hotkeys registered: show=%s replace=%s", showCombination.Display, replaceCombination.Display)
	err = window.RunMain(cfg, html, service, clip, popup)
	cancel()
	hotkeyManager.Close()
	controller.Close()
	service.Wait()
	popup.Close()
	logger.Printf("application stopping")
	return err
}

func newLogger(directory string) (*log.Logger, func()) {
	file, err := os.OpenFile(filepath.Join(directory, "translator.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return log.New(os.Stderr, "translator: ", log.LstdFlags|log.Lmicroseconds), func() {}
	}
	return log.New(io.MultiWriter(os.Stderr, file), "translator: ", log.LstdFlags|log.Lmicroseconds), func() { _ = file.Close() }
}

func showError(err error) {
	title, _ := syscall.UTF16PtrFromString("Sympllate — ошибка")
	message, _ := syscall.UTF16PtrFromString(err.Error())
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	proc.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
