//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sympllate/translator/internal/config"
)

func TestNormalizeLocalModelProfileForNormalMode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.LocalModel.Profile = config.ProfileTranslateGemma
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeLocalModelProfile(path, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalModel.Profile != config.ProfileGeneric {
		t.Fatalf("normalized profile = %q", got.LocalModel.Profile)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LocalModel.Profile != config.ProfileGeneric {
		t.Fatalf("saved profile = %q", saved.LocalModel.Profile)
	}
}

func TestNormalizeLocalModelProfileKeepsDebugProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.LocalModel.Profile = config.ProfileTranslateGemma
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeLocalModelProfile(path, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalModel.Profile != config.ProfileTranslateGemma {
		t.Fatalf("debug profile = %q", got.LocalModel.Profile)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LocalModel.Profile != config.ProfileTranslateGemma {
		t.Fatalf("saved debug profile = %q", saved.LocalModel.Profile)
	}
}

func TestNormalizeLocalModelProfileKeepsRawProfileInNormalMode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.LocalModel.Profile = config.ProfileTranslateGemmaRaw
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeLocalModelProfile(path, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalModel.Profile != config.ProfileTranslateGemmaRaw {
		t.Fatalf("normal-mode raw profile = %q", got.LocalModel.Profile)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LocalModel.Profile != config.ProfileTranslateGemmaRaw {
		t.Fatalf("saved raw profile = %q", saved.LocalModel.Profile)
	}
}

func TestNormalizeLocalModelProfileReturnsSaveError(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.LocalModel.Profile = config.ProfileTranslateGemma
	_, err := normalizeLocalModelProfile(t.TempDir(), cfg, false)
	if err == nil {
		t.Fatal("expected profile normalization save error")
	}
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		t.Fatalf("normalization error = %v", err)
	}
}

func TestStartupUIRequiredForSelectedProvider(t *testing.T) {
	t.Parallel()
	if !startupUIRequired(config.ProviderLocal) {
		t.Fatal("local provider should show startup UI")
	}
	for _, provider := range []string{config.ProviderOllama, config.ProviderAuto, ""} {
		if startupUIRequired(provider) {
			t.Fatalf("provider %q should not show startup UI", provider)
		}
	}
}

func TestStartupErrorSuppressesUserCancellation(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("start local provider: %w", context.Canceled)
	if err := startupError(wrapped, true); err != nil {
		t.Fatalf("startupError() = %v, want nil", err)
	}
}

func TestStartupErrorDoesNotSuppressOtherFailures(t *testing.T) {
	t.Parallel()
	realFailure := errors.New("llama-server crashed")
	if err := startupError(realFailure, true); !errors.Is(err, realFailure) {
		t.Fatalf("startupError(real failure) = %v", err)
	}
	if err := startupError(context.Canceled, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("startupError(non-user cancellation) = %v", err)
	}
}
