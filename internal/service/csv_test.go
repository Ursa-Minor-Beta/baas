package service

import (
	"os"
	"testing"

	. "github.com/onsi/gomega"
)

func Test_sniffCsvDelimiter(t *testing.T) {
	RegisterTestingT(t)

	tests := []struct {
		name      string
		filename  string
		wantDelim csvDelimiter
		isCsv     bool
	}{
		{
			name:      "comma delimited csv",
			filename:  "comma.csv",
			wantDelim: ",",
			isCsv:     true,
		},
		{
			name:      "semicolon delimited csv",
			filename:  "semicolon.csv",
			wantDelim: ";",
			isCsv:     true,
		},
		{
			name:     "plain text",
			filename: "plain.txt",
			isCsv:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputData, err := os.ReadFile("./test_data/" + tt.filename)
			Expect(err).To(BeNil())

			delimiter, score, ok := sniffCsvDelimiter(string(inputData))
			Expect(ok).To(Equal(tt.isCsv), "Expected isCsv=%v but got %v (score: %f)", tt.isCsv, ok, score)
			if tt.isCsv {
				Expect(delimiter).To(Equal(tt.wantDelim), "Expected delimiter %q but got %q (score: %f)", tt.wantDelim, delimiter, score)
			}
		})
	}
}
