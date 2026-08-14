package ocr

import (
	"math"
	"time"
)

const (
	DefaultTimeout       = 30 * time.Second
	maximumTileGridCells = 8
)

func tileGrid(width, height, desired int) (int, int) {
	bestColumns, bestRows := 1, desired
	bestScore := math.MaxFloat64
	for rows := 1; rows <= desired; rows++ {
		columns := ceilingDivision(desired, rows)
		if columns*rows > maximumTileGridCells {
			continue
		}
		cellRatio := (float64(width) / float64(columns)) / (float64(height) / float64(rows))
		score := math.Abs(math.Log(cellRatio))
		if score < bestScore {
			bestScore, bestColumns, bestRows = score, columns, rows
		}
	}
	return bestColumns, bestRows
}
