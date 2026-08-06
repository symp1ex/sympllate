package imagebatch

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCreateOutputLayoutTimestampCollisionAndDebug(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, 8, 6, 4, 45, 0, 0, time.Local)
	first, err := createOutputLayout(base, now, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := createOutputLayout(base, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first.Root) != "2026-08-06_04-45-00" || filepath.Base(second.Root) != "2026-08-06_04-45-00_2" {
		t.Fatalf("roots=%q %q", first.Root, second.Root)
	}
	if _, err := os.Stat(first.Debug); !os.IsNotExist(err) {
		t.Fatalf("unexpected first debug dir: %v", err)
	}
	if info, err := os.Stat(second.Debug); err != nil || !info.IsDir() {
		t.Fatalf("debug dir: %v", err)
	}
}

func TestCreateOutputLayoutReportsPermissionLikeFailure(t *testing.T) {
	base := t.TempDir()
	executable := filepath.Join(base, "not-a-directory")
	if err := os.WriteFile(executable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := createOutputLayout(executable, time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "Не удалось создать каталог") {
		t.Fatalf("err=%v", err)
	}
}

func TestUniqueOutputNamesAreDeterministic(t *testing.T) {
	files := []string{`C:\a\page.png`, `C:\b\page_2.png`, `C:\c\page.png`, `C:\d\PAGE.PNG`}
	want := []string{"page.png", "page_2.png", "page_3.png", "PAGE_4.PNG"}
	if got := uniqueOutputNames(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestAtomicWriteJSONReplacesCompleteDocumentAndCleansTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "job.json")
	if err := atomicWriteJSON(path, map[string]int{"value": 1}); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(path, map[string]int{"value": 2}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"value": 2`) {
		t.Fatalf("data=%s", data)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 || entries[0].Name() != "job.json" {
		t.Fatalf("entries=%v", entries)
	}
}
