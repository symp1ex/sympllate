package ocr

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/translation"
)

type recordedProcessCall struct {
	executable string
	args       []string
}

type fakeOCRProcesses struct {
	engine       *Engine
	calls        []recordedProcessCall
	tesseractTSV func(call int, args []string) string
	ffmpegErr    error
	tesseractErr error
	block        bool
	oversized    bool
	skipOutput   bool
	temporaryDir string
}

func (f *fakeOCRProcesses) run(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, recordedProcessCall{executable: executable, args: append([]string(nil), args...)})
	if executable == f.engine.ffmpegPath {
		if inputIndex := argumentIndex(args, "-i"); inputIndex >= 0 && inputIndex+1 < len(args) {
			f.temporaryDir = filepath.Dir(args[inputIndex+1])
		}
		if f.block {
			<-ctx.Done()
			return ctx.Err()
		}
		if f.ffmpegErr != nil {
			_, _ = io.WriteString(stderr, "decoder failed for "+argumentAfter(args, "-i"))
			return f.ffmpegErr
		}
		if f.skipOutput {
			return nil
		}
		width, height := scaleDimensions(argumentAfter(args, "-vf"))
		return writePNGHeader(args[len(args)-1], width, height)
	}
	if executable == f.engine.executablePath {
		if f.block {
			<-ctx.Done()
			return ctx.Err()
		}
		if f.tesseractErr != nil {
			_, _ = io.WriteString(stderr, "recognizer failed for "+args[0])
			return f.tesseractErr
		}
		if f.oversized {
			_, _ = stdout.Write(bytes.Repeat([]byte("x"), maxStructuredOutputBytes+1))
			return nil
		}
		call := countExecutable(f.calls, f.engine.executablePath) - 1
		value := tsvHeader
		if f.tesseractTSV != nil {
			value = f.tesseractTSV(call, args)
		}
		_, _ = io.WriteString(stdout, value)
		return nil
	}
	return errors.New("unexpected executable")
}

func TestEnginePreprocessesBeforeTesseractUpscalesAndCleansTemporaryDirectory(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	fake := &fakeOCRProcesses{engine: engine, tesseractTSV: func(_ int, _ []string) string {
		return tsvHeader + tsvRow(1, 1, 1, 1, 1, 40, 40, 200, 80, "90", "recognized")
	}}
	engine.run = fake.run
	page, err := engine.RecognizeStructured(context.Background(), testImage(100, 60), "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 || fake.calls[0].executable != engine.ffmpegPath || fake.calls[1].executable != engine.executablePath {
		t.Fatalf("process order=%+v", fake.calls)
	}
	filter := argumentAfter(fake.calls[0].args, "-vf")
	if !strings.Contains(filter, "scale=400:240:flags=lanczos") || !strings.Contains(filter, "format=gray") || !strings.Contains(filter, "contrast=1.08") {
		t.Fatalf("FFmpeg filter=%q", filter)
	}
	if got := argumentAfter(fake.calls[1].args, "--psm"); got != "3" {
		t.Fatalf("full PSM=%q args=%q", got, fake.calls[1].args)
	}
	if page.Image.Width != 100 || page.Image.Height != 60 || page.Words[0].Box != (OCRBox{X: 10, Y: 10, Width: 50, Height: 20}) {
		t.Fatalf("page=%+v", page)
	}
	if _, err := os.Stat(fake.temporaryDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory still exists: %q, %v", fake.temporaryDir, err)
	}
}

func TestEngineRecognizeUsesSameMergedPageText(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	fake := &fakeOCRProcesses{engine: engine, tesseractTSV: func(_ int, _ []string) string {
		return tsvHeader +
			tsvRow(1, 1, 1, 1, 1, 20, 20, 80, 60, "90", "One") +
			tsvRow(1, 2, 1, 1, 1, 20, 260, 80, 60, "90", "Two")
	}}
	engine.run = fake.run
	text, err := engine.Recognize(context.Background(), testImage(100, 80), "en")
	if err != nil || text != "One\n\nTwo" {
		t.Fatalf("Recognize()=%q err=%v", text, err)
	}
}

