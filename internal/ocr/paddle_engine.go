package ocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sympllate/translator/internal/logger"
	sharedort "github.com/sympllate/translator/internal/onnxruntime"
	"github.com/sympllate/translator/internal/translation"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	paddleInputName  = "x"
	paddleOutputName = "fetch_name_0"
)

var paddleRecognizerByLanguage = map[string]string{
	"ru": "eslav", "uk": "eslav", "en": "latin", "de": "latin", "fr": "latin", "es": "latin", "pl": "latin", "it": "latin", "pt": "latin", "tr": "latin", "zh": "cjk", "ja": "cjk", "ko": "korean", "ar": "arabic",
}
var paddleRecognizerNames = []string{"latin", "eslav", "cjk", "korean", "arabic"}

type paddleSession struct {
	session *ort.DynamicAdvancedSession
	config  recognizerConfig
	gate    sync.Mutex
}

type PaddleEngine struct {
	root, runtimePath string
	timeout           time.Duration
	log               logger.PrintLogger
	environment       *sharedort.Lease
	detector          *ort.DynamicAdvancedSession
	detectorConfig    detectorConfig
	documentProfile   paddleDocumentProfile
	detectorGate      sync.Mutex
	mu                sync.Mutex
	recognizers       map[string]*paddleSession
	closed            bool
	detectOverride    func(context.Context, image.Image, image.Point, image.Point) ([]paddleRegion, detectorTransform, error)
	recognizeOverride func(context.Context, image.Image, recognizerPlan) (string, float64, string, error)
}

func NewPaddleEngine(executableDir string, timeout time.Duration, log logger.PrintLogger) (*PaddleEngine, error) {
	if executableDir == "" {
		return nil, errors.New("PaddleOCR executable directory is empty")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	root := filepath.Join(executableDir, "bin", "OCR")
	engine := &PaddleEngine{root: root, runtimePath: sharedort.DLLPath(executableDir), timeout: timeout, log: log, recognizers: make(map[string]*paddleSession), documentProfile: defaultPaddleDocumentProfile()}
	if capability := engine.Capability(); !capability.Supported {
		return nil, errors.New(capability.Reason)
	}
	lease, err := sharedort.Acquire(executableDir)
	if err != nil {
		return nil, err
	}
	engine.environment = lease
	fail := func(err error) (*PaddleEngine, error) { return nil, errors.Join(err, engine.Close()) }
	config, err := loadDetectorConfig(filepath.Join(root, "det.yml"))
	if err != nil {
		return fail(err)
	}
	engine.detectorConfig = config
	if _, err := validateModelInfo(filepath.Join(root, "det.onnx"), 1); err != nil {
		return fail(fmt.Errorf("PaddleOCR detector model: %w", err))
	}
	detector, err := newPaddleSession(filepath.Join(root, "det.onnx"))
	if err != nil {
		return fail(fmt.Errorf("PaddleOCR detector model load failed: %w", err))
	}
	engine.detector = detector
	if log != nil {
		log.Printf("PaddleOCR detector loaded")
	}
	return engine, nil
}

func (e *PaddleEngine) Capability() translation.ImageCapability {
	paths := []struct{ path, label string }{{e.runtimePath, "ONNX Runtime missing"}, {filepath.Join(e.root, "det.onnx"), "PaddleOCR detector model missing"}, {filepath.Join(e.root, "det.yml"), "PaddleOCR detector config missing"}}
	for _, name := range paddleRecognizerNames {
		paths = append(paths, struct{ path, label string }{filepath.Join(e.root, name+"_rec.onnx"), "PaddleOCR recognizer model missing"}, struct{ path, label string }{filepath.Join(e.root, name+"_rec.yml"), "PaddleOCR recognizer config missing"})
	}
	for _, item := range paths {
		if err := requireRegularFile(item.path); err != nil {
			return translation.ImageCapability{Supported: false, Reason: fmt.Sprintf("%s: %s", item.label, filepath.Base(item.path))}
		}
	}
	return translation.ImageCapability{Supported: true}
}

func (e *PaddleEngine) ValidateSource(source string) error {
	if source == "auto" {
		return nil
	}
	name, ok := paddleRecognizerByLanguage[normalizedSource(source)]
	if !ok {
		return fmt.Errorf("unsupported OCR source language %q", source)
	}
	for _, suffix := range []string{".onnx", ".yml"} {
		path := filepath.Join(e.root, name+"_rec"+suffix)
		if err := requireRegularFile(path); err != nil {
			return fmt.Errorf("PaddleOCR recognizer %s missing for source %q: %s", map[bool]string{true: "config", false: "model"}[suffix == ".yml"], source, filepath.Base(path))
		}
	}
	return nil
}

func normalizedSource(source string) string {
	source = strings.ToLower(source)
	if i := strings.IndexAny(source, "-_"); i >= 0 {
		source = source[:i]
	}
	return source
}

func (e *PaddleEngine) Recognize(ctx context.Context, image translation.ValidatedImage, source string) (string, error) {
	page, _, err := e.recognizePage(ctx, image, source, paddleRecognizeOptions{Profile: e.documentProfile})
	if err != nil {
		return "", err
	}
	return plainText(page), nil
}

func (e *PaddleEngine) RecognizeStructured(ctx context.Context, validated translation.ValidatedImage, source string) (OCRPage, error) {
	page, _, err := e.recognizePage(ctx, validated, source, paddleRecognizeOptions{Profile: e.documentProfile})
	return page, err
}

type paddleRecognizeOptions struct{ Profile paddleDocumentProfile }

type ocrInvocationModeKey struct{}

// WithInvocationMode adds an observational label only. It never changes OCR
// preprocessing, models, thresholds, grouping, or output.
func WithInvocationMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, ocrInvocationModeKey{}, mode)
}

