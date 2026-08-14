package ocr

import "strings"

type OCRBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type OCRPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// OCRPolygon preserves detector geometry in original-image coordinates. The
// legacy integer Box remains available for renderers that cannot consume a
// quadrilateral yet.
type OCRPolygon [4]OCRPoint

type OCRWord struct {
	ID                   string     `json:"id"`
	Text                 string     `json:"text"`
	Confidence           float64    `json:"confidence"`
	Box                  OCRBox     `json:"box"`
	Accepted             bool       `json:"accepted"`
	Polygon              OCRPolygon `json:"polygon,omitempty"`
	DetectorConfidence   float64    `json:"detectorConfidence,omitempty"`
	RecognizerConfidence float64    `json:"recognizerConfidence,omitempty"`
	Detected             bool       `json:"detected,omitempty"`
	Recognized           bool       `json:"recognized,omitempty"`
	TextAccepted         bool       `json:"textAccepted,omitempty"`
	CleanupSafe          bool       `json:"cleanupSafe,omitempty"`
	Recognizer           string     `json:"recognizer,omitempty"`
	GeometryLevel        string     `json:"geometryLevel,omitempty"`
	SemanticStatus       string     `json:"semanticStatus,omitempty"`
	SemanticReason       string     `json:"semanticReason,omitempty"`
	FragmentOf           string     `json:"fragmentOf,omitempty"`
	Page                 int        `json:"page"`
	Block                int        `json:"block"`
	Paragraph            int        `json:"paragraph"`
	Line                 int        `json:"line"`
	Word                 int        `json:"word"`
}

type OCRLine struct {
	ID         string    `json:"id"`
	Text       string    `json:"text"`
	Confidence float64   `json:"confidence"`
	Box        OCRBox    `json:"box"`
	Words      []OCRWord `json:"words"`
	Page       int       `json:"page"`
	Block      int       `json:"block"`
	Paragraph  int       `json:"paragraph"`
	Line       int       `json:"line"`
}

type OCRParagraph struct {
	ID         string    `json:"id"`
	Text       string    `json:"text"`
	Confidence float64   `json:"confidence"`
	Box        OCRBox    `json:"box"`
	Lines      []OCRLine `json:"lines"`
	Page       int       `json:"page"`
	Block      int       `json:"block"`
	Paragraph  int       `json:"paragraph"`
}

type OCRImageInfo struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	MediaType string `json:"mediaType"`
}

type OCRPage struct {
	SchemaVersion int            `json:"schemaVersion"`
	SourceFile    string         `json:"sourceFile"`
	Image         OCRImageInfo   `json:"image"`
	Words         []OCRWord      `json:"words"`
	Paragraphs    []OCRParagraph `json:"paragraphs"`
	Diagnostics   OCRDiagnostics `json:"ocrDiagnostics,omitempty"`
}

type OCRPreprocessDiagnostics struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	ScaleX      float64 `json:"scaleX"`
	ScaleY      float64 `json:"scaleY"`
	PaddingLeft int     `json:"paddingLeft"`
	PaddingTop  int     `json:"paddingTop"`
}

type OCRStageDuration struct {
	Stage          string `json:"stage"`
	DurationMillis int64  `json:"durationMillis"`
}

// OCRDiagnostics is additive so existing OCR JSON consumers remain valid.
// Invocation mode is deliberately logged rather than stored here: it must not
// make otherwise identical single and batch OCRPage values differ.
type OCRDiagnostics struct {
	Backend                string                        `json:"backend,omitempty"`
	Profile                string                        `json:"profile,omitempty"`
	RequestedSource        string                        `json:"requestedSource,omitempty"`
	ResolvedOCRLanguage    string                        `json:"resolvedOCRLanguage,omitempty"`
	DetectorModel          string                        `json:"detectorModel,omitempty"`
	RecognizerModel        string                        `json:"recognizerModel,omitempty"`
	RecognizerModels       []string                      `json:"recognizerModels,omitempty"`
	Dictionary             string                        `json:"dictionary,omitempty"`
	Dictionaries           []string                      `json:"dictionaries,omitempty"`
	InputWidth             int                           `json:"inputWidth,omitempty"`
	InputHeight            int                           `json:"inputHeight,omitempty"`
	Preprocess             []OCRPreprocessDiagnostics    `json:"preprocess,omitempty"`
	Tiles                  int                           `json:"tiles"`
	DetectorCandidates     int                           `json:"detectorCandidates"`
	RecognizedCandidates   int                           `json:"recognizedCandidates"`
	AcceptedTextCandidates int                           `json:"acceptedTextCandidates"`
	CleanupSafeCandidates  int                           `json:"cleanupSafeCandidates"`
	MergeDuplicates        int                           `json:"mergeDuplicates"`
	FinalLines             int                           `json:"finalLines"`
	FinalParagraphs        int                           `json:"finalParagraphs"`
	NonSemanticOCRNoise    int                           `json:"nonSemanticOCRNoise"`
	SemanticSourceBlocks   int                           `json:"semanticSourceBlocks"`
	AverageConfidence      float64                       `json:"averageConfidence"`
	MinimumConfidence      float64                       `json:"minimumConfidence"`
	Durations              []OCRStageDuration            `json:"durations,omitempty"`
	Regions                []OCRRegionDiagnostic         `json:"regions,omitempty"`
	ParagraphMerges        []OCRParagraphMergeDiagnostic `json:"paragraphMerges,omitempty"`
}

