//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/hotkeys"
	"github.com/sympllate/translator/internal/imagebatch"
	"github.com/sympllate/translator/internal/inpaint"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/localmodel"
	"github.com/sympllate/translator/internal/logger"
	"github.com/sympllate/translator/internal/ocr"
	"github.com/sympllate/translator/internal/ollama"
	"github.com/sympllate/translator/internal/translation"
	"github.com/sympllate/translator/internal/tray"
	"github.com/sympllate/translator/internal/updater"
	"github.com/sympllate/translator/internal/webassets"
	"github.com/sympllate/translator/internal/window"
)

var errRestartRequested = errors.New("application restart requested")
var version = "0.3.8.6"

func main() {
	if err := run(); errors.Is(err, errRestartRequested) {
		if restartErr := restartApplication(); restartErr != nil {
			showError(restartErr)
			os.Exit(1)
		}
	} else if err != nil {
		showError(err)
		os.Exit(1)
	}
}

func run() error {
	configPath, err := config.ExecutablePath()
	if err != nil {
		return err
	}
	cfg, created, err := config.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	config.SetCurrent(cfg)
	logger.Configure(cfg.Logs)
	config.SetLogger(logger.Sympllate)
	updater.SetLogger(logger.Sympllate)
	applicationLogger := logger.Sympllate
	applicationLogger.Printf("application starting: config=%s", configPath)
	if created {
		applicationLogger.Printf("default config created: path=%s", configPath)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	selectedProvider, localLayout, err := localmodel.SelectProvider(cfg.Provider.Active, filepath.Dir(configPath), cfg.LocalModel)
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
			ExecutableDir:      filepath.Dir(configPath),
			StartupTimeout:     time.Duration(cfg.LocalModel.StartupTimeoutSeconds) * time.Second,
			RequestTimeout:     time.Duration(cfg.Ollama.TimeoutSeconds) * time.Second,
			NumCtx:             cfg.Ollama.NumCtx,
			NumPredict:         cfg.Ollama.NumPredict,
			Temperature:        cfg.Ollama.Temperature,
			FitTargetMiB:       cfg.LocalModel.FitTargetMiB,
			MaxInputCharacters: cfg.Limits.MaxInputCharacters,
		}, applicationLogger.Writer())
		if err != nil {
			return fmt.Errorf("start local provider: %w", err)
		}
		translator = localRuntime.Client()
		applicationLogger.Printf("translation provider selected: local model=%s", filepath.Base(localLayout.ModelPath))
	} else {
		client, clientErr := ollama.New(cfg.Ollama, cfg.Limits.MaxInputCharacters)
		if clientErr != nil {
			return clientErr
		}
		translator = client
		applicationLogger.Printf("translation provider selected: ollama")
	}
	defer func() {
		if localRuntime != nil {
			_ = localRuntime.Close()
		}
	}()

	showCombination, err := hotkeys.Parse(cfg.Hotkeys.ShowTranslation)
	if err != nil {
		return fmt.Errorf("invalid hotkeys.showTranslation: %w", err)
	}
	replaceCombination, err := hotkeys.Parse(cfg.Hotkeys.ReplaceSelection)
	if err != nil {
		return fmt.Errorf("invalid hotkeys.replaceSelection: %w", err)
	}
	html, err := webassets.HTML()
	if err != nil {
		return err
	}
	detector := language.SimpleDetector{}
	service := app.NewService(ctx, translator, detector, applicationLogger)
	completer, ok := translator.(translation.RawCompleter)
	if !ok {
		return errors.New("the selected provider does not support structured translation")
	}
	renderConfig := imagebatch.DefaultRenderConfig()
	renderConfig.MinimumFontSize = cfg.ImageBatch.MinimumFontSize
	renderConfig.MaximumFontSize = cfg.ImageBatch.MaximumFontSize
	renderConfig.LineSpacing = cfg.ImageBatch.LineSpacing
	renderConfig.JPEGQuality = cfg.ImageBatch.JPEGQuality
	inpaintEngine, err := inpaint.NewEngine(filepath.Dir(configPath))
	if err != nil {
		return fmt.Errorf("configure local LaMa inpainting: %w", err)
	}
	batchService, err := imagebatch.NewService(ctx, filepath.Dir(configPath), ocr.New(filepath.Dir(configPath), ocr.DefaultTimeout), completer, cfg.Limits.MaxInputCharacters, renderConfig, inpaintEngine, applicationLogger)
	if err != nil {
		_ = inpaintEngine.Close()
		return fmt.Errorf("configure image batch service: %w", err)
	}
	clip := clipboard.New(applicationLogger)
	popup := window.NewPopup(cfg, html, service, clip)
	if err := popup.Start(); err != nil {
		batchService.Close()
		batchService.Wait()
		return err
	}
	batchWindow := window.NewImageBatchWindow(cfg, html, service, batchService, clip, popup)
	if err := batchWindow.Start(); err != nil {
		batchService.Close()
		batchService.Wait()
		popup.Close()
		return err
	}
	targets := window.NewOriginTargetManager()
	controller := app.NewHotkeyController(ctx, cfg, service, detector, clip, targets, popup, applicationLogger)
	popup.SetQuickTranslationHandler(controller)
	hotkeyManager := hotkeys.NewManager(showCombination, replaceCombination, controller.ShowTranslation, controller.ReplaceSelection)
	if err := hotkeyManager.Start(); err != nil {
		controller.Close()
		batchWindow.Close()
		popup.Close()
		batchService.Close()
		batchService.Wait()
		return err
	}
	applicationLogger.Printf("global hotkeys registered: show=%s replace=%s", showCombination.Display, replaceCombination.Display)

	restartRequested := make(chan struct{}, 1)
	requestRestart := func() {
		select {
		case restartRequested <- struct{}{}:
		default:
		}
	}
	mainWindow := window.NewMainWindow(cfg, configPath, version, html, service, batchWindow, clip, popup, applicationLogger, showError, requestRestart)
	systemTray := tray.New(mainWindow.Open, mainWindow.OpenSettings, applicationLogger)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			service.Close()
			batchService.Close()
			systemTray.Close()
			hotkeyManager.Close()
			mainWindow.Shutdown()
			batchWindow.Close()
			cancel()
			controller.Close()
			service.Wait()
			batchService.Wait()
			popup.Close()
			if localRuntime != nil {
				if err := localRuntime.Close(); err != nil {
					applicationLogger.Printf("local provider shutdown failed: %v", err)
				}
			}
			if instanceLock != nil {
				if err := instanceLock.Close(); err != nil {
					applicationLogger.Printf("single-instance mutex close failed: %v", err)
				}
			}
			applicationLogger.Printf("application stopping")
		})
	}
	defer cleanup()
	if err := systemTray.Start(); err != nil {
		return fmt.Errorf("start system tray: %w", err)
	}
	applicationLogger.Printf("system tray started")
	restart := false
	select {
	case <-systemTray.Quit():
		applicationLogger.Printf("Quit selected from system tray")
	case <-restartRequested:
		restart = true
		applicationLogger.Printf("application restart requested after settings save")
	}
	cleanup()
	if restart {
		return errRestartRequested
	}
	return nil
}

func restartApplication() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determine application to restart: %w", err)
	}
	command := exec.Command(executable, os.Args[1:]...)
	if err := command.Start(); err != nil {
		return fmt.Errorf("restart application: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release restarted application process: %w", err)
	}
	return nil
}

func showError(err error) {
	title, _ := syscall.UTF16PtrFromString("Sympllate — Error")
	message, _ := syscall.UTF16PtrFromString(err.Error())
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	proc.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