func TestEngineLargeImageUsesBoundedTilesAndSparsePSM(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	fake := &fakeOCRProcesses{engine: engine, tesseractTSV: func(call int, _ []string) string {
		if call == 0 {
			return tsvHeader + tsvRow(1, 1, 1, 1, 1, 10, 10, 100, 40, "90", "Base")
		}
		return tsvHeader
	}}
	engine.run = fake.run
	page, err := engine.RecognizeStructured(context.Background(), testImage(6000, 3000), "en")
	if err != nil || len(page.Paragraphs) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	tesseractCalls := callsForExecutable(fake.calls, engine.executablePath)
	if len(tesseractCalls) < 2 || len(tesseractCalls) > maxTotalTesseractPasses {
		t.Fatalf("Tesseract calls=%d", len(tesseractCalls))
	}
	if argumentAfter(tesseractCalls[0].args, "--psm") != "3" {
		t.Fatalf("full args=%q", tesseractCalls[0].args)
	}
	for _, call := range tesseractCalls[1:] {
		if argumentAfter(call.args, "--psm") != "11" {
			t.Fatalf("tile args=%q", call.args)
		}
	}
}

func TestCalculateOCRPlanBoundsDimensionsTilesAndWork(t *testing.T) {
	t.Parallel()
	plan := calculateOCRPlan(8000, 3000)
	if len(plan.Tiles) == 0 || len(plan.Tiles) > maxOCRTiles {
		t.Fatalf("tiles=%d", len(plan.Tiles))
	}
	passes := append([]ocrPass{plan.Full}, plan.Tiles...)
	total := 0
	for _, pass := range passes {
		if pass.width > maxOCRWorkingDimension || pass.height > maxOCRWorkingDimension || pass.width*pass.height > maxOCRWorkingPixels {
			t.Fatalf("unbounded pass=%+v", pass)
		}
		total += pass.width * pass.height
	}
	if total > maxOCRTotalWorkingPixels {
		t.Fatalf("total work=%d", total)
	}
	for index := 1; index < len(plan.Tiles); index++ {
		previous, current := plan.Tiles[index-1].crop, plan.Tiles[index].crop
		if previous.Y == current.Y && current.X > previous.X+previous.Width {
			t.Fatalf("tiles do not overlap: previous=%+v current=%+v", previous, current)
		}
	}
}

func TestProjectBoxRoundsAndClampsToOriginal(t *testing.T) {
	t.Parallel()
	pass := ocrPass{crop: OCRBox{X: 100, Y: 50, Width: 200, Height: 100}, width: 600, height: 300}
	got := projectBox(OCRBox{X: 3, Y: 6, Width: 594, Height: 291}, pass, 280, 145)
	want := OCRBox{X: 101, Y: 52, Width: 179, Height: 93}
	if got != want {
		t.Fatalf("projectBox()=%+v want=%+v", got, want)
	}
}

func TestProjectedWordsRejectGeometryCollapsedByRounding(t *testing.T) {
	t.Parallel()
	pass := ocrPass{crop: OCRBox{Width: 10, Height: 10}, width: 100, height: 100}
	words := []OCRWord{
		{Text: ".", Accepted: true, Box: OCRBox{X: 1, Y: 1, Width: 1, Height: 1}},
		{Text: "!", Accepted: true, Box: OCRBox{X: 10, Y: 10, Width: 10, Height: 10}},
	}
	projected := projectWords(words, pass, 10, 10)
	if projected[0].Accepted || !projected[1].Accepted {
		t.Fatalf("projected=%+v", projected)
	}
	accepted := projectAcceptedWords(words, pass, 10, 10)
	if len(accepted) != 1 || accepted[0].Text != "!" {
		t.Fatalf("accepted=%+v", accepted)
	}
	page := rebuildOCRPage(projected, OCRImageInfo{Width: 10, Height: 10})
	if len(page.Paragraphs) != 1 || page.Paragraphs[0].Text != "!" {
		t.Fatalf("page=%+v", page)
	}
}

