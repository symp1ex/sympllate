package ocr

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const maxStructuredOutputBytes = 16 << 20

type OCRFilterConfig struct {
	MinimumWordConfidence float64
	MinimumWordWidth      int
	MinimumWordHeight     int
}

func DefaultFilterConfig() OCRFilterConfig {
	return OCRFilterConfig{MinimumWordConfidence: 35, MinimumWordWidth: 1, MinimumWordHeight: 1}
}

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

type tsvKey struct{ page, block, paragraph, line int }

func ParseTSV(input io.Reader, imageWidth, imageHeight int, filter OCRFilterConfig) (OCRPage, error) {
	if imageWidth <= 0 || imageHeight <= 0 {
		return OCRPage{}, errors.New("image dimensions must be positive")
	}
	reader := bufio.NewReader(input)
	headerLine, err := readTSVLine(reader)
	if err != nil {
		return OCRPage{}, fmt.Errorf("read TSV header: %w", err)
	}
	header := strings.Split(headerLine, "\t")
	indexes, err := tsvHeaderIndexes(header)
	if err != nil {
		return OCRPage{}, err
	}
	words := make([]OCRWord, 0)
	row := 1
	for {
		line, readErr := readTSVLine(reader)
		if errors.Is(readErr, io.EOF) {
			break
		}
		row++
		if readErr != nil {
			return OCRPage{}, fmt.Errorf("read TSV row %d: %w", row, readErr)
		}
		record := strings.SplitN(line, "\t", len(header))
		if len(record) != len(header) {
			return OCRPage{}, fmt.Errorf("TSV row %d has %d fields; expected %d", row, len(record), len(header))
		}
		level, err := tsvInt(record, indexes, "level", row)
		if err != nil {
			return OCRPage{}, err
		}
		if level != 5 {
			continue
		}
		word, err := parseTSVWord(record, indexes, row, imageWidth, imageHeight, filter)
		if err != nil {
			return OCRPage{}, err
		}
		words = append(words, word)
	}
	sort.SliceStable(words, func(i, j int) bool { return lessWord(words[i], words[j]) })
	paragraphs := groupWords(words)
	return OCRPage{SchemaVersion: 1, Image: OCRImageInfo{Width: imageWidth, Height: imageHeight}, Words: words, Paragraphs: paragraphs}, nil
}

func readTSVLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) && len(line) > 0 {
		err = nil
	}
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func tsvHeaderIndexes(header []string) (map[string]int, error) {
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	indexes := make(map[string]int, len(header))
	for index, name := range header {
		indexes[strings.TrimSpace(name)] = index
	}
	for _, required := range []string{"level", "page_num", "block_num", "par_num", "line_num", "word_num", "left", "top", "width", "height", "conf", "text"} {
		if _, ok := indexes[required]; !ok {
			return nil, fmt.Errorf("Tesseract TSV header is missing %q", required)
		}
	}
	return indexes, nil
}

func parseTSVWord(record []string, indexes map[string]int, row, imageWidth, imageHeight int, filter OCRFilterConfig) (OCRWord, error) {
	values := make(map[string]int, 9)
	for _, field := range []string{"page_num", "block_num", "par_num", "line_num", "word_num", "left", "top", "width", "height"} {
		value, err := tsvInt(record, indexes, field, row)
		if err != nil {
			return OCRWord{}, err
		}
		values[field] = value
	}
	confidence, err := strconv.ParseFloat(strings.TrimSpace(record[indexes["conf"]]), 64)
	if err != nil {
		return OCRWord{}, fmt.Errorf("TSV row %d field conf is not a number: %w", row, err)
	}
	box := OCRBox{X: values["left"], Y: values["top"], Width: values["width"], Height: values["height"]}
	if box.X < 0 || box.Y < 0 || box.Width < 0 || box.Height < 0 || box.X > imageWidth-box.Width || box.Y > imageHeight-box.Height {
		return OCRWord{}, fmt.Errorf("TSV row %d bounding box is outside the %dx%d image", row, imageWidth, imageHeight)
	}
	text := record[indexes["text"]]
	accepted := strings.TrimSpace(text) != "" && confidence >= filter.MinimumWordConfidence && box.Width >= filter.MinimumWordWidth && box.Height >= filter.MinimumWordHeight
	return OCRWord{
		ID:   fmt.Sprintf("p%d-b%d-par%d-l%d-w%d", values["page_num"], values["block_num"], values["par_num"], values["line_num"], values["word_num"]),
		Text: text, Confidence: confidence, Box: box, Accepted: accepted,
		Page: values["page_num"], Block: values["block_num"], Paragraph: values["par_num"], Line: values["line_num"], Word: values["word_num"],
	}, nil
}

func tsvInt(record []string, indexes map[string]int, field string, row int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(record[indexes[field]]))
	if err != nil {
		return 0, fmt.Errorf("TSV row %d field %s is not an integer: %w", row, field, err)
	}
	return value, nil
}

func lessWord(left, right OCRWord) bool {
	a := []int{left.Page, left.Block, left.Paragraph, left.Line, left.Word}
	b := []int{right.Page, right.Block, right.Paragraph, right.Line, right.Word}
	for index := range a {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return left.ID < right.ID
}

func groupWords(words []OCRWord) []OCRParagraph {
	lineGroups := make(map[tsvKey][]OCRWord)
	keys := make([]tsvKey, 0)
	for _, word := range words {
		if !word.Accepted {
			continue
		}
		key := tsvKey{word.Page, word.Block, word.Paragraph, word.Line}
		if _, exists := lineGroups[key]; !exists {
			keys = append(keys, key)
		}
		lineGroups[key] = append(lineGroups[key], word)
	}
	sort.Slice(keys, func(i, j int) bool { return lessKey(keys[i], keys[j]) })
	paragraphs := make([]OCRParagraph, 0)
	var current *OCRParagraph
	for _, key := range keys {
		lineWords := lineGroups[key]
		line := OCRLine{
			ID:   fmt.Sprintf("p%d-b%d-par%d-l%d", key.page, key.block, key.paragraph, key.line),
			Text: joinWords(lineWords), Confidence: averageConfidence(lineWords), Box: unionWordBoxes(lineWords), Words: lineWords,
			Page: key.page, Block: key.block, Paragraph: key.paragraph, Line: key.line,
		}
		if current == nil || current.Page != key.page || current.Block != key.block || current.Paragraph != key.paragraph {
			paragraphs = append(paragraphs, OCRParagraph{
				ID:   fmt.Sprintf("p%d-b%d-par%d", key.page, key.block, key.paragraph),
				Page: key.page, Block: key.block, Paragraph: key.paragraph,
			})
			current = &paragraphs[len(paragraphs)-1]
		}
		current.Lines = append(current.Lines, line)
	}
	for index := range paragraphs {
		paragraphs[index].Text = joinLines(paragraphs[index].Lines)
		paragraphs[index].Confidence = averageLineConfidence(paragraphs[index].Lines)
		paragraphs[index].Box = unionLineBoxes(paragraphs[index].Lines)
	}
	return paragraphs
}

func lessKey(a, b tsvKey) bool {
	if a.page != b.page {
		return a.page < b.page
	}
	if a.block != b.block {
		return a.block < b.block
	}
	if a.paragraph != b.paragraph {
		return a.paragraph < b.paragraph
	}
	return a.line < b.line
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
