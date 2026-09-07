//go:build windows

package window

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/localmodel"
)

type settingsWebView struct {
	webview.WebView
	bindings map[string]any
}

func (w *settingsWebView) Bind(name string, fn any) error {
	w.bindings[name] = fn
	return nil
}

func TestSettingsBindingsModelListAndProfileSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Default()); err != nil {
		t.Fatal(err)
	}
	restarted := make(chan struct{}, 1)
	window := &MainWindow{cfgPath: path, onRestart: func() { restarted <- struct{}{} }}
	view := &settingsWebView{bindings: make(map[string]any)}
	if err := bindMainSettings(view, window); err != nil {
		t.Fatal(err)
	}
	get := view.bindings["GetSettingsConfig"].(func() (config.Config, error))
	save := view.bindings["SaveSettingsConfig"].(func(config.Config) error)
	list := view.bindings["GetLocalModels"].(func() ([]string, error))
	directory, err := config.ExecutableDir()
	if err != nil {
		t.Fatal(err)
	}
	wantModels, err := localmodel.ListModels(directory)
	if err != nil {
		t.Fatal(err)
	}
	models, err := list()
	if err != nil || !reflect.DeepEqual(models, wantModels) {
		t.Fatalf("models = %v, %v", models, err)
	}
	cfg, err := get()
	if err != nil || cfg.LocalModel.Profile != config.ProfileGeneric || cfg.LocalModel.ModelFile != "" {
		t.Fatalf("defaults = %+v, %v", cfg.LocalModel, err)
	}
	cfg.LocalModel.Profile = "generic"
	cfg.LocalModel.ModelFile = filepath.Join("models", "selected.gguf")
	if err := save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := get()
	if err != nil || !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("saved config = %+v, %v", loaded, err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("settings did not request restart")
	}
	cfg.LocalModel.Profile = "auto"
	if err := save(cfg); err == nil {
		t.Fatal("invalid profile saved")
	}
	loaded, err = get()
	if err != nil || loaded.LocalModel.Profile != "generic" {
		t.Fatalf("invalid save changed config: %+v, %v", loaded, err)
	}
}

func TestSettingsProfileOptionsFollowDebugMode(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		debug bool
		want  []string
	}{
		{name: "normal", want: []string{config.ProfileGeneric}},
		{name: "debug", debug: true, want: []string{config.ProfileGeneric, config.ProfileTranslateGemma}},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := &settingsWebView{bindings: make(map[string]any)}
			if err := bindMainSettings(view, &MainWindow{debug: test.debug}); err != nil {
				t.Fatal(err)
			}
			profiles := view.bindings["GetLocalModelProfiles"].(func() []string)()
			if !reflect.DeepEqual(profiles, test.want) {
				t.Fatalf("profiles = %v, want %v", profiles, test.want)
			}
		})
	}
}
