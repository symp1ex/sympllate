package imagebatch

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sympllate/translator/internal/ffmpeg"
	"github.com/sympllate/translator/internal/inpaint"
	sharedort "github.com/sympllate/translator/internal/onnxruntime"
)

type Prerequisites struct {
	executableDir        string
	ocr                  StructuredOCR
	inpainter            inpaint.Engine
	onnxRuntimeAvailable bool
}

func NewPrerequisites(executableDir string, recognizer StructuredOCR, inpainter inpaint.Engine, onnxRuntimeAvailable bool) Prerequisites {
	return Prerequisites{
		executableDir: executableDir, ocr: recognizer, inpainter: inpainter,
		onnxRuntimeAvailable: onnxRuntimeAvailable,
	}
}

func (p Prerequisites) Check() error {
	issues := make([]string, 0, 4)
	if p.ocr == nil {
		issues = append(issues, "OCR (not initialized)")
	} else if capability := p.ocr.Capability(); !capability.Supported {
		issues = append(issues, componentIssue("OCR", capability.Reason))
	}
	if !p.onnxRuntimeAvailable {
		issues = append(issues, "ONNX Runtime (not initialized)")
	} else if err := requireRegularFile(sharedort.DLLPath(p.executableDir)); err != nil {
		issues = append(issues, componentIssue("ONNX Runtime", err.Error()))
	}
	if p.inpainter == nil {
		issues = append(issues, "inpaint / LaMa (not initialized)")
	} else if err := inpaint.RequireModel(p.executableDir); err != nil {
		issues = append(issues, componentIssue("inpaint / LaMa", err.Error()))
	}
	if err := ffmpeg.RequireExecutable(ffmpeg.ExecutablePath(p.executableDir), "FFmpeg"); err != nil {
		issues = append(issues, componentIssue("FFmpeg", err.Error()))
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("Batch Images requires unavailable components: %s", strings.Join(issues, "; "))
}

func componentIssue(name, reason string) string {
	if strings.TrimSpace(reason) == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, reason)
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
