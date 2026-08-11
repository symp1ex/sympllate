package imagebatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/ocr"
	"github.com/sympllate/translator/internal/translation"
	"golang.org/x/image/font/gofont/goregular"
)

type fakeBatchOCR struct {
	mu         sync.Mutex
	capability translation.ImageCapability
	pages      []ocr.OCRPage
	err        error
	block      bool
	calls      int
}

func (f *fakeBatchOCR) Capability() translation.ImageCapability {
	if !f.capability.Supported && f.capability.Reason == "" {
		return translation.ImageCapability{Supported: true}
	}
	return f.capability
}

func (f *fakeBatchOCR) RecognizeStructured(ctx context.Context, _ translation.ValidatedImage, _ string) (ocr.OCRPage, error) {
	f.mu.Lock()
	f.calls++
	index := f.calls - 1
	block, err := f.block, f.err
	pages := f.pages
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return ocr.OCRPage{}, ctx.Err()
	}
	if err != nil {
		return ocr.OCRPage{}, err
	}
	if index < len(pages) {
		return pages[index], nil
	}
	return translatedOCRPage(), nil
}

type fakeBatchCompleter struct {
	mu    sync.Mutex
	calls int
	err   error
	block bool
}

type sequenceBatchCompleter struct {
	mu        sync.Mutex
	responses []string
	calls     int
}

func (f *sequenceBatchCompleter) Complete(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	response := f.responses[min(f.calls, len(f.responses)-1)]
	f.calls++
	return response, nil
}

func (f *fakeBatchCompleter) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func (f *fakeBatchCompleter) Complete(ctx context.Context, _ string) (string, error) {
	f.mu.Lock()
	f.calls++
	block, err := f.block, f.err
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return `{"blocks":[{"id":"p1-b1-par1","text":"Перевод"}]}`, nil
}

func TestBatchLifecycleSuccessWritesDocumentsAndDebug(t *testing.T) {
	executableDir := t.TempDir()
	imagePath := writeBatchImage(t, executableDir, "page.png")
	recognizer := &fakeBatchOCR{pages: []ocr.OCRPage{translatedOCRPage()}}
	completer := &fakeBatchCompleter{}
	service := newBatchTestService(t, executableDir, recognizer, completer)
	opened := make(chan string, 1)
	service.openDirectory = func(path string) error { opened <- path; return nil }
	selection, _ := service.SelectFiles([]string{imagePath})
	id, err := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru", Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	status := waitBatch(t, service, id)
	var openedPath string
	select {
	case openedPath = <-opened:
	case <-time.After(time.Second):
		t.Fatal("output directory was not opened")
	}
	if status.State != "completed" || status.Processed != 1 || status.Translated != 1 || openedPath != status.OutputDirectory {
		t.Fatalf("status=%+v opened=%q", status, openedPath)
	}
	for _, relative := range []string{"images/page.png", "translated/page.png", "ocr/page.ocr.json", "translations/page.translation.json", "debug/page.ocr.png", "debug/page.cleaned.png", "debug/page.layout.png", "debug/page.render.json", "job.json", "errors.json"} {
		if _, err := os.Stat(filepath.Join(status.OutputDirectory, filepath.FromSlash(relative))); err != nil {
			t.Errorf("missing %s: %v", relative, err)
		}
	}
	var document TranslationDocument
	readJSON(t, filepath.Join(status.OutputDirectory, "translations", "page.translation.json"), &document)
	if document.Status != "translated" || len(document.Blocks) != 1 || document.Blocks[0].Box.Width != 120 {
		t.Fatalf("translation=%+v", document)
	}
	var report JobReport
	readJSON(t, filepath.Join(status.OutputDirectory, "job.json"), &report)
	if report.State != "completed" || report.CompletedAt == nil || len(report.Files) != 1 || report.Files[0].SourceID == "" {
		t.Fatalf("report=%+v", report)
	}
}

