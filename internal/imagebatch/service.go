package imagebatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sympllate/translator/internal/inpaint"
	"github.com/sympllate/translator/internal/logger"
	"github.com/sympllate/translator/internal/ocr"
	"github.com/sympllate/translator/internal/translation"
)

const retainedJobs = 5

type StructuredOCR interface {
	Capability() translation.ImageCapability
	RecognizeStructured(ctx context.Context, image translation.ValidatedImage, source string) (ocr.OCRPage, error)
}

type Service struct {
	ctx           context.Context
	executableDir string
	ocr           StructuredOCR
	translator    *translation.StructuredTranslator
	selections    *SelectionStore
	logger        logger.PrintLogger
	now           func() time.Time
	openDirectory func(string) error
	renderer      *Renderer
	ffmpeg        *ffmpegAdapter

	mu       sync.Mutex
	closed   bool
	busy     bool
	activeID string
	jobs     map[string]*batchJob
	jobOrder []string
	wg       sync.WaitGroup
}

type batchJob struct {
	status    ImageBatchStatus
	request   StartImageBatchRequest
	selection storedBatchSelection
	files     []string
	names     []string
	layout    outputLayout
	cancel    context.CancelFunc
	report    JobReport
	errors    []BatchFileError
}

func NewService(ctx context.Context, executableDir string, recognizer StructuredOCR, completer translation.RawCompleter, maxInputCharacters int, renderConfig RenderConfig, inpainter inpaint.Engine, log logger.PrintLogger) (*Service, error) {
	structuredTranslator, err := translation.NewStructuredTranslator(completer, maxInputCharacters)
	if err != nil {
		return nil, err
	}
	if recognizer == nil || executableDir == "" {
		return nil, errors.New("invalid image batch service configuration")
	}
	renderer, err := NewRenderer(executableDir, renderConfig, inpainter)
	if err != nil {
		return nil, err
	}
	return &Service{
		ctx: ctx, executableDir: executableDir, ocr: recognizer, translator: structuredTranslator,
		selections: NewSelectionStore(DefaultSelectionTTL), logger: log, now: time.Now,
		openDirectory: openExplorer, jobs: make(map[string]*batchJob),
		renderer: renderer, ffmpeg: newFFmpegAdapter(executableDir),
	}, nil
}

func (s *Service) SelectFiles(paths []string) (BatchSelection, error) {
	return s.selections.CreateFiles(paths)
}
func (s *Service) SelectDirectory(path string) (BatchSelection, error) {
	return s.selections.CreateDirectory(path)
}

func (s *Service) Start(request StartImageBatchRequest) (string, error) {
	if strings.TrimSpace(request.SelectionID) == "" {
		return "", errors.New("select images before starting batch translation")
	}
	if err := translation.ValidateLanguagePair(request.Source, request.Target); err != nil {
		return "", err
	}
	capability := s.ocr.Capability()
	if !capability.Supported {
		return "", errors.New(capability.Reason)
	}
	if sourceValidator, ok := s.ocr.(interface{ ValidateSource(string) error }); ok {
		if err := sourceValidator.ValidateSource(request.Source); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", errors.New("image batch service is shutting down")
	}
	if s.busy {
		s.mu.Unlock()
		return "", errors.New("another image batch is already running")
	}
	s.busy = true
	s.mu.Unlock()
	releaseBusy := true
	defer func() {
		if releaseBusy {
			s.mu.Lock()
			s.busy = false
			s.mu.Unlock()
		}
	}()
	selection, err := s.selections.Take(request.SelectionID)
	if err != nil {
		return "", err
	}
	if len(selection.Files) == 0 {
		return "", errors.New("the batch selection is empty")
	}
	layout, err := createOutputLayout(s.executableDir, s.now(), request.Debug)
	if err != nil {
		return "", err
	}
	id, err := randomID("batch-")
	if err != nil {
		return "", err
	}
	jobContext, cancel := context.WithCancel(s.ctx)
	started := s.now()
	job := &batchJob{
		status:  ImageBatchStatus{ID: id, State: "pending", Total: len(selection.Files), OutputDirectory: layout.Root},
		request: request, selection: selection, files: append([]string(nil), selection.Files...), names: uniqueOutputNames(selection.Files),
		layout: layout, cancel: cancel,
		errors: []BatchFileError{},
		report: JobReport{
			SchemaVersion: SchemaVersion, ID: id, State: "pending", StartedAt: started, Source: request.Source, Target: request.Target,
			Selection: JobSelection{Kind: selection.Kind, DisplayName: selection.DisplayName, FileCount: len(selection.Files)},
			Summary:   JobSummary{Total: len(selection.Files)}, Files: []JobFileReport{},
		},
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return "", errors.New("image batch service is shutting down")
	}
	s.evictJobsLocked()
	s.jobs[id] = job
	s.jobOrder = append(s.jobOrder, id)
	s.activeID = id
	s.wg.Add(1)
	s.mu.Unlock()
	releaseBusy = false
	s.logf("image batch started: id=%s kind=%s files=%d source=%s target=%s output=%s debug=%t", id, selection.Kind, len(selection.Files), request.Source, request.Target, filepath.Base(layout.Root), request.Debug)
	go func() { defer s.wg.Done(); s.run(jobContext, job) }()
	return id, nil
}

