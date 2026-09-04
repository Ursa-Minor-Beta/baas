package service

import (
	"encoding/csv"
	"io"
	"strings"
)

type csvDelimiter string

const (
	commaDelimiter     csvDelimiter = ","
	semicolonDelimiter csvDelimiter = ";"
	tabDelimiter       csvDelimiter = "\t"
)

const minCsvScore = 0.5

func sniffCsvDelimiter(text string) (delimiter csvDelimiter, score float64, ok bool) {
	delimiters := []csvDelimiter{commaDelimiter, semicolonDelimiter, tabDelimiter}

	bestScore := 0.0
	var bestDelimiter csvDelimiter = ","

	for _, d := range delimiters {
		s, ok := scoreCsvDelimiter(text, d)
		if ok && s > bestScore {
			bestScore = s
			bestDelimiter = d
		}
	}
	return bestDelimiter, bestScore, bestScore >= minCsvScore
}

func scoreCsvDelimiter(text string, delimiter csvDelimiter) (score float64, ok bool) {
	maxRows := 100

	fieldCounts := []int{}
	parseErrors := 0

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = rune(delimiter[0])
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = false
	r.ReuseRecord = true

	rows := 0
	for rows < maxRows {
		row, err := r.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			parseErrors += 1
			if rows < 2 {
				return 0.0, false
			}
			if parseErrors >= 2 {
				break
			}
			continue
		}
		// skip empty lines
		if len(row) == 1 && row[0] == "" {
			continue
		}
		fieldCounts = append(fieldCounts, len(row))
		rows += 1
	}

	if rows < 2 {
		return 0.0, false
	}

	fieldAverage := average(fieldCounts)
	fieldMode, fieldFrequency := mode(fieldCounts)

	consistency := float64(fieldFrequency) / float64(rows)

	score = 0.0

	if fieldMode >= 2 {
		score += 0.5
	}

	score += 0.3 * consistency
	score += 0.1 * min(1, fieldAverage/3)

	return clamp01(score), true
}

func average(values []int) float64 {
	if len(values) == 0 {
		return 0.0
	}
	sum := 0
	for _, v := range values {
		sum += v
	}
	return float64(sum) / float64(len(values))
}

func mode(values []int) (mode int, frequency int) {
	counts := make(map[int]int)
	maxFreq := 0
	modeValue := 0

	for _, v := range values {
		counts[v]++
		if counts[v] > maxFreq {
			maxFreq = counts[v]
			modeValue = v
		}
	}

	return modeValue, maxFreq
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
