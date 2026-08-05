package webassets

import (
	"strings"
	"testing"
)

func TestHTMLInlinesProductionAssets(t *testing.T) {
	page, err := HTML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "<style>") || !strings.Contains(page, `<script type="module">`) {
		t.Fatal("frontend assets were not inlined")
	}
	if strings.Contains(page, `src="./assets/`) || strings.Contains(page, `href="./assets/`) {
		t.Fatal("external frontend asset reference remains")
	}
}

func TestPopupUsesAutomaticTargetTranslationWithoutSourceText(t *testing.T) {
	page, err := HTML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "Translate again") {
		t.Fatal("popup retry button remains in production assets")
	}
	if strings.Contains(page, "popup-source") {
		t.Fatal("popup source text remains in production assets")
	}
	if !strings.Contains(page, "SetQuickTranslationTarget") {
		t.Fatal("popup target language does not call the automatic translation binding")
	}
}
