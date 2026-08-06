package imagebatch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSelectionStoreCreatesFileSelectionAndFiltersFormats(t *testing.T) {
	store := NewSelectionStore(time.Minute)
	selection, err := store.CreateFiles([]string{`C:\pages\page-10.png`, `C:\pages\notes.txt`, `C:\pages\page-2.jpg`, `C:\pages\page-2.jpg`})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Kind != SelectionFiles || selection.FileCount != 2 {
		t.Fatalf("selection=%+v", selection)
	}
	stored, err := store.Peek(selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{filepath.Base(stored.Files[0]), filepath.Base(stored.Files[1])}; !reflect.DeepEqual(got, []string{"page-2.jpg", "page-10.png"}) {
		t.Fatalf("files=%v", got)
	}
}

func TestSelectionStoreCreatesNaturallySortedDirectorySelection(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"page-10.png", "page-2.png", "page-1.jpg", "ignored.webp", ".hidden.png", "temp.tmp"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Symlink(filepath.Join(directory, "page-1.jpg"), filepath.Join(directory, "link.png"))
	store := NewSelectionStore(time.Minute)
	selection, err := store.CreateDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Peek(selection.ID)
	got := make([]string, 0, len(stored.Files))
	for _, path := range stored.Files {
		got = append(got, filepath.Base(path))
	}
	if !reflect.DeepEqual(got, []string{"ignored.webp", "page-1.jpg", "page-2.png", "page-10.png"}) {
		t.Fatalf("files=%v", got)
	}
}

func TestSelectionStoreRejectsEmptyDirectoryAndUnknownID(t *testing.T) {
	store := NewSelectionStore(time.Minute)
	if _, err := store.CreateDirectory(t.TempDir()); err == nil {
		t.Fatal("expected empty directory error")
	}
	if _, err := store.Take("missing"); err == nil {
		t.Fatal("expected unknown selection error")
	}
}

func TestSelectionStoreExpiresConsumesAndCleansSelections(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 45, 0, 0, time.UTC)
	store := NewSelectionStore(time.Minute)
	store.now = func() time.Time { return now }
	expired, _ := store.CreateFiles([]string{"old.png"})
	now = now.Add(2 * time.Minute)
	if _, err := store.Peek(expired.ID); err == nil {
		t.Fatal("expected expired selection")
	}
	fresh, _ := store.CreateFiles([]string{"fresh.png"})
	if _, err := store.Take(fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Peek(fresh.ID); err == nil {
		t.Fatal("selection was not consumed")
	}
	if removed := store.Cleanup(); removed != 0 {
		t.Fatalf("removed=%d", removed)
	}
}
