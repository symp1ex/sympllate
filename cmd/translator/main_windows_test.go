//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sympllate/translator/internal/config"
)

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