func invocationMode(ctx context.Context) string {
	if mode, ok := ctx.Value(ocrInvocationModeKey{}).(string); ok && mode != "" {
		return mode
	}
	return "single"
}

type recognizerPlan struct {
	Requested, Resolved string
	Names               []string
}

func resolvePaddleRecognizerPlan(source string) (recognizerPlan, error) {
	if source == "auto" {
		return recognizerPlan{Requested: source, Resolved: "auto:per-region-script-selection", Names: append([]string(nil), paddleRecognizerNames...)}, nil
	}
	name, ok := paddleRecognizerByLanguage[normalizedSource(source)]
	if !ok {
		return recognizerPlan{}, fmt.Errorf("unsupported OCR source language %q", source)
	}
	return recognizerPlan{Requested: source, Resolved: normalizedSource(source), Names: []string{name}}, nil
}

func (e *PaddleEngine) recognizePage(ctx context.Context, validated translation.ValidatedImage, source string, options paddleRecognizeOptions) (OCRPage, OCRDiagnostics, error) {
	if validated.Width <= 0 || validated.Height <= 0 {
		return OCRPage{}, OCRDiagnostics{}, errors.New("OCR image dimensions must be positive")
	}
	if err := e.ValidateSource(source); err != nil {
		return OCRPage{}, OCRDiagnostics{}, err
	}
	plan, err := resolvePaddleRecognizerPlan(source)
	if err != nil {
		return OCRPage{}, OCRDiagnostics{}, err
	}
	profile := options.Profile
	if profile.Name == "" {
		profile = defaultPaddleDocumentProfile()
	}
	ocrCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	pipelineStarted := time.Now()
	decoded, _, err := image.Decode(bytes.NewReader(validated.Data))
	if err != nil {
		return OCRPage{}, OCRDiagnostics{}, fmt.Errorf("decode PaddleOCR image: %w", err)
	}
	diagnostics := OCRDiagnostics{
		Profile: profile.Name, RequestedSource: plan.Requested, ResolvedOCRLanguage: plan.Resolved,
		DetectorModel: "det.onnx", RecognizerModels: make([]string, 0, len(plan.Names)),
		Dictionaries: make([]string, 0, len(plan.Names)), InputWidth: validated.Width, InputHeight: validated.Height,
	}
	for _, name := range plan.Names {
		diagnostics.RecognizerModels = append(diagnostics.RecognizerModels, name+"_rec.onnx")
		diagnostics.Dictionaries = append(diagnostics.Dictionaries, name+"_rec.yml")
	}
	if len(plan.Names) == 1 {
		diagnostics.RecognizerModel, diagnostics.Dictionary = diagnostics.RecognizerModels[0], diagnostics.Dictionaries[0]
	}
	detectStarted := time.Now()
	regions, transform, err := e.detect(ocrCtx, decoded, image.Point{}, image.Pt(validated.Width, validated.Height))
	if err != nil {
		return OCRPage{}, diagnostics, e.contextError(ctx, ocrCtx, err)
	}
	diagnostics.Preprocess = append(diagnostics.Preprocess, transformDiagnostics(transform))
	for index := range regions {
		regions[index].Pass = "full"
	}
	fullCandidates := len(regions)
	tiles := paddleTileCrops(validated.Width, validated.Height, profile, transform)
	for tileIndex, crop := range tiles {
		if err := ocrCtx.Err(); err != nil {
			return OCRPage{}, diagnostics, e.contextError(ctx, ocrCtx, err)
		}
		tileImage := cropImage(decoded, crop)
		tileRegions, tileTransform, detectErr := e.detect(ocrCtx, tileImage, image.Pt(crop.X, crop.Y), image.Pt(validated.Width, validated.Height))
		if detectErr != nil {
			return OCRPage{}, diagnostics, e.contextError(ctx, ocrCtx, detectErr)
		}
		diagnostics.Preprocess = append(diagnostics.Preprocess, transformDiagnostics(tileTransform))
		for index := range tileRegions {
			tileRegions[index].Pass = fmt.Sprintf("tile-%02d", tileIndex+1)
		}
		regions = append(regions, tileRegions...)
	}
	diagnostics.Tiles = len(tiles)
	diagnostics.DetectorCandidates = len(regions)
	recognizeStarted := time.Now()
	for index := range regions {
		if err := ocrCtx.Err(); err != nil {
			return OCRPage{}, diagnostics, e.contextError(ctx, ocrCtx, err)
		}
		crop := rectifyRegion(decoded, regions[index])
		result, confidence, recognizer, recognizeErr := e.recognizeCrop(ocrCtx, crop, plan)
		regions[index].Text, regions[index].RecognizerConfidence, regions[index].Recognizer = strings.TrimSpace(result), confidence, recognizer
		if recognizeErr != nil {
			return OCRPage{}, diagnostics, e.contextError(ctx, ocrCtx, recognizeErr)
		}
	}
	merged, duplicates := mergePaddleRegions(regions)
	diagnostics.MergeDuplicates = duplicates
	sortPaddleRegions(merged)
	words := make([]OCRWord, 0, len(merged))
	minimumConfidence, confidenceTotal := 1.0, 0.0
	for _, region := range merged {
		result, confidence := region.Text, region.RecognizerConfidence
		regionDiagnostic := OCRRegionDiagnostic{Pass: region.Pass, Text: result, Recognizer: region.Recognizer, DetectorConfidence: region.DetectorConfidence * 100, RecognizerConfidence: confidence * 100, Box: region.Box, Polygon: publicPolygon(region.Polygon), Recognized: result != "", TextAccepted: result != "", CleanupSafe: result != "" && confidence >= .5}
		diagnostics.Regions = append(diagnostics.Regions, regionDiagnostic)
		if result == "" {
			continue
		}
		diagnostics.RecognizedCandidates++
		diagnostics.AcceptedTextCandidates++
		cleanupSafe := confidence >= .5
		if cleanupSafe {
			diagnostics.CleanupSafeCandidates++
		}
		confidenceTotal += confidence
		if confidence < minimumConfidence {
			minimumConfidence = confidence
		}
		words = append(words, OCRWord{
			Text: result, Confidence: confidence * 100, Box: region.Box, Polygon: publicPolygon(region.Polygon),
			DetectorConfidence: region.DetectorConfidence * 100, RecognizerConfidence: confidence * 100,
			Detected: true, Recognized: true, TextAccepted: true, CleanupSafe: cleanupSafe,
			Accepted: true, Recognizer: region.Recognizer, GeometryLevel: region.Pass,
		})
	}
	if diagnostics.RecognizedCandidates > 0 {
		diagnostics.AverageConfidence = confidenceTotal * 100 / float64(diagnostics.RecognizedCandidates)
		diagnostics.MinimumConfidence = minimumConfidence * 100
	}
	groupStarted := time.Now()
	page := OCRPage{SchemaVersion: 1, Image: OCRImageInfo{Width: validated.Width, Height: validated.Height, MediaType: validated.MediaType}, Words: words, Paragraphs: buildPaddleParagraphs(words)}
	for paragraphIndex := range page.Paragraphs {
		for _, line := range page.Paragraphs[paragraphIndex].Lines {
			for wordIndex := range line.Words {
				for pageWordIndex := range page.Words {
					if page.Words[pageWordIndex].Box == line.Words[wordIndex].Box && page.Words[pageWordIndex].Text == line.Words[wordIndex].Text {
						page.Words[pageWordIndex] = line.Words[wordIndex]
						break
					}
				}
			}
		}
	}
	page.Diagnostics = diagnostics
	for _, paragraph := range page.Paragraphs {
		diagnostics.FinalLines += len(paragraph.Lines)
	}
	diagnostics.FinalParagraphs = len(page.Paragraphs)
	page.Diagnostics = diagnostics
	if e.log != nil {
		hash := sha256.Sum256(validated.Data)
		e.log.Printf("PaddleOCR invocation: mode=%s image_sha256=%x input=%dx%d source_requested=%s source_resolved=%s detector=%s recognizers=%s profile=%s full_candidates=%d tiles=%d detector_candidates=%d recognized_candidates=%d accepted_text=%d cleanup_safe=%d merge_duplicates=%d final_lines=%d final_paragraphs=%d preprocess=%v decode_ms=%d detect_ms=%d recognize_ms=%d grouping_ms=%d total_ms=%d", invocationMode(ctx), hash, validated.Width, validated.Height, plan.Requested, plan.Resolved, diagnostics.DetectorModel, strings.Join(diagnostics.RecognizerModels, ","), profile.Name, fullCandidates, len(tiles), diagnostics.DetectorCandidates, diagnostics.RecognizedCandidates, diagnostics.AcceptedTextCandidates, diagnostics.CleanupSafeCandidates, diagnostics.MergeDuplicates, diagnostics.FinalLines, diagnostics.FinalParagraphs, diagnostics.Preprocess, detectStarted.Sub(pipelineStarted).Milliseconds(), recognizeStarted.Sub(detectStarted).Milliseconds(), groupStarted.Sub(recognizeStarted).Milliseconds(), time.Since(groupStarted).Milliseconds(), time.Since(pipelineStarted).Milliseconds())
	}
	return page, diagnostics, nil
	/*
		for _, region := range regions {
			result, confidence, err := e.recognizeCrop(ocrCtx, crop, source)
			if err != nil {
				return OCRPage{}, OCRDiagnostics{}, e.contextError(ctx, ocrCtx, err)
			}
			result = strings.TrimSpace(result)
			if result == "" {
				continue
			}
			words = append(words, OCRWord{Text: result, Confidence: confidence * 100, Box: region.Box, Accepted: confidence >= .5})
		}
		page := rebuildOCRPage(words, OCRImageInfo{Width: validated.Width, Height: validated.Height, MediaType: validated.MediaType})
		return page, OCRDiagnostics{}, nil */
}

