package imagebatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type outputLayout struct {
	Root         string
	Images       string
	OCR          string
	Translations string
	Debug        string
}

func createOutputLayout(executableDir string, now time.Time, debug bool) (outputLayout, error) {
	parent := filepath.Join(executableDir, "_output")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return outputLayout{}, errors.New("Не удалось создать каталог результатов рядом с приложением. Проверьте права записи")
	}
	base := now.Format("2006-01-02_15-04-05")
	var root string
	for suffix := 1; ; suffix++ {
		name := base
		if suffix > 1 {
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		candidate := filepath.Join(parent, name)
		err := os.Mkdir(candidate, 0o755)
		if err == nil {
			root = candidate
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return outputLayout{}, errors.New("Не удалось создать каталог результатов рядом с приложением. Проверьте права записи")
		}
	}
	layout := outputLayout{
		Root: root, Images: filepath.Join(root, "images"), OCR: filepath.Join(root, "ocr"),
		Translations: filepath.Join(root, "translations"), Debug: filepath.Join(root, "debug"),
	}
	for _, directory := range []string{layout.Images, layout.OCR, layout.Translations} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			return outputLayout{}, fmt.Errorf("create output structure: %w", safePathError(err))
		}
	}
	if debug {
		if err := os.Mkdir(layout.Debug, 0o755); err != nil {
			return outputLayout{}, fmt.Errorf("create debug output directory: %w", safePathError(err))
		}
	}
	return layout, nil
}

func uniqueOutputNames(files []string) []string {
	used := make(map[string]int, len(files))
	result := make([]string, len(files))
	for index, path := range files {
		name := filepath.Base(path)
		extension := filepath.Ext(name)
		stem := strings.TrimSuffix(name, extension)
		key := strings.ToLower(name)
		used[key]++
		if used[key] == 1 {
			result[index] = name
			continue
		}
		for suffix := used[key]; ; suffix++ {
			candidate := fmt.Sprintf("%s_%d%s", stem, suffix, extension)
			candidateKey := strings.ToLower(candidate)
			if used[candidateKey] == 0 {
				used[candidateKey] = 1
				result[index] = candidate
				break
			}
		}
	}
	return result
}

func atomicWriteJSON(path string, value any) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".sympllate-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary JSON: %w", safePathError(err))
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode JSON: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync JSON: %w", safePathError(err))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close JSON: %w", safePathError(err))
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit JSON: %w", safePathError(err))
	}
	keep = true
	return nil
}

func atomicWriteBytes(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".sympllate-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", safePathError(err))
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write file: %w", safePathError(err))
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync file: %w", safePathError(err))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close file: %w", safePathError(err))
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit file: %w", safePathError(err))
	}
	keep = true
	return nil
}

func relativeOutputPath(directory, path string) string {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(relative)
}
