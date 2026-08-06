package imagebatch

import (
	"time"

	"github.com/sympllate/translator/internal/ocr"
)

const SchemaVersion = 1

type BatchSelectionKind string

const (
	SelectionFiles     BatchSelectionKind = "files"
	SelectionDirectory BatchSelectionKind = "directory"
)

type BatchSelection struct {
	ID          string             `json:"id"`
	Kind        BatchSelectionKind `json:"kind"`
	DisplayName string             `json:"displayName"`
	FileCount   int                `json:"fileCount"`
}

type StartImageBatchRequest struct {
	SelectionID string `json:"selectionId"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	Debug       bool   `json:"debug"`
}

type ImageBatchStatus struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	Total           int    `json:"total"`
	Processed       int    `json:"processed"`
	Translated      int    `json:"translated"`
	NoText          int    `json:"noText"`
	Failed          int    `json:"failed"`
	CurrentFile     string `json:"currentFile,omitempty"`
	OutputDirectory string `json:"outputDirectory,omitempty"`
	Error           string `json:"error,omitempty"`
}

type TranslationDocument struct {
	SchemaVersion int               `json:"schemaVersion"`
	SourceFile    string            `json:"sourceFile"`
	Source        string            `json:"source"`
	Target        string            `json:"target"`
	Status        string            `json:"status"`
	Blocks        []TranslatedBlock `json:"blocks"`
	Error         string            `json:"error,omitempty"`
}

type TranslatedBlock struct {
	ID             string           `json:"id"`
	SourceText     string           `json:"sourceText"`
	TranslatedText string           `json:"translatedText"`
	Confidence     float64          `json:"confidence"`
	Box            ocr.OCRBox       `json:"box"`
	Parts          []TranslatedPart `json:"parts,omitempty"`
}

type TranslatedPart struct {
	ID             string `json:"id"`
	SourceText     string `json:"sourceText"`
	TranslatedText string `json:"translatedText"`
}

type BatchFileError struct {
	File        string `json:"file"`
	Stage       string `json:"stage"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
}

type ErrorsDocument struct {
	SchemaVersion int              `json:"schemaVersion"`
	Errors        []BatchFileError `json:"errors"`
}

type JobReport struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	State         string          `json:"state"`
	StartedAt     time.Time       `json:"startedAt"`
	CompletedAt   *time.Time      `json:"completedAt,omitempty"`
	Source        string          `json:"source"`
	Target        string          `json:"target"`
	Error         string          `json:"error,omitempty"`
	Selection     JobSelection    `json:"selection"`
	Summary       JobSummary      `json:"summary"`
	Files         []JobFileReport `json:"files"`
}

type JobSelection struct {
	Kind        BatchSelectionKind `json:"kind"`
	DisplayName string             `json:"displayName"`
	FileCount   int                `json:"fileCount"`
}

type JobSummary struct {
	Total      int `json:"total"`
	Processed  int `json:"processed"`
	Translated int `json:"translated"`
	NoText     int `json:"noText"`
	Failed     int `json:"failed"`
}

type JobFileReport struct {
	SourceID        string `json:"sourceId"`
	SourceFile      string `json:"sourceFile"`
	OutputName      string `json:"outputName,omitempty"`
	Status          string `json:"status"`
	OCRPath         string `json:"ocrPath,omitempty"`
	TranslationPath string `json:"translationPath,omitempty"`
	DebugPath       string `json:"debugPath,omitempty"`
	DurationMillis  int64  `json:"durationMillis"`
	ErrorStage      string `json:"errorStage,omitempty"`
}