func (e *PaddleEngine) contextError(parent, pipeline context.Context, err error) error {
	if errors.Is(parent.Err(), context.Canceled) {
		return fmt.Errorf("OCR cancelled: %w", context.Canceled)
	}
	if errors.Is(pipeline.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("OCR timeout after %s: %w", e.timeout, context.DeadlineExceeded)
	}
	return err
}

func (e *PaddleEngine) recognizeCrop(ctx context.Context, crop image.Image, plan recognizerPlan) (string, float64, string, error) {
	if e.recognizeOverride != nil {
		return e.recognizeOverride(ctx, crop, plan)
	}
	if len(plan.Names) == 1 {
		text, confidence, err := e.runRecognizer(ctx, plan.Names[0], crop)
		return text, confidence, plan.Names[0], err
	}
	type candidate struct {
		text, name        string
		confidence, score float64
	}
	candidates := make([]candidate, 0, len(paddleRecognizerNames))
	for _, name := range plan.Names {
		text, confidence, err := e.runRecognizer(ctx, name, crop)
		if err != nil {
			return "", 0, "", err
		}
		score := confidence * scriptCompatibility(text, name)
		if strings.TrimSpace(text) != "" {
			candidates = append(candidates, candidate{text, name, confidence, score})
		}
	}
	if len(candidates) == 0 {
		return "", 0, "", nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})
	return candidates[0].text, candidates[0].confidence, candidates[0].name, nil
}