func TestBatchNoTextSkipsModel(t *testing.T) {
	directory := t.TempDir()
	path := writeBatchImage(t, directory, "blank.png")
	recognizer := &fakeBatchOCR{pages: []ocr.OCRPage{{SchemaVersion: 1, Paragraphs: []ocr.OCRParagraph{}, Words: []ocr.OCRWord{}}}}
	completer := &fakeBatchCompleter{}
	service := newBatchTestService(t, directory, recognizer, completer)
	selection, _ := service.SelectFiles([]string{path})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "auto", Target: "ru"})
	status := waitBatch(t, service, id)
	if status.State != "completed" || status.NoText != 1 || completer.calls != 0 {
		t.Fatalf("status=%+v calls=%d", status, completer.calls)
	}
	var document TranslationDocument
	readJSON(t, filepath.Join(status.OutputDirectory, "translations", "blank.translation.json"), &document)
	if document.Status != "no_text" || len(document.Blocks) != 0 {
		t.Fatalf("document=%+v", document)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(filepath.Join(status.OutputDirectory, "translated", "blank.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, output) {
		t.Fatal("no-text output was re-encoded")
	}
}

func TestBatchProtocolFailureFallsBackPerBlockAndRendersPartial(t *testing.T) {
	directory := t.TempDir()
	path := writeBatchImage(t, directory, "partial.png")
	page := translatedOCRPage()
	second := page.Paragraphs[0]
	second.ID, second.Text, second.Box.X = "p1-b2-par1", "Second", 150
	second.Lines[0].ID, second.Lines[0].Text, second.Lines[0].Box.X = "p1-b2-par1-l1", "Second", 150
	page.Paragraphs = append(page.Paragraphs, second)
	completer := &sequenceBatchCompleter{responses: []string{
		`not json`, `still not json`,
		`{"blocks":[{"id":"p1-b1-par1","text":"Перевод"}]}`,
		`not json`, `still not json`,
	}}
	service := newBatchTestService(t, directory, &fakeBatchOCR{pages: []ocr.OCRPage{page}}, completer)
	selection, _ := service.SelectFiles([]string{path})
	id, err := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	status := waitBatch(t, service, id)
	if status.State != "completed" || status.Partial != 1 || status.Rendered != 1 || status.Failed != 0 {
		t.Fatalf("status=%+v", status)
	}
	var report JobReport
	readJSON(t, filepath.Join(status.OutputDirectory, "job.json"), &report)
	if len(report.Files) != 1 || report.Files[0].Status != "partial" || report.Files[0].RenderedBlocks != 1 || len(report.Files[0].SkippedBlocks) != 1 {
		t.Fatalf("report=%+v", report)
	}
	var document TranslationDocument
	readJSON(t, filepath.Join(status.OutputDirectory, "translations", "partial.translation.json"), &document)
	if document.Status != "partial" || document.Blocks[1].Status != "failed" || document.Blocks[1].TranslatedText != "" {
		t.Fatalf("translation=%+v", document)
	}
}

func TestBatchFileFailureContinuesAndCompletesWithErrors(t *testing.T) {
	directory := t.TempDir()
	broken := filepath.Join(directory, "broken.png")
	_ = os.WriteFile(broken, []byte("broken"), 0o600)
	valid := writeBatchImage(t, directory, "valid.png")
	service := newBatchTestService(t, directory, &fakeBatchOCR{}, &fakeBatchCompleter{})
	selection, _ := service.SelectFiles([]string{broken, valid})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"})
	status := waitBatch(t, service, id)
	if status.State != "completed_with_errors" || status.Processed != 2 || status.Failed != 1 || status.Translated != 1 {
		t.Fatalf("status=%+v", status)
	}
	var errorsDocument ErrorsDocument
	readJSON(t, filepath.Join(status.OutputDirectory, "errors.json"), &errorsDocument)
	if len(errorsDocument.Errors) != 1 || errorsDocument.Errors[0].Stage != "validate" {
		t.Fatalf("errors=%+v", errorsDocument)
	}
}

func TestBatchCompletionFailureIsSystemic(t *testing.T) {
	directory := t.TempDir()
	path := writeBatchImage(t, directory, "page.png")
	service := newBatchTestService(t, directory, &fakeBatchOCR{}, &fakeBatchCompleter{err: errors.New("offline")})
	selection, _ := service.SelectFiles([]string{path})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"})
	status := waitBatch(t, service, id)
	if status.State != "failed" || status.Failed != 1 || status.Error == "" {
		t.Fatalf("status=%+v", status)
	}
}

func TestBatchCancellationDuringOCRIsIdempotentAndAllowsNewJob(t *testing.T) {
	directory := t.TempDir()
	first := writeBatchImage(t, directory, "first.png")
	second := writeBatchImage(t, directory, "second.png")
	recognizer := &fakeBatchOCR{block: true}
	service := newBatchTestService(t, directory, recognizer, &fakeBatchCompleter{})
	one, _ := service.SelectFiles([]string{first})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: one.ID, Source: "en", Target: "ru"})
	two, _ := service.SelectFiles([]string{second})
	if _, err := service.Start(StartImageBatchRequest{SelectionID: two.ID, Source: "en", Target: "ru"}); err == nil {
		t.Fatal("expected parallel job rejection")
	}
	if err := service.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if err := service.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if status := waitBatch(t, service, id); status.State != "cancelled" {
		t.Fatalf("status=%+v", status)
	}
	recognizer.mu.Lock()
	recognizer.block = false
	recognizer.mu.Unlock()
	newID, err := service.Start(StartImageBatchRequest{SelectionID: two.ID, Source: "en", Target: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if status := waitBatch(t, service, newID); status.State != "completed" {
		t.Fatalf("status=%+v", status)
	}
}

func TestBatchCancellationDuringTranslation(t *testing.T) {
	directory := t.TempDir()
	path := writeBatchImage(t, directory, "page.png")
	completer := &fakeBatchCompleter{block: true}
	service := newBatchTestService(t, directory, &fakeBatchOCR{}, completer)
	selection, _ := service.SelectFiles([]string{path})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"})
	deadline := time.Now().Add(time.Second)
	for completer.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := service.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if status := waitBatch(t, service, id); status.State != "cancelled" {
		t.Fatalf("status=%+v", status)
	}
}

func TestBatchShutdownCancelsActiveJob(t *testing.T) {
	directory := t.TempDir()
	path := writeBatchImage(t, directory, "page.png")
	service := newBatchTestService(t, directory, &fakeBatchOCR{block: true}, &fakeBatchCompleter{})
	selection, _ := service.SelectFiles([]string{path})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"})
	service.Close()
	service.Wait()
	status, err := service.Status(id)
	if err != nil || status.State != "cancelled" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, err := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"}); err == nil {
		t.Fatal("expected shutdown rejection")
	}
}

