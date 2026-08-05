//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/hotkeys"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/localmodel"
	"github.com/sympllate/translator/internal/ollama"
	"github.com/sympllate/translator/internal/tray"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	selectedProvider, localLayout, err := localmodel.SelectProvider(cfg.Provider, filepath.Dir(configPath), cfg.LocalModel)
	if err != nil {
		return err
	}
	var translator app.Translator
	var localRuntime *localmodel.Runtime
	var instanceLock *localmodel.InstanceLock
	if selectedProvider == config.ProviderLocal {
		instanceLock, err = localmodel.AcquireInstanceLock(filepath.Dir(configPath))
		if err != nil {
			return err
		}
		defer instanceLock.Close()
		localRuntime, err = localmodel.Start(ctx, localmodel.RuntimeConfig{
			Layout:             localLayout,
			StartupTimeout:     time.Duration(cfg.LocalModel.StartupTimeoutSeconds) * time.Second,
			RequestTimeout:     time.Duration(cfg.Ollama.TimeoutSeconds) * time.Second,
			NumCtx:             cfg.Ollama.NumCtx,
			NumPredict:         cfg.Ollama.NumPredict,
			Temperature:        cfg.Ollama.Temperature,
			FitTargetMiB:       cfg.LocalModel.FitTargetMiB,
			MaxInputCharacters: cfg.Limits.MaxInputCharacters,
		}, logger.Writer())
		if err != nil {
			return fmt.Errorf("запустить local provider: %w", err)
		}
		translator = localRuntime.Client()
		logger.Printf("translation provider selected: local model=%s", filepath.Base(localLayout.ModelPath))
	} else {
		client, clientErr := ollama.New(cfg.Ollama, cfg.Limits.MaxInputCharacters)
		if clientErr != nil {
			return clientErr
		}
		translator = client
		logger.Printf("translation provider selected: ollama")
	}
	defer func() {
		if localRuntime != nil {
			_ = localRuntime.Close()
		}
	}()

	showCombination, err := hotkeys.Parse(cfg.Hotkeys.ShowTranslation)
	if err != nil {
		return fmt.Errorf("неверная hotkeys.showTranslation: %w", err)
	}
	replaceCombination, err := hotkeys.Parse(cfg.Hotkeys.ReplaceSelection)
	if err != nil {
		return fmt.Errorf("неверная hotkeys.replaceSelection: %w", err)
	}
	html, err := webassets.HTML()
	if err != nil {
		return err
	}
	detector := language.SimpleDetector{}
	service := app.NewService(ctx, translator, detector, logger)
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

	mainWindow := window.NewMainWindow(cfg, html, service, clip, popup, logger, showError)
	systemTray := tray.New(mainWindow.Open, logger)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			service.Close()
			systemTray.Close()
			hotkeyManager.Close()
			mainWindow.Shutdown()
			cancel()
			controller.Close()
			service.Wait()
			popup.Close()
			if localRuntime != nil {
				if err := localRuntime.Close(); err != nil {
					logger.Printf("local provider shutdown failed: %v", err)
				}
			}
			if instanceLock != nil {
				if err := instanceLock.Close(); err != nil {
					logger.Printf("single-instance mutex close failed: %v", err)
				}
			}
			logger.Printf("application stopping")
		})
	}
	defer cleanup()
	if err := systemTray.Start(); err != nil {
		return fmt.Errorf("запустить system tray: %w", err)
	}
	logger.Printf("system tray started")
	<-systemTray.Quit()
	logger.Printf("Quit selected from system tray")
	cleanup()
	return nil
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