func transformDiagnostics(transform detectorTransform) OCRPreprocessDiagnostics {
	return OCRPreprocessDiagnostics{Width: transform.InputWidth, Height: transform.InputHeight, ScaleX: transform.ScaleX, ScaleY: transform.ScaleY, PaddingLeft: int(transform.PaddingLeft), PaddingTop: int(transform.PaddingTop)}
}

func cropImage(source image.Image, box OCRBox) image.Image {
	if sub, ok := source.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(image.Rect(box.X, box.Y, box.X+box.Width, box.Y+box.Height))
	}
	return source
}

func paddleTileCrops(width, height int, profile paddleDocumentProfile, full detectorTransform) []OCRBox {
	if width <= 0 || height <= 0 || maximum(width, height) <= full.InputWidth || profile.MaximumTiles <= 0 {
		return nil
	}
	maximumTiles := minimum(profile.MaximumTiles, maximum(0, profile.MaximumDetectorPasses-1))
	desired := ceilingDivision(width, profile.TileSize) * ceilingDivision(height, profile.TileSize)
	desired = minimum(maximum(2, desired), maximumTiles)
	columns, rows := tileGrid(width, height, desired)
	result := make([]OCRBox, 0, columns*rows)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			x0, x1 := column*width/columns, (column+1)*width/columns
			y0, y1 := row*height/rows, (row+1)*height/rows
			if column > 0 {
				x0 = maximum(0, x0-profile.TileOverlap)
			}
			if column+1 < columns {
				x1 = minimum(width, x1+profile.TileOverlap)
			}
			if row > 0 {
				y0 = maximum(0, y0-profile.TileOverlap)
			}
			if row+1 < rows {
				y1 = minimum(height, y1+profile.TileOverlap)
			}
			result = append(result, OCRBox{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0})
		}
	}
	return result
}

