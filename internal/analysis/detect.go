package analysis

import "time"

// pivotWindow assembles the LeftBars+1+RightBars candles a pivot
// centered at pivotTime needs, by exact time lookup -- never array-index
// arithmetic, which would silently misalign the moment the underlying
// candle series has a gap (a real, expected condition; see the
// ingestion milestone's gap recording). ok is false if any required
// slot is missing from byTime or is not closed: "cannot evaluate this
// pivot yet", which the caller must treat as withholding, never as a
// disqualification -- a gap in today's read is not evidence the pivot
// doesn't qualify, only that there isn't enough information to say.
func pivotWindow(byTime map[time.Time]CandleInput, pivotTime time.Time, leftBars, rightBars int, interval time.Duration) ([]CandleInput, bool) {
	total := leftBars + rightBars + 1
	window := make([]CandleInput, total)
	for i := range total {
		t := pivotTime.Add(time.Duration(i-leftBars) * interval)
		c, ok := byTime[t]
		if !ok || !c.IsClosed {
			return nil, false
		}
		window[i] = c
	}
	return window, true
}

// detectPivot reports whether window's center candle (at index
// leftBars) is a confirmed swing high and/or swing low (spec section
// 1): strictly greater (high) or strictly lower (low) than every one of
// its leftBars left and rightBars right neighbors in window. Equality
// on either side disqualifies -- every comparison is strict, never
// >=/<=.
func detectPivot(window []CandleInput, leftBars, rightBars int) (isHigh, isLow bool) {
	pivot := window[leftBars]
	isHigh, isLow = true, true

	for d := 1; d <= leftBars; d++ {
		n := window[leftBars-d]
		if n.High >= pivot.High {
			isHigh = false
		}
		if n.Low <= pivot.Low {
			isLow = false
		}
	}
	for d := 1; d <= rightBars; d++ {
		n := window[leftBars+d]
		if n.High >= pivot.High {
			isHigh = false
		}
		if n.Low <= pivot.Low {
			isLow = false
		}
	}
	return isHigh, isLow
}