func (s *Service) Status(id string) (ImageBatchStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return ImageBatchStatus{}, fmt.Errorf("image batch %q not found", id)
	}
	return job.status, nil
}

func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("image batch %q not found", id)
	}
	if terminalState(job.status.State) {
		return nil
	}
	job.cancel()
	return nil
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.activeID != "" {
		if job := s.jobs[s.activeID]; job != nil {
			job.cancel()
		}
	}
	s.mu.Unlock()
}

func (s *Service) Wait() {
	s.wg.Wait()
	if err := s.renderer.Close(); err != nil {
		s.logf("image inpaint shutdown failed: error=%v", err)
	}
}

func (s *Service) run(ctx context.Context, job *batchJob) {
	s.setState(job, "preparing", "")
	job.report.State = "preparing"
	if err := s.writeReports(job); err != nil {
		s.finish(job, "failed", err)
		return
	}
	s.setState(job, "processing", "")
	job.report.State = "processing"
	for index, path := range job.files {
		if err := ctx.Err(); err != nil {
			s.finish(job, "cancelled", nil)
			return
		}
		name := filepath.Base(path)
		s.updateCurrent(job, name)
		s.logf("image batch file started: id=%s index=%d total=%d name=%s", job.status.ID, index+1, len(job.files), name)
		fileReport, systemErr, cancelled := s.processFile(ctx, job, index, path, job.names[index])
		job.report.Files = append(job.report.Files, fileReport)
		if cancelled {
			s.finish(job, "cancelled", nil)
			return
		}
		s.syncSummary(job)
		if err := s.writeReports(job); err != nil {
			s.finish(job, "failed", err)
			return
		}
		if systemErr != nil {
			s.finish(job, "failed", systemErr)
			return
		}
	}
	state := "completed"
	if len(job.errors) > 0 {
		state = "completed_with_errors"
	}
	s.finish(job, state, nil)
}