func mergePaddleRegions(regions []paddleRegion) ([]paddleRegion, int) {
	result := make([]paddleRegion, 0, len(regions))
	duplicates := 0
	for _, candidate := range regions {
		match := -1
		for index, existing := range result {
			intersection := polygonIntersectionArea(existing.Polygon, candidate.Polygon)
			smaller := math.Min(polygonArea(existing.Polygon), polygonArea(candidate.Polygon))
			if smaller <= 0 {
				continue
			}
			centerA, centerB := polygonCenter(existing.Polygon), polygonCenter(candidate.Polygon)
			centerDistance := math.Hypot(centerA.X-centerB.X, centerA.Y-centerB.Y)
			scale := math.Max(1, math.Min(float64(existing.Box.Height), float64(candidate.Box.Height)))
			sameText := normalizedOCRText(existing.Text) == normalizedOCRText(candidate.Text)
			containedText := normalizedTextContains(existing.Text, candidate.Text)
			if (sameText && intersection/smaller >= .30 && centerDistance <= scale*2) || (containedText && intersection/smaller >= .80) || (intersection/smaller >= .75 && centerDistance <= scale) {
				match = index
				break
			}
		}
		if match < 0 {
			result = append(result, candidate)
			continue
		}
		duplicates++
		existing := result[match]
		candidateText, existingText := []rune(normalizedOCRText(candidate.Text)), []rune(normalizedOCRText(existing.Text))
		if len(candidateText) > len(existingText) && normalizedTextContains(candidate.Text, existing.Text) || (len(candidateText) == len(existingText) && (candidate.RecognizerConfidence > existing.RecognizerConfidence || (candidate.RecognizerConfidence == existing.RecognizerConfidence && candidate.DetectorConfidence > existing.DetectorConfidence))) {
			result[match] = candidate
		}
	}
	return result, duplicates
}