type OCRRegionDiagnostic struct {
	Pass                 string     `json:"pass"`
	Text                 string     `json:"text,omitempty"`
	Recognizer           string     `json:"recognizer,omitempty"`
	DetectorConfidence   float64    `json:"detectorConfidence"`
	RecognizerConfidence float64    `json:"recognizerConfidence"`
	Box                  OCRBox     `json:"box"`
	Polygon              OCRPolygon `json:"polygon"`
	Recognized           bool       `json:"recognized"`
	TextAccepted         bool       `json:"textAccepted"`
	CleanupSafe          bool       `json:"cleanupSafe"`
	SemanticStatus       string     `json:"semanticStatus,omitempty"`
	SemanticReason       string     `json:"semanticReason,omitempty"`
	FragmentOf           string     `json:"fragmentOf,omitempty"`
}

type OCRParagraphMergeDiagnostic struct {
	PreviousText      string  `json:"previousText,omitempty"`
	CurrentText       string  `json:"currentText,omitempty"`
	PreviousBox       OCRBox  `json:"previousBox"`
	CurrentBox        OCRBox  `json:"currentBox"`
	TentativeBox      OCRBox  `json:"tentativeBox,omitempty"`
	ForeignText       string  `json:"foreignText,omitempty"`
	ForeignBox        OCRBox  `json:"foreignBox,omitempty"`
	Reason            string  `json:"reason"`
	IntersectionRatio float64 `json:"intersectionRatio,omitempty"`
	LeftEdgeDelta     int     `json:"leftEdgeDelta,omitempty"`
	VerticalGap       int     `json:"verticalGap,omitempty"`
	LineHeightRatio   float64 `json:"lineHeightRatio,omitempty"`
}

func joinWords(words []OCRWord) string {
	var builder strings.Builder
	for _, word := range words {
		text := strings.TrimSpace(word.Text)
		if text == "" {
			continue
		}
		if builder.Len() > 0 && !startsWithClosingPunctuation(text) && !endsWithOpeningPunctuation(builder.String()) {
			builder.WriteByte(' ')
		}
		builder.WriteString(text)
	}
	return builder.String()
}

func startsWithClosingPunctuation(value string) bool {
	return strings.ContainsRune(".,;:!?%)]}»”,", []rune(value)[0])
}
func endsWithOpeningPunctuation(value string) bool {
	runes := []rune(value)
	return len(runes) > 0 && strings.ContainsRune("([{«“", runes[len(runes)-1])
}
func joinLines(lines []OCRLine) string {
	values := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line.Text) != "" {
			values = append(values, line.Text)
		}
	}
	return strings.Join(values, "\n")
}
func averageConfidence(words []OCRWord) float64 {
	if len(words) == 0 {
		return 0
	}
	var total float64
	for _, word := range words {
		total += word.Confidence
	}
	return total / float64(len(words))
}
func averageLineConfidence(lines []OCRLine) float64 {
	count := 0
	var total float64
	for _, line := range lines {
		for _, word := range line.Words {
			total += word.Confidence
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}
func unionWordBoxes(words []OCRWord) OCRBox {
	boxes := make([]OCRBox, 0, len(words))
	for _, word := range words {
		boxes = append(boxes, word.Box)
	}
	return unionBoxes(boxes)
}
func unionLineBoxes(lines []OCRLine) OCRBox {
	boxes := make([]OCRBox, 0, len(lines))
	for _, line := range lines {
		boxes = append(boxes, line.Box)
	}
	return unionBoxes(boxes)
}
func unionBoxes(boxes []OCRBox) OCRBox {
	if len(boxes) == 0 {
		return OCRBox{}
	}
	minX, minY := boxes[0].X, boxes[0].Y
	maxX, maxY := boxes[0].X+boxes[0].Width, boxes[0].Y+boxes[0].Height
	for _, box := range boxes[1:] {
		if box.X < minX {
			minX = box.X
		}
		if box.Y < minY {
			minY = box.Y
		}
		if box.X+box.Width > maxX {
			maxX = box.X + box.Width
		}
		if box.Y+box.Height > maxY {
			maxY = box.Y + box.Height
		}
	}
	return OCRBox{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}
}