func (s *Service) processFile(ctx context.Context, job *batchJob, index int, sourcePath, outputName string) (JobFileReport, error, bool) {
	started := s.now()
	report := JobFileReport{SourceID: fmt.Sprintf("selection-file-%06d", index+1), SourceFile: filepath.Base(sourcePath), OutputName: outputName, Status: "processing", DurationsMillis: make(map[string]int64)}
	finish := func(status, stage string) JobFileReport {
		report.Status = status
		report.ErrorStage = stage
		report.DurationMillis = s.now().Sub(started).Milliseconds()
		return report
	}
	s.updateStage(job, "prepare_render")
	prepareStarted := s.now()
	prepared, err := prepareSource(ctx, sourcePath, s.ffmpeg)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return finish("cancelled", "prepare_render"), nil, true
		}
		s.fileFailed(job, report.SourceFile, "validate", err)
		return finish("failed", "validate"), nil, false
	}
	report.DurationsMillis["prepare"] = s.now().Sub(prepareStarted).Milliseconds()
	imagePath := filepath.Join(job.layout.Images, outputName)
	if err := atomicWriteBytesContext(ctx, imagePath, prepared.Original); err != nil {
		s.fileFailed(job, report.SourceFile, "copy", err)
		return finish("failed", "copy"), nil, false
	}
	stem := strings.TrimSuffix(outputName, filepath.Ext(outputName))
	ocrPath := filepath.Join(job.layout.OCR, stem+".ocr.json")
	translationPath := filepath.Join(job.layout.Translations, stem+".translation.json")
	report.OCRPath = relativeOutputPath(job.layout.Root, ocrPath)
	report.TranslationPath = relativeOutputPath(job.layout.Root, translationPath)
	finalPath := filepath.Join(job.layout.Translated, outputName)
	report.OutputFile = relativeOutputPath(job.layout.Root, finalPath)
	s.updateStage(job, "ocr")
	ocrStarted := s.now()
	page, err := s.ocr.RecognizeStructured(ctx, prepared.Validated, job.request.Source)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return finish("cancelled", "ocr"), nil, true
		}
		stage := "ocr"
		if strings.Contains(err.Error(), "parse Tesseract TSV") {
			stage = "parse_ocr"
		}
		s.fileFailed(job, report.SourceFile, stage, err)
		_ = s.writeFailedTranslation(translationPath, outputName, job.request, "failed", safeMessage(err))
		return finish("failed", stage), nil, false
	}
	page.SourceFile = outputName
	if err := atomicWriteJSON(ocrPath, page); err != nil {
		s.fileFailed(job, report.SourceFile, "write_output", err)
		return finish("failed", "write_output"), nil, false
	}
	s.logf("image batch OCR completed: id=%s name=%s words=%d paragraphs=%d duration=%s", job.status.ID, report.SourceFile, len(page.Words), len(page.Paragraphs), s.now().Sub(ocrStarted))
	if len(page.Paragraphs) == 0 {
		document := TranslationDocument{SchemaVersion: SchemaVersion, SourceFile: outputName, Source: job.request.Source, Target: job.request.Target, Status: "no_text", Blocks: []TranslatedBlock{}}
		if err := atomicWriteJSON(translationPath, document); err != nil {
			s.fileFailed(job, report.SourceFile, "write_output", err)
			return finish("failed", "write_output"), nil, false
		}
		if err := ctx.Err(); err != nil {
			return finish("cancelled", "encode_output"), nil, true
		}
		s.updateStage(job, "encode_output")
		if err := atomicWriteBytesContext(ctx, finalPath, prepared.Original); err != nil {
			s.fileFailed(job, report.SourceFile, "encode_output", err)
			return finish("failed", "encode_output"), nil, false
		}
		s.increment(job, "no_text")
		s.renderDebug(ctx, job, &report, prepared.Validated.Data, page)
		return finish("no_text", ""), nil, false
	}
	blocks := make([]translation.TranslationBlock, 0, len(page.Paragraphs))
	for _, paragraph := range page.Paragraphs {
		lines := make([]string, 0, len(paragraph.Lines))
		for _, line := range paragraph.Lines {
			lines = append(lines, line.Text)
		}
		blocks = append(blocks, translation.TranslationBlock{ID: paragraph.ID, Text: paragraph.Text, Lines: lines})
	}
	translationStarted := s.now()
	s.updateStage(job, "translate")
	translated, chunks, err := s.translator.Translate(ctx, job.request.Source, job.request.Target, blocks)
	failedTranslationIDs := make(map[string]struct{})
	var protocolErr *translation.ProtocolError
	if errors.As(err, &protocolErr) {
		translated, chunks, failedTranslationIDs, err = s.translateBlocksIndividually(ctx, job.request.Source, job.request.Target, blocks)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_ = s.writeFailedTranslation(translationPath, outputName, job.request, "cancelled", "")
			return finish("cancelled", "translate"), nil, true
		}
		stage := "validate_translation"
		var completionErr *translation.CompletionError
		if errors.As(err, &completionErr) {
			stage = "translate"
		}
		if completionErr != nil {
			s.addFileError(job, report.SourceFile, stage, err, false)
			s.increment(job, "failed")
		} else {
			s.fileFailed(job, report.SourceFile, stage, err)
		}
		_ = s.writeFailedTranslation(translationPath, outputName, job.request, "failed", safeMessage(err))
		fileReport := finish("failed", stage)
		if completionErr != nil {
			return fileReport, fmt.Errorf("translation service is unavailable: %w", completionErr), false
		}
		return fileReport, nil, false
	}
	translatedByID := make(map[string]translation.TranslatedTextBlock, len(translated))
	for _, block := range translated {
		translatedByID[block.ID] = block
	}
	documentStatus := "translated"
	if len(failedTranslationIDs) > 0 {
		documentStatus = "partial"
	}
	document := TranslationDocument{SchemaVersion: SchemaVersion, SourceFile: outputName, Source: job.request.Source, Target: job.request.Target, Status: documentStatus, Blocks: make([]TranslatedBlock, 0, len(page.Paragraphs))}
	for _, paragraph := range page.Paragraphs {
		translatedBlock, translatedOK := translatedByID[paragraph.ID]
		blockStatus := "translated"
		if !translatedOK || strings.TrimSpace(translatedBlock.Text) == "" {
			blockStatus = "failed"
		}
		outputBlock := TranslatedBlock{ID: paragraph.ID, SourceText: paragraph.Text, TranslatedText: translatedBlock.Text, Confidence: paragraph.Confidence, Box: paragraph.Box, Status: blockStatus}
		for _, part := range translatedBlock.Parts {
			outputBlock.Parts = append(outputBlock.Parts, TranslatedPart{ID: part.ID, SourceText: part.SourceText, TranslatedText: part.TranslatedText})
		}
		document.Blocks = append(document.Blocks, outputBlock)
	}
	if err := atomicWriteJSON(translationPath, document); err != nil {
		s.fileFailed(job, report.SourceFile, "write_output", err)
		return finish("failed", "write_output"), nil, false
	}
	s.logf("image batch translation completed: id=%s name=%s blocks=%d chunks=%d duration=%s", job.status.ID, report.SourceFile, len(document.Blocks), chunks, s.now().Sub(translationStarted))
	report.TotalBlocks = len(page.Paragraphs)
	s.updateStage(job, "layout_text")
	layoutStarted := s.now()
	renderDocument, err := s.renderer.Prepare(ctx, prepared.Image, page, document)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return finish("cancelled", "layout_text"), nil, true
		}
		stage := "layout_text"
		if strings.Contains(err.Error(), "шрифт") {
			stage = "load_font"
		}
		s.fileFailed(job, report.SourceFile, stage, err)
		return finish("failed", stage), systemRenderError(stage, err), false
	}
	report.DurationsMillis["layout"] = s.now().Sub(layoutStarted).Milliseconds()
	s.logf("image layout completed: job=%s name=%s renderable=%d skipped=%d warnings=%d duration=%s", job.status.ID, report.SourceFile, len(renderDocument.Blocks), len(renderDocument.SkippedBlocks), len(renderDocument.Warnings), s.now().Sub(layoutStarted))

	if len(renderDocument.Blocks) == 0 {
		report.RenderedBlocks = 0
		report.SkippedBlocks = renderDocument.SkippedBlocks
		report.Warnings = renderDocument.Warnings
		s.updateStage(job, "encode_output")
		if err := atomicWriteBytesContext(ctx, finalPath, prepared.Original); err != nil {
			if errors.Is(err, context.Canceled) {
				return finish("cancelled", "encode_output"), nil, true
			}
			s.fileFailed(job, report.SourceFile, "encode_output", err)
			return finish("failed", "encode_output"), nil, false
		}
		s.incrementRendered(job, "partial", len(renderDocument.Warnings))
		s.renderDebugArtifacts(ctx, job, &report, prepared.Image, prepared.Image, renderDocument)
		return finish("partial", ""), nil, false
	}

	s.updateStage(job, "clean_background")
	cleanupStarted := s.now()
	cleaned, renderDocument, cleanupStats, err := s.renderer.Clean(ctx, prepared.Image, renderDocument)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return finish("cancelled", "clean_background"), nil, true
		}
		s.fileFailed(job, report.SourceFile, "clean_background", err)
		return finish("failed", "clean_background"), nil, false
	}
	report.DurationsMillis["cleanup"] = s.now().Sub(cleanupStarted).Milliseconds()
	report.RenderedBlocks = len(renderDocument.Blocks)
	report.SkippedBlocks = renderDocument.SkippedBlocks
	report.Warnings = renderDocument.Warnings
	s.logf(
		"image cleanup completed: job=%s name=%s uniform_regions=%d neural_regions=%d neural_clusters=%d preprocessing=%s inference=%s postprocessing=%s duration=%s",
		job.status.ID, report.SourceFile, cleanupStats.UniformRegions, cleanupStats.NeuralRegions, cleanupStats.NeuralClusters,
		cleanupStats.Preprocessing, cleanupStats.Inference, cleanupStats.Postprocessing, s.now().Sub(cleanupStarted),
	)
	if len(renderDocument.Blocks) == 0 {
		s.updateStage(job, "encode_output")
		if err := atomicWriteBytesContext(ctx, finalPath, prepared.Original); err != nil {
			if errors.Is(err, context.Canceled) {
				return finish("cancelled", "encode_output"), nil, true
			}
			s.fileFailed(job, report.SourceFile, "encode_output", err)
			return finish("failed", "encode_output"), nil, false
		}
		s.incrementRendered(job, "partial", len(renderDocument.Warnings))
		s.renderDebug(ctx, job, &report, prepared.Validated.Data, page)
		s.renderDebugArtifacts(ctx, job, &report, prepared.Image, prepared.Image, renderDocument)
		return finish("partial", ""), nil, false
	}
	cleanedDebug := cloneNRGBA(cleaned)
	s.updateStage(job, "render_text")
	renderStarted := s.now()
	if err := s.renderer.Draw(ctx, cleaned, renderDocument); err != nil {
		if errors.Is(err, context.Canceled) {
			return finish("cancelled", "render_text"), nil, true
		}
		s.fileFailed(job, report.SourceFile, "render_text", err)
		return finish("failed", "render_text"), nil, false
	}
	report.DurationsMillis["render"] = s.now().Sub(renderStarted).Milliseconds()
	s.updateStage(job, "encode_output")
	encodeStarted := s.now()
	if err := encodeRendered(ctx, cleaned, finalPath, prepared.Extension, s.renderer.config.JPEGQuality, s.ffmpeg); err != nil {
		if errors.Is(err, context.Canceled) {
			return finish("cancelled", "encode_output"), nil, true
		}
		s.fileFailed(job, report.SourceFile, "encode_output", err)
		return finish("failed", "encode_output"), nil, false
	}
	report.DurationsMillis["encode"] = s.now().Sub(encodeStarted).Milliseconds()
	s.updateStage(job, "verify_output")
	if err := ctx.Err(); err != nil {
		return finish("cancelled", "verify_output"), nil, true
	}
	status := "translated"
	if len(renderDocument.SkippedBlocks) > 0 {
		status = "partial"
	} else if len(renderDocument.Warnings) > 0 {
		status = "translated_with_warnings"
	}
	s.incrementRendered(job, status, len(renderDocument.Warnings))
	s.logf("image render completed: job=%s name=%s format=%s rendered_blocks=%d duration=%s", job.status.ID, report.SourceFile, prepared.Extension, len(renderDocument.Blocks), s.now().Sub(renderStarted))
	s.renderDebug(ctx, job, &report, prepared.Validated.Data, page)
	s.renderDebugArtifacts(ctx, job, &report, cleanedDebug, cleaned, renderDocument)
	return finish(status, ""), nil, false
}

