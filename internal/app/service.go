package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/logger"
	"github.com/sympllate/translator/internal/translation"
)

type Translator interface {
	Translate(ctx context.Context, req translation.TranslateRequest) (translation.TranslateResult, error)
}

type JobStatus struct {
	State  string                       `json:"state"`
	Result *translation.TranslateResult `json:"result,omitempty"`
	Error  string                       `json:"error,omitempty"`
}

type Service struct {
	ctx        context.Context
	translator Translator
	detector   language.Detector
	logger     logger.PrintLogger
	manualBusy atomic.Bool
	nextID     atomic.Uint64
	mu         sync.Mutex
	closed     bool
	jobs       map[string]JobStatus
	wg         sync.WaitGroup
}

func NewService(ctx context.Context, translator Translator, detector language.Detector, logger logger.PrintLogger) *Service {
	return &Service{ctx: ctx, translator: translator, detector: detector, logger: logger, jobs: make(map[string]JobStatus)}
}

func (s *Service) StartTranslate(req translation.TranslateRequest) (string, error) {
	if !s.manualBusy.CompareAndSwap(false, true) {
		return "", errors.New("previous translation is still in progress")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.manualBusy.Store(false)
		return "", errors.New("translation service is shutting down")
	}
	id := strconv.FormatUint(s.nextID.Add(1), 10)
	s.jobs[id] = JobStatus{State: "pending"}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer s.manualBusy.Store(false)
		started := time.Now()
		result, err := s.Translate(s.ctx, req)
		status := JobStatus{State: "done", Result: &result}
		if err != nil {
			status = JobStatus{State: "error", Error: err.Error()}
		}
		s.mu.Lock()
		s.jobs[id] = status
		s.mu.Unlock()
		if err != nil {
			s.logger.Printf("manual translation failed: source=%s target=%s chars=%d duration=%s error=%v", req.Source, req.Target, len([]rune(req.Text)), time.Since(started), err)
		} else {
			s.logger.Printf("manual translation completed: source=%s target=%s chars=%d duration=%s", req.Source, req.Target, len([]rune(req.Text)), time.Since(started))
		}
	}()
	return id, nil
}

func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *Service) Job(id string) (JobStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.jobs[id]
	if !ok {
		return JobStatus{}, fmt.Errorf("translation job %q not found", id)
	}
	if status.State != "pending" {
		delete(s.jobs, id)
	}
	return status, nil
}

func (s *Service) Translate(ctx context.Context, req translation.TranslateRequest) (translation.TranslateResult, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return translation.TranslateResult{}, errors.New("translation service is shutting down")
	}
	result, err := s.translator.Translate(ctx, req)
	if err != nil {
		return translation.TranslateResult{}, err
	}
	if req.Source == "auto" {
		result.DetectedLanguage = s.detector.Detect(req.Text)
	}
	return result, nil
}
