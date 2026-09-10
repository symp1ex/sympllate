package imagebatch

import (
	"os"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/ffmpeg"
	"github.com/sympllate/translator/internal/inpaint"
	sharedort "github.com/sympllate/translator/internal/onnxruntime"
	"github.com/sympllate/translator/internal/translation"
)

func TestBatchPrerequisitesReportEveryUnavailableComponent(t *testing.T) {
	t.Parallel()
	check := NewPrerequisites(t.TempDir(), nil, nil, false)
	err := check.Check()
	if err == nil {
		t.Fatal("expected unavailable Batch Images prerequisites")
	}
	for _, component := range []string{"OCR", "ONNX Runtime", "inpaint / LaMa", "FFmpeg"} {
		if !strings.Contains(err.Error(), component) {
			t.Errorf("error %q does not mention %s", err, component)
		}
	}
}

func TestBatchPrerequisitesRecheckEveryComponent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		component string
		breakIt   func(string, *fakeBatchOCR) error
	}{
		{name: "OCR", component: "OCR", breakIt: func(_ string, recognizer *fakeBatchOCR) error {
			recognizer.capability = translation.ImageCapability{Reason: "OCR models removed"}
			return nil
		}},
		{name: "ONNX Runtime", component: "ONNX Runtime", breakIt: func(directory string, _ *fakeBatchOCR) error {
			return os.Remove(sharedort.DLLPath(directory))
		}},
		{name: "inpaint", component: "inpaint / LaMa", breakIt: func(directory string, _ *fakeBatchOCR) error {
			return os.Remove(inpaint.ModelPath(directory))
		}},
		{name: "FFmpeg", component: "FFmpeg", breakIt: func(directory string, _ *fakeBatchOCR) error {
			return os.Remove(ffmpeg.ExecutablePath(directory))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeBatchPrerequisiteFiles(t, directory)
			recognizer := &fakeBatchOCR{}
			check := NewPrerequisites(directory, recognizer, &fakeInpaintEngine{}, true)
			if err := check.Check(); err != nil {
				t.Fatalf("complete prerequisites rejected: %v", err)
			}
			if err := test.breakIt(directory, recognizer); err != nil {
				t.Fatal(err)
			}
			if err := check.Check(); err == nil || !strings.Contains(err.Error(), test.component) {
				t.Fatalf("unavailable %s error = %v", test.component, err)
			}
		})
	}
}