func (s *Service) translateBlocksIndividually(ctx context.Context, source, target string, blocks []translation.TranslationBlock) ([]translation.TranslatedTextBlock, int, map[string]struct{}, error) {
	translated := make([]translation.TranslatedTextBlock, 0, len(blocks))
	failed := make(map[string]struct{})
	totalChunks := 0
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, totalChunks, failed, err
		}
		result, chunks, err := s.translator.Translate(ctx, source, target, []translation.TranslationBlock{block})
		totalChunks += chunks
		if err == nil {
			translated = append(translated, result...)
			continue
		}
		var completionErr *translation.CompletionError
		if errors.As(err, &completionErr) || errors.Is(err, context.Canceled) {
			return nil, totalChunks, failed, err
		}
		failed[block.ID] = struct{}{}
	}
	return translated, totalChunks, failed, nil
}

func (s *Service) renderDebug(ctx context.Context, job *batchJob, report *JobFileReport, data []byte, page ocr.OCRPage) {
	if !job.request.Debug {
		return
	}
	stem := strings.TrimSuffix(report.OutputName, filepath.Ext(report.OutputName))
	debugPath := filepath.Join(job.layout.Debug, stem+".ocr.png")
	if err := renderDebugImage(ctx, data, page, debugPath); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.addFileError(job, report.SourceFile, "debug_render", err, true)
			s.logf("image batch debug render warning: id=%s name=%s error=%v", job.status.ID, report.SourceFile, err)
		}
		return
	}
	report.DebugPath = relativeOutputPath(job.layout.Root, debugPath)
}

