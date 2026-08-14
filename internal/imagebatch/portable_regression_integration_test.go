//go:build windows

package imagebatch_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/imagebatch"
	"github.com/sympllate/translator/internal/inpaint"
	"github.com/sympllate/translator/internal/localmodel"
	"github.com/sympllate/translator/internal/ocr"
)

// TestPortableImageRegression is an opt-in end-to-end harness for the shipped
// Windows runtime. It deliberately runs the current packages beside the old
// portable translator.exe; the executable is never replaced or launched.
func TestPortableImageRegression(t *testing.T) {
	portable := strings.TrimSpace(os.Getenv("SYMPLLATE_PORTABLE_REGRESSION"))
	if portable == "" {
		t.Skip("set SYMPLLATE_PORTABLE_REGRESSION to the portable application directory")
	}
	portable, err := filepath.Abs(portable)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(portable, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	layout, err := localmodel.ResolveLayout(portable, cfg.LocalModel)
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(os.Stdout, "regression: ", log.LstdFlags|log.Lmicroseconds)
	ocrEngine, err := ocr.NewPaddleEngine(portable, time.Duration(cfg.Ollama.TimeoutSeconds)*time.Second, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer ocrEngine.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Minute)
	defer cancel()
	runtime, err := localmodel.Start(ctx, localmodel.RuntimeConfig{
		Layout: layout, ExecutableDir: portable,
		StartupTimeout: time.Duration(cfg.LocalModel.StartupTimeoutSeconds) * time.Second,
		RequestTimeout: time.Duration(cfg.Ollama.TimeoutSeconds) * time.Second,
		NumCtx:         cfg.Ollama.NumCtx, NumPredict: cfg.Ollama.NumPredict, Temperature: cfg.Ollama.Temperature,
		FitTargetMiB: cfg.LocalModel.FitTargetMiB, MaxInputCharacters: cfg.Limits.MaxInputCharacters,
		ImageTextExtractor: ocrEngine,
	}, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	inpaintEngine, err := inpaint.NewEngine(portable)
	if err != nil {
		t.Fatal(err)
	}
	renderConfig := imagebatch.DefaultRenderConfig()
	renderConfig.MinimumFontSize = cfg.ImageBatch.MinimumFontSize
	renderConfig.MaximumFontSize = cfg.ImageBatch.MaximumFontSize
	renderConfig.LineSpacing = cfg.ImageBatch.LineSpacing
	renderConfig.JPEGQuality = cfg.ImageBatch.JPEGQuality
	service, err := imagebatch.NewService(ctx, portable, ocrEngine, runtime.Client(), cfg.Limits.MaxInputCharacters, renderConfig, inpaintEngine, logger)
	if err != nil {
		_ = inpaintEngine.Close()
		t.Fatal(err)
	}
	defer func() {
		service.Close()
		service.Wait()
	}()
	selection, err := service.SelectFiles(portableRegressionImages(portable))
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := service.Start(imagebatch.StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru", Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	for {
		status, statusErr := service.Status(jobID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.State == "completed" || status.State == "completed_with_errors" || status.State == "cancelled" || status.State == "failed" {
			t.Logf("portable regression state=%s processed=%d rendered=%d partial=%d failed=%d output=%s error=%s", status.State, status.Processed, status.Rendered, status.Partial, status.Failed, status.OutputDirectory, status.Error)
			if status.State != "completed" && status.State != "completed_with_errors" {
				t.Fatalf("regression job did not complete: %+v", status)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func portableRegressionImages(portable string) []string {
	repository := filepath.Dir(filepath.Dir(portable))
	document := filepath.Join(repository, "_resources", "images", "99920-2296-05-o5fx801v-us-en-tws.pdf")
	tax := filepath.Join(repository, "_resources", "images", "TAX and Service.pdf")
	names := []string{
		filepath.Join(document, "99920-2296-05-o5fx801v-us-en-tws-14.png"),
		filepath.Join(document, "99920-2296-05-o5fx801v-us-en-tws-18.png"),
	}
	for page := 1; page <= 5; page++ {
		names = append(names, filepath.Join(tax, fmt.Sprintf("TAX and Service-%d.png", page)))
	}
	return names
}