func normalizedTextContains(left, right string) bool {
	left, right = normalizedOCRText(left), normalizedOCRText(right)
	if left == "" || right == "" { return false }
	return strings.Contains(left,right) || strings.Contains(right,left)
}

func scriptCompatibility(text, name string) float64 {
	letters, compatible := 0, 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch name {
		case "latin":
			if unicode.In(r, unicode.Latin) {
				compatible++
			}
		case "eslav":
			if unicode.In(r, unicode.Cyrillic) {
				compatible++
			}
		case "cjk":
			if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
				compatible++
			}
		case "korean":
			if unicode.In(r, unicode.Hangul) {
				compatible++
			}
		case "arabic":
			if unicode.In(r, unicode.Arabic) {
				compatible++
			}
		}
	}
	if letters == 0 {
		return .5
	}
	return .35 + .65*float64(compatible)/float64(letters)
}

func (e *PaddleEngine) recognizer(name string) (*paddleSession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("PaddleOCR engine is closed")
	}
	if existing := e.recognizers[name]; existing != nil {
		return existing, nil
	}
	configPath := filepath.Join(e.root, name+"_rec.yml")
	config, err := loadRecognizerConfig(configPath)
	if err != nil {
		return nil, err
	}
	modelPath := filepath.Join(e.root, name+"_rec.onnx")
	info, err := validateModelInfo(modelPath, len(config.Characters)+1)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR recognizer %s: %w", name, err)
	}
	session, err := newPaddleSession(modelPath)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR recognizer %s model load failed: %w", name, err)
	}
	if info.MaxInputWidth > 0 {
		config.MaxWidth = int(info.MaxInputWidth)
	}
	result := &paddleSession{session: session, config: config}
	e.recognizers[name] = result
	if e.log != nil {
		e.log.Printf("PaddleOCR recognizer loaded: %s", name)
	}
	return result, nil
}

func newPaddleSession(path string) (*ort.DynamicAdvancedSession, error) {
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	configureErr := errors.Join(options.SetExecutionMode(ort.ExecutionModeSequential), options.SetInterOpNumThreads(1), options.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll))
	if configureErr != nil {
		_ = options.Destroy()
		return nil, configureErr
	}
	session, sessionErr := ort.NewDynamicAdvancedSession(path, []string{paddleInputName}, []string{paddleOutputName}, options)
	destroyErr := options.Destroy()
	return session, errors.Join(sessionErr, destroyErr)
}

type paddleModelInfo struct{ MaxInputWidth int64 }

func validateModelInfo(path string, classes int) (paddleModelInfo, error) {
	inputs, outputs, err := ort.GetInputOutputInfo(path)
	if err != nil {
		return paddleModelInfo{}, fmt.Errorf("inspect model metadata: %w", err)
	}
	if len(inputs) != 1 || inputs[0].Name != paddleInputName || len(inputs[0].Dimensions) != 4 {
		return paddleModelInfo{}, fmt.Errorf("model input has unexpected name or shape")
	}
	if len(outputs) != 1 || outputs[0].Name != paddleOutputName {
		return paddleModelInfo{}, fmt.Errorf("model output has unexpected name")
	}
	if classes > 1 && (len(outputs[0].Dimensions) != 3 || outputs[0].Dimensions[2] != int64(classes)) {
		return paddleModelInfo{}, fmt.Errorf("model output has unexpected shape %v; dictionary requires %d classes", outputs[0].Dimensions, classes)
	}
	return paddleModelInfo{MaxInputWidth: inputs[0].Dimensions[3]}, nil
}

func runSession(ctx context.Context, session *ort.DynamicAdvancedSession, input ort.Value) (*ort.Tensor[float32], error) {
	options, err := ort.NewRunOptions()
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		select {
		case <-ctx.Done():
			_ = options.Terminate()
		case <-done:
		}
	}()
	outputs := []ort.Value{nil}
	err = session.RunWithOptions([]ort.Value{input}, outputs, options)
	close(done)
	watcher.Wait()
	destroyErr := options.Destroy()
	if ctx.Err() != nil {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
		return nil, ctx.Err()
	}
	if err != nil {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
		return nil, err
	}
	if destroyErr != nil {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
		return nil, destroyErr
	}
	tensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
		return nil, errors.New("PaddleOCR model output has unexpected tensor type")
	}
	return tensor, nil
}