func (s *Service) writeFailedTranslation(path, sourceFile string, request StartImageBatchRequest, status, message string) error {
	return atomicWriteJSON(path, TranslationDocument{SchemaVersion: SchemaVersion, SourceFile: sourceFile, Source: request.Source, Target: request.Target, Status: status, Blocks: []TranslatedBlock{}, Error: message})
}

func (s *Service) fileFailed(job *batchJob, file, stage string, err error) {
	s.addFileError(job, file, stage, err, true)
	s.increment(job, "failed")
}
func (s *Service) addFileError(job *batchJob, file, stage string, err error, recoverable bool) {
	job.errors = append(job.errors, BatchFileError{File: file, Stage: stage, Message: safeMessage(err), Recoverable: recoverable})
}
func safeMessage(err error) string {
	if err == nil {
		return ""
	}
	return safePathError(err).Error()
}

func (s *Service) increment(job *batchJob, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case "translated":
		job.status.Translated++
	case "no_text":
		job.status.NoText++
	case "failed":
		job.status.Failed++
	}
	job.status.Processed++
}

func (s *Service) incrementRendered(job *batchJob, status string, warnings int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job.status.Rendered++
	job.status.Warnings += warnings
	if status == "partial" {
		job.status.Partial++
	} else {
		job.status.Translated++
	}
	job.status.Processed++
}