func TestEngineParsesLiteralQuoteWithoutConsumingFollowingTSVRow(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	fake := &fakeOCRProcesses{engine: engine, tesseractTSV: func(_ int, _ []string) string {
		return tsvHeader +
			tsvRow(1, 1, 1, 1, 1, 20, 20, 80, 60, "90", `"`) +
			strings.TrimSuffix(tsvRow(1, 1, 1, 1, 2, 120, 20, 80, 60, "90", "after"), "\n")
	}}
	engine.run = fake.run
	page, err := engine.RecognizeStructured(context.Background(), testImage(100, 60), "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 2 || page.Words[0].Text != `"` || page.Words[1].Text != "after" {
		t.Fatalf("words=%+v", page.Words)
	}
}

func TestMergeOCRWordsDeduplicatesOverlapAndPrefersConfidence(t *testing.T) {
	t.Parallel()
	base := []OCRWord{{Text: "Install", Confidence: 70, Accepted: true, Box: OCRBox{X: 10, Y: 10, Width: 50, Height: 20}}}
	additions := []OCRWord{
		{Text: "install", Confidence: 90, Accepted: true, Box: OCRBox{X: 12, Y: 10, Width: 50, Height: 20}},
		{Text: "Install", Confidence: 80, Accepted: true, Box: OCRBox{X: 200, Y: 10, Width: 50, Height: 20}},
	}
	merged := mergeOCRWords(base, additions)
	if len(merged) != 2 || merged[0].Confidence != 90 || merged[0].Box.X != 12 {
		t.Fatalf("merged=%+v", merged)
	}
	conflict := mergeOCRWords(base, []OCRWord{{Text: "lnstall", Confidence: 95, Accepted: true, Box: base[0].Box}})
	if len(conflict) != 1 || conflict[0].Text != "lnstall" {
		t.Fatalf("conflict=%+v", conflict)
	}
}

func TestRebuildOCRPageReadingOrderAndIDsAreDeterministic(t *testing.T) {
	t.Parallel()
	words := []OCRWord{
		{Text: "world", Confidence: 80, Accepted: true, Box: OCRBox{X: 70, Y: 10, Width: 40, Height: 15}},
		{Text: "Next", Confidence: 90, Accepted: true, Box: OCRBox{X: 10, Y: 80, Width: 40, Height: 15}},
		{Text: "Hello", Confidence: 90, Accepted: true, Box: OCRBox{X: 10, Y: 10, Width: 50, Height: 15}},
	}
	one := rebuildOCRPage(words, OCRImageInfo{Width: 200, Height: 100, MediaType: "image/png"})
	two := rebuildOCRPage(words, one.Image)
	if plainText(one) != "Hello world\n\nNext" || one.Words[0].ID != "p1-b1-par1-l1-w1" || one.Paragraphs[1].ID != "p1-b2-par1" {
		t.Fatalf("page=%+v text=%q", one, plainText(one))
	}
	if one.Words[0].ID != two.Words[0].ID || one.Paragraphs[1].ID != two.Paragraphs[1].ID {
		t.Fatalf("IDs are not deterministic: one=%+v two=%+v", one, two)
	}
}

func TestEngineEmptyOCRIsEmptyPage(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	fake := &fakeOCRProcesses{engine: engine}
	engine.run = fake.run
	page, err := engine.RecognizeStructured(context.Background(), testImage(100, 60), "en")
	if err != nil || len(page.Words) != 0 || len(page.Paragraphs) != 0 || page.Image.Width != 100 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestEngineProcessErrorsOutputLimitTimeoutCancellationAndCleanup(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Engine, *fakeOCRProcesses) context.Context
		want      string
		canceled  bool
	}{
		{name: "FFmpeg", configure: func(_ *Engine, fake *fakeOCRProcesses) context.Context {
			fake.ffmpegErr = errors.New("exit 1")
			return context.Background()
		}, want: "preprocess OCR image with FFmpeg"},
		{name: "missing FFmpeg output", configure: func(_ *Engine, fake *fakeOCRProcesses) context.Context {
			fake.skipOutput = true
			return context.Background()
		}, want: "non-empty PNG"},
		{name: "Tesseract", configure: func(_ *Engine, fake *fakeOCRProcesses) context.Context {
			fake.tesseractErr = errors.New("exit 1")
			return context.Background()
		}, want: "recognizer failed"},
		{name: "output limit", configure: func(_ *Engine, fake *fakeOCRProcesses) context.Context {
			fake.oversized = true
			return context.Background()
		}, want: "output is too large"},
		{name: "timeout", configure: func(engine *Engine, fake *fakeOCRProcesses) context.Context {
			engine.timeout = 5 * time.Millisecond
			fake.block = true
			return context.Background()
		}, want: "timed out"},
		{name: "cancellation", configure: func(_ *Engine, fake *fakeOCRProcesses) context.Context {
			fake.block = true
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, canceled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := newTestEngine(t, "eng")
			fake := &fakeOCRProcesses{engine: engine}
			ctx := test.configure(engine, fake)
			engine.run = fake.run
			_, err := engine.RecognizeStructured(ctx, testImage(100, 60), "en")
			if test.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
			if fake.temporaryDir != "" && strings.Contains(err.Error(), fake.temporaryDir) {
				t.Fatalf("temporary path leaked in error: %v", err)
			}
			if fake.temporaryDir != "" {
				if _, statErr := os.Stat(fake.temporaryDir); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("temporary directory still exists: %q, %v", fake.temporaryDir, statErr)
				}
			}
		})
	}
}

func TestEngineLanguageMappingTessdataAndFFmpegChecks(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	engine := New(base, time.Second)
	if capability := engine.Capability(); capability.Supported || !strings.Contains(capability.Reason, "Tesseract OCR") {
		t.Fatalf("capability=%+v", capability)
	}
	if err := os.MkdirAll(filepath.Dir(engine.executablePath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, engine.executablePath)
	if capability := engine.Capability(); capability.Supported || !strings.Contains(capability.Reason, "FFmpeg") {
		t.Fatalf("capability=%+v", capability)
	}
	writeFile(t, engine.ffmpegPath)
	if capability := engine.Capability(); capability.Supported || !strings.Contains(capability.Reason, "language data") {
		t.Fatalf("capability=%+v", capability)
	}
	if err := os.MkdirAll(engine.tessdataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(engine.tessdataDir, "eng.traineddata"))
	if _, err := engine.languagesForSource("ru-RU"); err == nil || !strings.Contains(err.Error(), "rus.traineddata") {
		t.Fatalf("language error=%v", err)
	}
}

func TestEngineAutoUsesInstalledLanguagesAndDirectBinLayout(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "rus", "eng", "osd")
	fake := &fakeOCRProcesses{engine: engine}
	engine.run = fake.run
	if _, err := engine.RecognizeStructured(context.Background(), testImage(100, 60), "auto"); err != nil {
		t.Fatal(err)
	}
	tesseractCalls := callsForExecutable(fake.calls, engine.executablePath)
	if got := argumentAfter(tesseractCalls[0].args, "-l"); got != "eng+rus" {
		t.Fatalf("languages=%q", got)
	}

	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(filepath.Join(bin, "tessdata"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bin, "tesseract.exe"))
	ffmpegPath := filepath.Join(bin, "ffmpeg", "ffmpeg.exe")
	writeFile(t, ffmpegPath)
	writeFile(t, filepath.Join(bin, "tessdata", "eng.traineddata"))
	direct := New(base, time.Second)
	if direct.executablePath != filepath.Join(bin, "tesseract.exe") || direct.ffmpegPath != ffmpegPath || !direct.Capability().Supported {
		t.Fatalf("engine=%+v capability=%+v", direct, direct.Capability())
	}
}

func newTestEngine(t *testing.T, languages ...string) *Engine {
	t.Helper()
	base := t.TempDir()
	engine := New(base, time.Second)
	if err := os.MkdirAll(engine.tessdataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, engine.executablePath)
	writeFile(t, engine.ffmpegPath)
	for _, language := range languages {
		writeFile(t, filepath.Join(engine.tessdataDir, language+".traineddata"))
	}
	return engine
}

func testImage(width, height int) translation.ValidatedImage {
	return translation.ValidatedImage{Data: []byte("image-bytes"), MediaType: "image/png", Width: width, Height: height}
}

func tsvRow(page, block, paragraph, line, word, left, top, width, height int, confidence, text string) string {
	values := []string{"5", itoa(page), itoa(block), itoa(paragraph), itoa(line), itoa(word), itoa(left), itoa(top), itoa(width), itoa(height), confidence, text}
	return strings.Join(values, "\t") + "\n"
}

func scaleDimensions(filter string) (int, int) {
	marker := "scale="
	start := strings.Index(filter, marker)
	if start < 0 {
		return 0, 0
	}
	var width, height int
	_, _ = fmt.Sscanf(filter[start+len(marker):], "%d:%d", &width, &height)
	return width, height
}

func writePNGHeader(path string, width, height int) error {
	data := make([]byte, 33)
	copy(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R'})
	binary.BigEndian.PutUint32(data[16:20], uint32(width))
	binary.BigEndian.PutUint32(data[20:24], uint32(height))
	data[24], data[25] = 8, 6
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return os.WriteFile(path, data, 0o600)
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func argumentIndex(args []string, value string) int {
	for index, argument := range args {
		if argument == value {
			return index
		}
	}
	return -1
}

func argumentAfter(args []string, value string) string {
	index := argumentIndex(args, value)
	if index < 0 || index+1 >= len(args) {
		return ""
	}
	return args[index+1]
}

func countExecutable(calls []recordedProcessCall, executable string) int {
	return len(callsForExecutable(calls, executable))
}

func callsForExecutable(calls []recordedProcessCall, executable string) []recordedProcessCall {
	result := make([]recordedProcessCall, 0)
	for _, call := range calls {
		if call.executable == executable {
			result = append(result, call)
		}
	}
	return result
}
