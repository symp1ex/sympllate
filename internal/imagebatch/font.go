package imagebatch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

type fontCache struct {
	path  string
	once  sync.Once
	font  *opentype.Font
	err   error
	mu    sync.Mutex
	faces map[int]font.Face
}

func newFontCache(executableDir string) *fontCache {
	return &fontCache{path: filepath.Join(executableDir, "bin", "fonts", "regular.ttf"), faces: make(map[int]font.Face)}
}

func (c *fontCache) load() (*opentype.Font, error) {
	c.once.Do(func() {
		data, err := os.ReadFile(c.path)
		if err != nil {
			c.err = errors.New("Не удалось создать итоговое изображение: файл шрифта bin\\fonts\\regular.ttf не найден")
			return
		}
		c.font, c.err = opentype.Parse(data)
		if c.err != nil {
			c.err = fmt.Errorf("Не удалось создать итоговое изображение: файл шрифта повреждён: %w", c.err)
		}
	})
	return c.font, c.err
}

func (c *fontCache) face(size float64) (font.Face, error) {
	parsed, err := c.load()
	if err != nil {
		return nil, err
	}
	key := int(size*64 + 0.5)
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.faces[key]; existing != nil {
		return existing, nil
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: float64(key) / 64, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("load font face: %w", err)
	}
	for _, required := range []rune{'A', 'Я'} {
		if _, ok := face.GlyphAdvance(required); !ok {
			if closer, closeOK := face.(interface{ Close() error }); closeOK {
				_ = closer.Close()
			}
			return nil, fmt.Errorf("Не удалось создать итоговое изображение: шрифт не содержит обязательный символ %q", required)
		}
	}
	c.faces[key] = face
	return face, nil
}

func (c *fontCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, face := range c.faces {
		if closer, ok := face.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		delete(c.faces, key)
	}
}