func TestBatchPreflightAndExplorerFailure(t *testing.T) {
	directory := t.TempDir()
	path := writeBatchImage(t, directory, "page.png")
	unavailable := newBatchTestService(t, directory, &fakeBatchOCR{capability: translation.ImageCapability{Reason: "missing tesseract"}}, &fakeBatchCompleter{})
	selection, _ := unavailable.SelectFiles([]string{path})
	if _, err := unavailable.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"}); err == nil {
		t.Fatal("expected OCR preflight error")
	}
	service := newBatchTestService(t, directory, &fakeBatchOCR{}, &fakeBatchCompleter{})
	service.openDirectory = func(string) error { return errors.New("Explorer unavailable") }
	selection, _ = service.SelectFiles([]string{path})
	id, _ := service.Start(StartImageBatchRequest{SelectionID: selection.ID, Source: "en", Target: "ru"})
	if status := waitBatch(t, service, id); status.State != "completed" {
		t.Fatalf("status=%+v", status)
	}
}

func newBatchTestService(t *testing.T, executableDir string, recognizer StructuredOCR, completer translation.RawCompleter) *Service {
	t.Helper()
	writeTestFont(t, executableDir)
	service, err := NewService(context.Background(), executableDir, recognizer, completer, 12_000, DefaultRenderConfig(), &fakeInpaintEngine{}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	service.openDirectory = func(string) error { return nil }
	return service
}

func waitBatch(t *testing.T, service *Service, id string) ImageBatchStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.Status(id)
		if err != nil {
			t.Fatal(err)
		}
		if terminalState(status.State) {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("batch did not complete")
	return ImageBatchStatus{}
}

func writeBatchImage(t *testing.T, directory, name string) string {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 200, 100))
	value.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func translatedOCRPage() ocr.OCRPage {
	word := ocr.OCRWord{ID: "p1-b1-par1-l1-w1", Text: "Source", Confidence: 90, Box: ocr.OCRBox{X: 10, Y: 10, Width: 120, Height: 30}, Accepted: true, Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1}
	line := ocr.OCRLine{ID: "p1-b1-par1-l1", Text: "Source", Confidence: 90, Box: word.Box, Words: []ocr.OCRWord{word}, Page: 1, Block: 1, Paragraph: 1, Line: 1}
	paragraph := ocr.OCRParagraph{ID: "p1-b1-par1", Text: "Source", Confidence: 90, Box: word.Box, Lines: []ocr.OCRLine{line}, Page: 1, Block: 1, Paragraph: 1}
	return ocr.OCRPage{SchemaVersion: 1, Image: ocr.OCRImageInfo{Width: 200, Height: 100, MediaType: "image/png"}, Words: []ocr.OCRWord{word}, Paragraphs: []ocr.OCRParagraph{paragraph}}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func writeTestFont(t *testing.T, executableDir string) {
	t.Helper()
	directory := filepath.Join(executableDir, "bin", "fonts")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "regular.ttf"), goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
}
