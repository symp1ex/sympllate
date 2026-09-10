//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
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
	sharedort "github.com/sympllate/translator/internal/onnxruntime"
	"github.com/sympllate/translator/internal/translation"
	"github.com/sympllate/translator/internal/tray"
	"github.com/sympllate/translator/internal/updater"
	"github.com/sympllate/translator/internal/webassets"
	"github.com/sympllate/translator/internal/window"
)

var errRestartRequested = errors.New("application restart requested")
var version = "0.4.3.11"
var debugMode = flag.Bool("debug", false, "enable experimental application features")

func main() {
	flag.Parse()
	if err := run(*debugMode); errors.Is(err, errRestartRequested) {
		if restartErr := restartApplication(); restartErr != nil {
			showError(restartErr)
			os.Exit(1)
		}
	} else if err != nil {
		showError(err)
		os.Exit(1)
	}
}

func run(debugMode bool) (runErr error) {
	configPath, err := config.ExecutablePath()
	if err != nil {
		return err
	}
	cfg, created, err := config.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	cfg, err = normalizeLocalModelProfile(configPath, cfg, debugMode)
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
	if err := updater.RunMigrations(); err != nil {
		applicationLogger.Errorf("startup cleanup migrations failed; will retry on next start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executableDir := filepath.Dir(configPath)
	onnxLease, onnxErr := sharedort.Acquire(executableDir)
	onnxRuntimeAvailable := onnxErr == nil
	if onnxErr != nil {
		applicationLogger.Warnf("optional ONNX Runtime unavailable: %v", onnxErr)
	} else {
		defer func(lease *sharedort.Lease) {
			if err := lease.Close(); err != nil {
				applicationLogger.Printf("ONNX Runtime bootstrap lease shutdown failed: %v", err)
			}
		}(onnxLease)
	}
	var ocrEngine *ocr.PaddleEngine
	var ocrErr error
	if onnxRuntimeAvailable {
		ocrEngine, ocrErr = ocr.NewPaddleEngine(executableDir, ocr.DefaultTimeout, applicationLogger)
	} else {
		ocrErr = fmt.Errorf("requires ONNX Runtime: %w", onnxErr)
	}
	var localImageExtractor localmodel.ImageTextExtractor
	var batchOCR imagebatch.StructuredOCR
	if ocrErr != nil {
		applicationLogger.Warnf("optional OCR unavailable: %v", ocrErr)
	} else {
		localImageExtractor = ocrEngine
		batchOCR = ocrEngine
		defer func() {
			if err := ocrEngine.Close(); err != nil {
				applicationLogger.Printf("OCR backend shutdown failed: %v", err)
			}
		}()
	}
	classifier, err := language.NewWhatlangClassifier()
	if err != nil {
		return fmt.Errorf("configure language identification: %w", err)
	}
	identifier := language.NewLanguageIdentifier(classifier)
	selectedProvider, localLayout, err := localmodel.SelectProvider(cfg.Provider.Active, executableDir, cfg.LocalModel)
	if err != nil {
		return err
	}
	var startupWindow *window.StartupWindow
	if startupUIRequired(selectedProvider) {
		startupWindow = window.NewStartupWindow(cancel)
		if err := startupWindow.Start(); err != nil {
			startupWindow.Close()
			if startupWindow.WasClosedByUser() && errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return fmt.Errorf("create startup window: %w", err)
		}
		defer func(startup *window.StartupWindow) {
			startup.Close()
			runErr = startupError(runErr, startup.WasClosedByUser())
		}(startupWindow)
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
			Profile:            cfg.LocalModel.Profile,
			ExecutableDir:      executableDir,
			StartupTimeout:     time.Duration(cfg.LocalModel.StartupTimeoutSeconds) * time.Second,
			RequestTimeout:     time.Duration(cfg.Ollama.TimeoutSeconds) * time.Second,
			NumCtx:             cfg.LocalModel.ContextSize,
			NumPredict:         cfg.Ollama.NumPredict,
			Temperature:        cfg.Ollama.Temperature,
			FitTargetMiB:       cfg.LocalModel.FitTargetMiB,
			MaxInputCharacters: cfg.Limits.MaxInputCharacters,
			ImageTextExtractor: localImageExtractor,
			LanguageIdentifier: identifier,
		}, applicationLogger.Writer())
		if err != nil {
			return startupError(fmt.Errorf("start local provider: %w", err), startupWindow != nil && startupWindow.WasClosedByUser())
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
	if startupWasCancelled(startupWindow) {
		return nil
	}

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
	service := app.NewService(ctx, translator, identifier, cfg.DefaultLanguagePair.First.Active, cfg.DefaultLanguagePair.Second.Active, applicationLogger)
	completer, ok := translator.(translation.RawCompleter)
	if !ok {
		return errors.New("the selected provider does not support structured translation")
	}
	renderConfig := imagebatch.DefaultRenderConfig()
	renderConfig.MinimumFontSize = cfg.ImageBatch.MinimumFontSize
	renderConfig.MaximumFontSize = cfg.ImageBatch.MaximumFontSize
	renderConfig.LineSpacing = cfg.ImageBatch.LineSpacing
	renderConfig.JPEGQuality = cfg.ImageBatch.JPEGQuality
	var inpaintEngine inpaint.Engine
	var inpaintErr error
	if onnxRuntimeAvailable {
		inpaintEngine, inpaintErr = inpaint.NewEngine(executableDir)
	} else {
		inpaintErr = fmt.Errorf("requires ONNX Runtime: %w", onnxErr)
	}
	if inpaintErr != nil {
		applicationLogger.Warnf("optional inpaint / LaMa unavailable: %v", inpaintErr)
	}
	if onnxLease != nil {
		if err := onnxLease.Close(); err != nil {
			applicationLogger.Printf("ONNX Runtime bootstrap lease shutdown failed: %v", err)
		}
	}
	batchPrerequisites := imagebatch.NewPrerequisites(executableDir, batchOCR, inpaintEngine, onnxRuntimeAvailable)
	batchUnavailable := batchPrerequisites.Check()
	var batchService *imagebatch.Service
	if batchUnavailable == nil {
		batchService, err = imagebatch.NewService(ctx, executableDir, batchOCR, completer, cfg.Limits.MaxInputCharacters, renderConfig, inpaintEngine, applicationLogger)
		if err != nil {
			batchUnavailable = fmt.Errorf("Batch Images is unavailable: %w", err)
		}
	}
	if batchUnavailable != nil {
		applicationLogger.Warnf("optional Batch Images subsystem unavailable: %v", batchUnavailable)
		if inpaintEngine != nil && batchService == nil {
			if err := inpaintEngine.Close(); err != nil {
				applicationLogger.Printf("image inpaint shutdown failed: %v", err)
			}
			inpaintEngine = nil
		}
	}
	if startupWasCancelled(startupWindow) {
		if batchService != nil {
			batchService.Close()
			batchService.Wait()
		}
		return nil
	}
	clip := clipboard.New(applicationLogger)
	popup := window.NewPopup(cfg, html, service, clip)
	if err := popup.Start(); err != nil {
		if batchService != nil {
			batchService.Close()
			batchService.Wait()
		}
		return err
	}
	if startupWasCancelled(startupWindow) {
		popup.Close()
		if batchService != nil {
			batchService.Close()
			batchService.Wait()
		}
		return nil
	}
	var batchWindow *window.ImageBatchWindow
	if batchService != nil {
		batchWindow = window.NewImageBatchWindow(cfg, html, service, batchService, clip, popup)
		if err := batchWindow.Start(); err != nil {
			batchUnavailable = fmt.Errorf("Batch Images window is unavailable: %w", err)
			applicationLogger.Warnf("optional Batch Images window unavailable: %v", err)
			batchService.Close()
			batchService.Wait()
			batchService = nil
			batchWindow = nil
		}
	}
	shutdownImageBatch := func() {
		if batchService != nil {
			batchService.Close()
		}
		if batchWindow != nil {
			batchWindow.Close()
			batchWindow = nil
		}
		if batchService != nil {
			batchService.Wait()
			batchService = nil
		}
	}
	if startupWasCancelled(startupWindow) {
		shutdownImageBatch()
		popup.Close()
		return nil
	}
	targets := window.NewOriginTargetManager()
	controller := app.NewHotkeyController(ctx, cfg, service, completer, identifier, clip, targets, popup, applicationLogger)
	popup.SetQuickTranslationHandler(controller)
	hotkeyManager := hotkeys.NewManager(showCombination, replaceCombination, controller.ShowTranslation, controller.ReplaceSelection)
	if err := hotkeyManager.Start(); err != nil {
		controller.Close()
		shutdownImageBatch()
		popup.Close()
		return err
	}
	if startupWasCancelled(startupWindow) {
		hotkeyManager.Close()
		controller.Close()
		shutdownImageBatch()
		popup.Close()
		return nil
	}
	applicationLogger.Printf("global hotkeys registered: show=%s replace=%s", showCombination.Display, replaceCombination.Display)

	restartRequested := make(chan struct{}, 1)
	requestRestart := func() {
		select {
		case restartRequested <- struct{}{}:
		default:
		}
	}
	mainWindow := window.NewMainWindow(cfg, configPath, version, html, service, batchWindow, batchUnavailable, clip, popup, applicationLogger, debugMode, showError, requestRestart)
	systemTray := tray.New(mainWindow.Open, mainWindow.OpenSettings, fmt.Sprintf("Sympllate v%s", version), applicationLogger)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			service.Close()
			systemTray.Close()
			hotkeyManager.Close()
			mainWindow.Shutdown()
			shutdownImageBatch()
			cancel()
			controller.Close()
			service.Wait()
			if ocrEngine != nil {
				if err := ocrEngine.Close(); err != nil {
					applicationLogger.Printf("OCR backend shutdown failed: %v", err)
				}
			}
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
	if startupWasCancelled(startupWindow) {
		return nil
	}
	if err := systemTray.Start(); err != nil {
		return fmt.Errorf("start system tray: %w", err)
	}
	applicationLogger.Printf("system tray started")
	if startupWindow != nil {
		startupWindow.Close()
		if startupWindow.WasClosedByUser() {
			return nil
		}
		startupWindow = nil
	}
	if shouldOpenMainWindowOnStartup(cfg) {
		mainWindow.Open()
	}
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

func normalizeLocalModelProfile(configPath string, cfg config.Config, debugMode bool) (config.Config, error) {
	if debugMode || cfg.LocalModel.Profile != config.ProfileTranslateGemma {
		return cfg, nil
	}
	cfg.LocalModel.Profile = config.ProfileGeneric
	if err := config.Save(configPath, cfg); err != nil {
		return config.Config{}, fmt.Errorf("normalize localModel.profile for normal mode: %w", err)
	}
	return cfg, nil
}

func startupUIRequired(selectedProvider string) bool {
	return selectedProvider == config.ProviderLocal
}

func shouldOpenMainWindowOnStartup(cfg config.Config) bool {
	return !cfg.UI.HideIntoTrayOnStartup
}

func startupWasCancelled(startupWindow *window.StartupWindow) bool {
	return startupWindow != nil && startupWindow.WasClosedByUser()
}

func startupError(err error, userClosed bool) error {
	if userClosed && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
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
