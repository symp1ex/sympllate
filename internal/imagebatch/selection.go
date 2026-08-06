package imagebatch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultSelectionTTL = 30 * time.Minute

var supportedExtensions = map[string]struct{}{".png": {}, ".jpg": {}, ".jpeg": {}}

type storedBatchSelection struct {
	BatchSelection
	Files     []string
	CreatedAt time.Time
}

type SelectionStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	now  func() time.Time
	data map[string]storedBatchSelection
}

func NewSelectionStore(ttl time.Duration) *SelectionStore {
	if ttl <= 0 {
		ttl = DefaultSelectionTTL
	}
	return &SelectionStore{ttl: ttl, now: time.Now, data: make(map[string]storedBatchSelection)}
}

func (s *SelectionStore) CreateFiles(paths []string) (BatchSelection, error) {
	files := supportedPaths(paths)
	if len(files) == 0 {
		return BatchSelection{}, errors.New("no supported PNG or JPEG images were selected")
	}
	sort.SliceStable(files, func(i, j int) bool { return naturalLess(strings.ToLower(files[i]), strings.ToLower(files[j])) })
	displayName := "Selected images"
	if len(files) == 1 {
		displayName = filepath.Base(files[0])
	}
	return s.store(SelectionFiles, displayName, files)
}

func (s *SelectionStore) CreateDirectory(directory string) (BatchSelection, error) {
	clean := filepath.Clean(directory)
	entries, err := os.ReadDir(clean)
	if err != nil {
		return BatchSelection{}, fmt.Errorf("read selected directory: %w", safePathError(err))
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.EqualFold(name, "_output") || isServiceFileName(name) {
			continue
		}
		if _, ok := supportedExtensions[strings.ToLower(filepath.Ext(name))]; !ok {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || fileIsHidden(info) {
			continue
		}
		files = append(files, filepath.Join(clean, name))
	}
	sort.SliceStable(files, func(i, j int) bool {
		return naturalLess(strings.ToLower(filepath.Base(files[i])), strings.ToLower(filepath.Base(files[j])))
	})
	if len(files) == 0 {
		return BatchSelection{}, errors.New("the selected directory contains no supported PNG or JPEG images")
	}
	return s.store(SelectionDirectory, filepath.Base(clean), files)
}

func (s *SelectionStore) Take(id string) (storedBatchSelection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	selection, ok := s.data[id]
	if !ok {
		return storedBatchSelection{}, fmt.Errorf("batch selection %q was not found or has expired", id)
	}
	delete(s.data, id)
	selection.Files = append([]string(nil), selection.Files...)
	return selection, nil
}

func (s *SelectionStore) Peek(id string) (storedBatchSelection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	selection, ok := s.data[id]
	if !ok {
		return storedBatchSelection{}, fmt.Errorf("batch selection %q was not found or has expired", id)
	}
	selection.Files = append([]string(nil), selection.Files...)
	return selection, nil
}

func (s *SelectionStore) Cleanup() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.data)
	s.cleanupLocked()
	return before - len(s.data)
}

func (s *SelectionStore) store(kind BatchSelectionKind, displayName string, files []string) (BatchSelection, error) {
	id, err := randomID("selection-")
	if err != nil {
		return BatchSelection{}, err
	}
	selection := BatchSelection{ID: id, Kind: kind, DisplayName: displayName, FileCount: len(files)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.data[id] = storedBatchSelection{BatchSelection: selection, Files: append([]string(nil), files...), CreatedAt: s.now()}
	return selection, nil
}

func (s *SelectionStore) cleanupLocked() {
	cutoff := s.now().Add(-s.ttl)
	for id, selection := range s.data {
		if !selection.CreatedAt.After(cutoff) {
			delete(s.data, id)
		}
	}
}

func supportedPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if _, ok := supportedExtensions[strings.ToLower(filepath.Ext(clean))]; !ok {
			continue
		}
		key := strings.ToLower(clean)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, clean)
	}
	return result
}

func isServiceFileName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~") || strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".part")
}

func naturalLess(left, right string) bool {
	li, ri := 0, 0
	for li < len(left) && ri < len(right) {
		if isDigit(left[li]) && isDigit(right[ri]) {
			lStart, rStart := li, ri
			for li < len(left) && left[li] == '0' {
				li++
			}
			for ri < len(right) && right[ri] == '0' {
				ri++
			}
			lDigits, rDigits := li, ri
			for li < len(left) && isDigit(left[li]) {
				li++
			}
			for ri < len(right) && isDigit(right[ri]) {
				ri++
			}
			if li-lDigits != ri-rDigits {
				return li-lDigits < ri-rDigits
			}
			if value := strings.Compare(left[lDigits:li], right[rDigits:ri]); value != 0 {
				return value < 0
			}
			if li-lStart != ri-rStart {
				return li-lStart < ri-rStart
			}
			continue
		}
		if left[li] != right[ri] {
			return left[li] < right[ri]
		}
		li++
		ri++
	}
	return len(left) < len(right)
}

func isDigit(value byte) bool { return value >= '0' && value <= '9' }

func randomID(prefix string) (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + hex.EncodeToString(data), nil
}

func safePathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
