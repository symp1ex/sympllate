package inpaint

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	runtimeVersion = "1.26.0"
	modelSize      = 512
	runtimeName    = "onnxruntime.dll"
	modelName      = "inpainting_lama.onnx"
)

type Timings struct {
	Preprocessing  time.Duration
	Inference      time.Duration
	Postprocessing time.Duration
}

type Result struct {
	Image   *image.NRGBA
	Timings Timings
}

type Engine interface {
	Inpaint(ctx context.Context, source *image.NRGBA, mask *image.Gray) (Result, error)
	Close() error
}

type sessionRunner interface {
	Run(context.Context) error
	Destroy() error
}

type valueDestroyer interface {
	Destroy() error
}

type runtimeEngine struct {
	gate chan struct{}

	session      sessionRunner
	imageTensor  valueDestroyer
	maskTensor   valueDestroyer
	outputTensor valueDestroyer
	imageData    []float32
	maskData     []float32
	outputData   []float32
	environment  bool
	closed       bool
}

type advancedSession struct {
	session *ort.AdvancedSession
}

func (s advancedSession) Run(ctx context.Context) error {
	options, err := ort.NewRunOptions()
	if err != nil {
		return fmt.Errorf("create LaMa run options: %w", err)
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
	err = s.session.RunWithOptions(options)
	close(done)
	watcher.Wait()
	destroyErr := options.Destroy()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return fmt.Errorf("run LaMa inference: %w", err)
	}
	if destroyErr != nil {
		return fmt.Errorf("destroy LaMa run options: %w", destroyErr)
	}
	return nil
}

func (s advancedSession) Destroy() error { return s.session.Destroy() }

func NewEngine(executableDir string) (Engine, error) {
	if executableDir == "" {
		return nil, errors.New("inpaint executable directory is empty")
	}
	runtimePath := filepath.Join(executableDir, "bin", "inpaint", runtimeName)
	modelPath := filepath.Join(executableDir, "bin", "inpaint", modelName)
	if err := requireRegularFile(runtimePath); err != nil {
		return nil, fmt.Errorf("ONNX Runtime DLL %q is unavailable: %w", runtimePath, err)
	}
	if err := requireRegularFile(modelPath); err != nil {
		return nil, fmt.Errorf("LaMa model %q is unavailable: %w", modelPath, err)
	}
	if ort.IsInitialized() {
		return nil, errors.New("ONNX Runtime environment is already initialized")
	}

	ort.SetSharedLibraryPath(runtimePath)
	if err := ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
		return nil, fmt.Errorf("initialize ONNX Runtime from %q: %w", runtimePath, err)
	}
	engine := &runtimeEngine{gate: make(chan struct{}, 1), environment: true}
	engine.gate <- struct{}{}
	fail := func(err error) (Engine, error) {
		return nil, errors.Join(err, engine.destroyResources())
	}
	if version := ort.GetVersion(); version != runtimeVersion {
		return fail(fmt.Errorf("unsupported ONNX Runtime version %q from %q; expected %s", version, runtimePath, runtimeVersion))
	}

	imageTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, modelSize, modelSize))
	if err != nil {
		return fail(fmt.Errorf("create LaMa image tensor: %w", err))
	}
	engine.imageTensor, engine.imageData = imageTensor, imageTensor.GetData()
	maskTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, modelSize, modelSize))
	if err != nil {
		return fail(fmt.Errorf("create LaMa mask tensor: %w", err))
	}
	engine.maskTensor, engine.maskData = maskTensor, maskTensor.GetData()
	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, modelSize, modelSize))
	if err != nil {
		return fail(fmt.Errorf("create LaMa output tensor: %w", err))
	}
	engine.outputTensor, engine.outputData = outputTensor, outputTensor.GetData()

	options, err := ort.NewSessionOptions()
	if err != nil {
		return fail(fmt.Errorf("create LaMa session options: %w", err))
	}
	configureErr := errors.Join(
		options.SetExecutionMode(ort.ExecutionModeSequential),
		options.SetInterOpNumThreads(1),
		options.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll),
	)
	if configureErr != nil {
		_ = options.Destroy()
		return fail(fmt.Errorf("configure LaMa CPU session: %w", configureErr))
	}
	session, sessionErr := ort.NewAdvancedSession(
		modelPath,
		[]string{"image", "mask"},
		[]string{"output"},
		[]ort.Value{imageTensor, maskTensor},
		[]ort.Value{outputTensor},
		options,
	)
	optionsErr := options.Destroy()
	if sessionErr != nil {
		return fail(fmt.Errorf("load LaMa model %q: %w", modelPath, sessionErr))
	}
	if optionsErr != nil {
		_ = session.Destroy()
		return fail(fmt.Errorf("destroy LaMa session options: %w", optionsErr))
	}
	engine.session = advancedSession{session: session}
	return engine, nil
}

func (e *runtimeEngine) Inpaint(ctx context.Context, source *image.NRGBA, mask *image.Gray) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	started := time.Now()
	input, binaryMask, transform, err := preprocess(source, mask)
	if err != nil {
		return Result{}, err
	}
	timings := Timings{Preprocessing: time.Since(started)}

	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-e.gate:
	}
	if e.closed {
		e.gate <- struct{}{}
		return Result{}, errors.New("LaMa inpaint engine is closed")
	}
	copy(e.imageData, input)
	copy(e.maskData, binaryMask)
	inferenceStarted := time.Now()
	err = e.session.Run(ctx)
	timings.Inference = time.Since(inferenceStarted)
	if err != nil {
		e.gate <- struct{}{}
		return Result{}, err
	}
	output := append([]float32(nil), e.outputData...)
	e.gate <- struct{}{}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	postprocessStarted := time.Now()
	result, err := postprocess(source, mask, output, transform)
	timings.Postprocessing = time.Since(postprocessStarted)
	if err != nil {
		return Result{}, err
	}
	return Result{Image: result, Timings: timings}, nil
}

func (e *runtimeEngine) Close() error {
	if e == nil {
		return nil
	}
	<-e.gate
	defer func() { e.gate <- struct{}{} }()
	if e.closed {
		return nil
	}
	e.closed = true
	return e.destroyResources()
}

func (e *runtimeEngine) destroyResources() error {
	var errs []error
	if e.session != nil {
		errs = append(errs, e.session.Destroy())
		e.session = nil
	}
	for _, tensor := range []valueDestroyer{e.outputTensor, e.maskTensor, e.imageTensor} {
		if tensor != nil {
			errs = append(errs, tensor.Destroy())
		}
	}
	e.outputTensor, e.maskTensor, e.imageTensor = nil, nil, nil
	e.outputData, e.maskData, e.imageData = nil, nil, nil
	if e.environment {
		errs = append(errs, ort.DestroyEnvironment())
		e.environment = false
	}
	return errors.Join(errs...)
}

func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	return nil
}
