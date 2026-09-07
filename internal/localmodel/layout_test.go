package localmodel

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/config"
)

func TestListModelsIsDynamicAndResolutionIsExplicit(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	list, err := ListModels(base)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("missing directory list = %v, %v", list, err)
	}
	models := filepath.Join(base, "models")
	if err := os.Mkdir(models, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveModel(base, ""); err == nil {
		t.Fatal("empty directory resolved a model")
	}
	if err := os.Mkdir(filepath.Join(models, "directory.gguf"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gemma-3-1b-it-Q8_0.gguf", "translategemma.GGUF", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(models, name), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{filepath.Join("models", "gemma-3-1b-it-Q8_0.gguf"), filepath.Join("models", "translategemma.GGUF")}
	list, err = ListModels(base)
	if err != nil || !reflect.DeepEqual(list, want) {
		t.Fatalf("list = %v, %v; want %v", list, err, want)
	}
	if _, err := ResolveModel(base, ""); err == nil {
		t.Fatal("multiple models resolved without selection")
	}
	for _, selected := range []string{want[0], filepath.Join(base, want[1])} {
		got, err := ResolveModel(base, selected)
		expected := selected
		if !filepath.IsAbs(expected) {
			expected = filepath.Join(base, expected)
		}
		if err != nil || got != expected {
			t.Fatalf("explicit selection = %q, %v", got, err)
		}
	}
	if err := os.Remove(filepath.Join(base, want[1])); err != nil {
		t.Fatal(err)
	}
	list, err = ListModels(base)
	if err != nil || !reflect.DeepEqual(list, want[:1]) {
		t.Fatalf("stale model list = %v, %v", list, err)
	}
	if _, err := ResolveModel(base, want[1]); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing explicit model silently replaced: %v", err)
	}
	if got, err := ResolveModel(base, ""); err != nil || got != filepath.Join(base, want[0]) {
		t.Fatalf("single model auto selection = %q, %v", got, err)
	}
}

func TestResolveModelFindsExactlyOneGGUF(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	models := filepath.Join(base, "models")
	if err := os.MkdirAll(models, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(models, "translator.gguf")
	if err := os.WriteFile(want, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "readme.txt"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveModel(base, "")
	if err != nil || got != want {
		t.Fatalf("ResolveModel() = %q, %v; want %q", got, err, want)
	}
}

func TestResolveModelRejectsMissingAndMultiple(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if _, err := ResolveModel(base, ""); err == nil || !strings.Contains(err.Error(), "no GGUF model found") {
		t.Fatalf("missing model error = %v", err)
	}
	models := filepath.Join(base, "models")
	if err := os.MkdirAll(models, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.gguf", "two.GGUF"} {
		if err := os.WriteFile(filepath.Join(models, name), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResolveModel(base, ""); err == nil || !strings.Contains(err.Error(), "multiple GGUF models") {
		t.Fatalf("multiple models error = %v", err)
	}
}

func TestResolveModelUsesExecutableDirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	want := filepath.Join(base, "custom", "model.gguf")
	if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveModel(base, filepath.Join("custom", "model.gguf"))
	if err != nil || got != want {
		t.Fatalf("ResolveModel() = %q, %v; want %q", got, err, want)
	}
}

func TestSelectProvider(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	cfg := config.Default().LocalModel
	provider, _, err := SelectProvider(config.ProviderAuto, base, cfg)
	if err != nil || provider != config.ProviderOllama {
		t.Fatalf("auto without layout = %q, %v", provider, err)
	}
	if _, _, err := SelectProvider(config.ProviderLocal, base, cfg); err == nil {
		t.Fatal("local without layout expected error")
	}
	server := filepath.Join(base, "runtime", "llama", "llama-server.exe")
	model := filepath.Join(base, "models", "model.gguf")
	for _, path := range []string{server, model} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provider, layout, err := SelectProvider(config.ProviderAuto, base, cfg)
	if err != nil || provider != config.ProviderLocal || layout.ModelPath != model {
		t.Fatalf("auto with layout = %q, %+v, %v", provider, layout, err)
	}
}

func TestBuildArgumentsUsesProfileAndRequiresAutomaticGPUFit(t *testing.T) {
	t.Parallel()
	args := BuildArguments(Layout{ModelPath: `C:\app\models\m.gguf`}, config.ProfileGeneric, 4321, "secret", 2048, 1024)
	joined := strings.Join(args, " ")
	for _, required := range []string{
		"--host 127.0.0.1", "--port 4321", "--alias " + ModelAlias,
		"--no-webui", "--no-jinja", "--offline", "--parallel 1", "--ctx-size 2048",
		"--gpu-layers auto", "--fit on", "--fit-target 1024",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("arguments missing %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "--gpu-layers all") {
		t.Fatalf("unsafe gpu layer mode: %s", joined)
	}
	translateGemmaArgs := BuildArguments(Layout{ModelPath: `C:\app\models\m.gguf`}, config.ProfileTranslateGemma, 4321, "secret", 2048, 1024)
	if strings.Contains(strings.Join(translateGemmaArgs, " "), "--no-jinja") {
		t.Fatalf("TranslateGemma arguments disable Jinja: %v", translateGemmaArgs)
	}
}