func (e *PaddleEngine) runRecognizer(ctx context.Context, name string, crop image.Image) (string, float64, error) {
	rec, err := e.recognizer(name)
	if err != nil {
		return "", 0, err
	}
	rec.gate.Lock()
	defer rec.gate.Unlock()
	data, width := recognizerInput(crop, rec.config)
	input, err := ort.NewTensor(ort.NewShape(1, 3, 48, int64(width)), data)
	if err != nil {
		return "", 0, err
	}
	defer input.Destroy()
	output, err := runSession(ctx, rec.session, input)
	if err != nil {
		return "", 0, fmt.Errorf("PaddleOCR recognizer inference failed (%s): %w", name, err)
	}
	defer output.Destroy()
	shape := output.GetShape()
	if len(shape) != 3 || shape[0] != 1 || shape[2] != int64(len(rec.config.Characters)+1) {
		return "", 0, fmt.Errorf("PaddleOCR model output has unexpected shape %v", shape)
	}
	text, confidence := ctcDecode(output.GetData(), int(shape[1]), int(shape[2]), rec.config.Characters)
	return text, confidence, nil
}

func recognizerInput(src image.Image, config recognizerConfig) ([]float32, int) {
	b := src.Bounds()
	targetW := maximum(1, int(math.Ceil(float64(config.Height)*float64(b.Dx())/float64(maximum(1, b.Dy())))))
	maximumWidth := config.MaxWidth
	if maximumWidth <= 0 {
		maximumWidth = config.Width
	}
	inputWidth := clamp(maximum(config.Width, targetW), config.Width, maximumWidth)
	targetW = minimum(targetW, inputWidth)
	data := make([]float32, 3*config.Height*inputWidth)
	for y := 0; y < config.Height; y++ {
		sy := float64(b.Min.Y) + (float64(y)+.5)*float64(b.Dy())/float64(config.Height) - .5
		for x := 0; x < targetW; x++ {
			sx := float64(b.Min.X) + (float64(x)+.5)*float64(b.Dx())/float64(targetW) - .5
			c := bilinearColor(src, sx, sy)
			offset := y*inputWidth + x
			data[offset] = (float32(c.B)/255 - .5) / .5
			data[config.Height*inputWidth+offset] = (float32(c.G)/255 - .5) / .5
			data[2*config.Height*inputWidth+offset] = (float32(c.R)/255 - .5) / .5
		}
	}
	return data, inputWidth
}

func ctcDecode(values []float32, steps, classes int, dictionary []string) (string, float64) {
	var builder strings.Builder
	previous := -1
	confidence, count := float64(0), 0
	for step := 0; step < steps; step++ {
		base := step * classes
		best := 0
		for class := 1; class < classes; class++ {
			if values[base+class] > values[base+best] {
				best = class
			}
		}
		if best != 0 && best != previous && best-1 < len(dictionary) {
			builder.WriteString(dictionary[best-1])
			confidence += float64(values[base+best])
			count++
		}
		previous = best
	}
	if count == 0 {
		return "", 0
	}
	return builder.String(), confidence / float64(count)
}

func (e *PaddleEngine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	recognizers := e.recognizers
	e.recognizers = nil
	e.mu.Unlock()
	var errs []error
	e.detectorGate.Lock()
	if e.detector != nil {
		errs = append(errs, e.detector.Destroy())
		e.detector = nil
	}
	e.detectorGate.Unlock()
	for _, rec := range recognizers {
		rec.gate.Lock()
		errs = append(errs, rec.session.Destroy())
		rec.gate.Unlock()
	}
	if e.environment != nil {
		errs = append(errs, e.environment.Close())
		e.environment = nil
	}
	return errors.Join(errs...)
}

var _ StructuredEngine = (*PaddleEngine)(nil)
