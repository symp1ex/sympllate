package ocr

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type detectorConfig struct {
	ResizeLong    int
	Threshold     float32
	BoxThreshold  float32
	UnclipRatio   float64
	Mean, Std     [3]float32
	ColorOrder    string
	TensorLayout  string
	ScoreMode     string
	Dilation      bool
	MaxCandidates int
}

type paddleDocumentProfile struct {
	Name                  string
	TileSize              int
	TileOverlap           int
	MaximumTiles          int
	MaximumDetectorPasses int
	MaximumWorkingPixels  int
}

func defaultPaddleDocumentProfile() paddleDocumentProfile {
	return paddleDocumentProfile{
		Name: "document", TileSize: 1280, TileOverlap: 160,
		MaximumTiles: 8, MaximumDetectorPasses: 9,
		MaximumWorkingPixels: 48_000_000,
	}
}

type recognizerConfig struct {
	Height, Width int
	MaxWidth      int
	Characters    []string
}

func loadDetectorConfig(path string) (detectorConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return detectorConfig{}, fmt.Errorf("PaddleOCR detector config missing: %w", err)
	}
	text := string(data)
	config := detectorConfig{
		ResizeLong:   yamlInt(text, "resize_long", 960),
		Threshold:    float32(yamlFloat(text, "thresh", .3)),
		BoxThreshold: float32(yamlFloat(text, "box_thresh", .6)),
		UnclipRatio:  yamlFloat(text, "unclip_ratio", 1.5),
		Mean:         yamlFloatTriplet(text, "mean", [3]float32{.485, .456, .406}),
		Std:          yamlFloatTriplet(text, "std", [3]float32{.229, .224, .225}),
		ColorOrder:   yamlString(text, "img_mode", "BGR"),
		TensorLayout: "CHW", ScoreMode: yamlString(text, "score_mode", "fast"),
		Dilation:      yamlBool(text, "use_dilation", false),
		MaxCandidates: yamlInt(text, "max_candidates", 1000),
	}
	if config.ResizeLong <= 0 || config.Threshold <= 0 || config.BoxThreshold <= 0 || config.UnclipRatio <= 0 {
		return detectorConfig{}, errorsUnexpectedConfig("detector", path)
	}
	return config, nil
}

func yamlString(text, key, fallback string) string {
	needle := key + ":"
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, needle) {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, needle))
			if value != "" {
				return parseYAMLScalar(value)
			}
		}
	}
	return fallback
}

func yamlBool(text, key string, fallback bool) bool {
	value := strings.ToLower(yamlString(text, key, ""))
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	return fallback
}

func yamlFloatTriplet(text, key string, fallback [3]float32) [3]float32 {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != key+":" {
			continue
		}
		var result [3]float32
		for item := 0; item < len(result) && index+1+item < len(lines); item++ {
			value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[index+1+item]), "-"))
			parsed, err := strconv.ParseFloat(value, 32)
			if err != nil {
				return fallback
			}
			result[item] = float32(parsed)
		}
		return result
	}
	return fallback
}

func loadRecognizerConfig(path string) (recognizerConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return recognizerConfig{}, fmt.Errorf("PaddleOCR recognizer config missing: %w", err)
	}
	defer file.Close()
	config := recognizerConfig{Height: 48, Width: 320, MaxWidth: 320}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	inDictionary := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !inDictionary && strings.HasPrefix(trimmed, "- ") {
			if dimension, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))); parseErr == nil && dimension > config.MaxWidth {
				config.MaxWidth = dimension
			}
		}
		if trimmed == "character_dict:" {
			inDictionary = true
			continue
		}
		if inDictionary {
			listLine := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(listLine, "-") {
				value := strings.TrimPrefix(listLine, "-")
				value = strings.TrimPrefix(value, " ")
				config.Characters = append(config.Characters, parseYAMLScalar(strings.TrimSuffix(value, "\r")))
				continue
			}
			if trimmed != "" {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return recognizerConfig{}, fmt.Errorf("read PaddleOCR recognizer config: %w", err)
	}
	if len(config.Characters) == 0 {
		return recognizerConfig{}, errorsUnexpectedConfig("recognizer", path)
	}
	// Paddle's CTCLabelDecode appends a literal space after character_dict;
	// class zero remains the CTC blank.
	config.Characters = append(config.Characters, " ")
	return config, nil
}

func parseYAMLScalar(value string) string {
	if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
		value = value[1 : len(value)-1]
	}
	return strings.ReplaceAll(value, "''", "'")
}

func yamlInt(text, key string, fallback int) int {
	value := yamlFloat(text, key, float64(fallback))
	return int(value)
}

func yamlFloat(text, key string, fallback float64) float64 {
	needle := key + ":"
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, needle) {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(trimmed, needle)), 64)
		if err == nil {
			return value
		}
	}
	return fallback
}

func errorsUnexpectedConfig(kind, path string) error {
	return fmt.Errorf("PaddleOCR %s config %q has unexpected schema", kind, path)
}
