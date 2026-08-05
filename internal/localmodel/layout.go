package localmodel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sympllate/translator/internal/config"
)

const ModelAlias = "local-model"

type Layout struct {
	ServerPath string
	RuntimeDir string
	ModelPath  string
}

func ResolveLayout(executableDir string, cfg config.LocalModelConfig) (Layout, error) {
	serverPath := filepath.Join(executableDir, "runtime", "llama", "llama-server.exe")
	if err := requireFile(serverPath); err != nil {
		return Layout{}, fmt.Errorf("локальный runtime не найден: %w", err)
	}
	modelPath, err := ResolveModel(executableDir, cfg.ModelFile)
	if err != nil {
		return Layout{}, err
	}
	return Layout{ServerPath: serverPath, RuntimeDir: filepath.Dir(serverPath), ModelPath: modelPath}, nil
}

func ResolveModel(executableDir, configuredPath string) (string, error) {
	if strings.TrimSpace(configuredPath) != "" {
		path := configuredPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(executableDir, path)
		}
		path = filepath.Clean(path)
		if !strings.EqualFold(filepath.Ext(path), ".gguf") {
			return "", fmt.Errorf("локальная модель должна иметь расширение .gguf: %q", path)
		}
		if err := requireFile(path); err != nil {
			return "", fmt.Errorf("локальная модель недоступна: %w", err)
		}
		return path, nil
	}

	modelsDir := filepath.Join(executableDir, "models")
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("в %q не найдена GGUF-модель", modelsDir)
		}
		return "", fmt.Errorf("прочитать каталог моделей %q: %w", modelsDir, err)
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		matches = append(matches, filepath.Join(modelsDir, entry.Name()))
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("в %q не найдена GGUF-модель", modelsDir)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("в %q найдено несколько GGUF-моделей; укажите localModel.modelFile", modelsDir)
	}
}

func SelectProvider(provider, executableDir string, cfg config.LocalModelConfig) (string, Layout, error) {
	switch provider {
	case config.ProviderOllama:
		return config.ProviderOllama, Layout{}, nil
	case config.ProviderLocal:
		layout, err := ResolveLayout(executableDir, cfg)
		if err != nil {
			return "", Layout{}, fmt.Errorf("provider local: %w", err)
		}
		return config.ProviderLocal, layout, nil
	case config.ProviderAuto:
		layout, err := ResolveLayout(executableDir, cfg)
		if err != nil {
			return config.ProviderOllama, Layout{}, nil
		}
		return config.ProviderLocal, layout, nil
	default:
		return "", Layout{}, fmt.Errorf("неподдерживаемый provider %q", provider)
	}
}

func BuildArguments(layout Layout, port int, apiKey string, numCtx, fitTargetMiB int) []string {
	return []string{
		"--model", layout.ModelPath,
		"--alias", ModelAlias,
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--api-key", apiKey,
		"--no-webui",
		"--no-jinja",
		"--offline",
		"--parallel", "1",
		"--ctx-size", fmt.Sprintf("%d", numCtx),
		"--gpu-layers", "auto",
		"--fit", "on",
		"--fit-target", fmt.Sprintf("%d", fitTargetMiB),
	}
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q не является файлом", path)
	}
	return nil
}