func (s *Service) syncSummary(job *batchJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job.report.Summary = JobSummary{Total: job.status.Total, Processed: job.status.Processed, Translated: job.status.Translated, Rendered: job.status.Rendered, Partial: job.status.Partial, Warnings: job.status.Warnings, NoText: job.status.NoText, Failed: job.status.Failed}
}

func (s *Service) updateCurrent(job *batchJob, file string) {
	s.mu.Lock()
	job.status.CurrentFile = file
	s.mu.Unlock()
}
func (s *Service) updateStage(job *batchJob, stage string) {
	s.mu.Lock()
	job.status.CurrentStage = stage
	s.mu.Unlock()
}
func (s *Service) setState(job *batchJob, state, message string) {
	s.mu.Lock()
	job.status.State = state
	job.status.Error = message
	s.mu.Unlock()
}

func (s *Service) writeReports(job *batchJob) error {
	if err := atomicWriteJSON(filepath.Join(job.layout.Root, "errors.json"), ErrorsDocument{SchemaVersion: SchemaVersion, Errors: job.errors}); err != nil {
		return err
	}
	return atomicWriteJSON(filepath.Join(job.layout.Root, "job.json"), job.report)
}

func (s *Service) finish(job *batchJob, state string, failure error) {
	completed := s.now()
	job.report.State = state
	job.report.CompletedAt = &completed
	s.syncSummary(job)
	if failure != nil {
		job.report.State = "failed"
		job.report.Error = safeMessage(failure)
		state = "failed"
	}
	if err := s.writeReports(job); err != nil {
		state = "failed"
		if failure == nil {
			failure = err
		}
	}
	s.mu.Lock()
	job.status.State = state
	job.status.CurrentFile = ""
	job.status.CurrentStage = ""
	if failure != nil {
		job.status.Error = safeMessage(failure)
	}
	s.activeID = ""
	s.busy = false
	status := job.status
	s.mu.Unlock()
	job.cancel()
	s.logf("image batch completed: id=%s state=%s total=%d translated=%d rendered=%d partial=%d warnings=%d no_text=%d failed=%d duration=%s", status.ID, status.State, status.Total, status.Translated, status.Rendered, status.Partial, status.Warnings, status.NoText, status.Failed, completed.Sub(job.report.StartedAt))
	if state == "completed" || state == "completed_with_errors" {
		if err := s.openDirectory(job.layout.Root); err != nil {
			s.logf("image batch output directory could not be opened: id=%s error=%v", status.ID, err)
		}
	}
}

func (s *Service) evictJobsLocked() {
	for len(s.jobOrder) >= retainedJobs {
		id := s.jobOrder[0]
		job := s.jobs[id]
		if job != nil && !terminalState(job.status.State) {
			return
		}
		delete(s.jobs, id)
		s.jobOrder = s.jobOrder[1:]
	}
}

func terminalState(state string) bool {
	return state == "completed" || state == "completed_with_errors" || state == "cancelled" || state == "failed"
}
func (s *Service) logf(format string, values ...any) {
	if s.logger != nil {
		s.logger.Printf(format, values...)
	}
}

func systemRenderError(stage string, err error) error {
	if stage == "load_font" {
		return err
	}
	return nil
}
