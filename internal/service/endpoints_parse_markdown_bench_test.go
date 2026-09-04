package service

import (
	"context"
	"io"
	"strings"
	"testing"
)

// Benchmark CSV conversion
func BenchmarkConvertCsvToMarkdown(b *testing.B) {
	server := &Server{}
	ctx := context.Background()

	// Small CSV (10 rows, 5 columns)
	smallCSV := "Col1,Col2,Col3,Col4,Col5\n"
	for i := 0; i < 10; i++ {
		smallCSV += "Value1,Value2,Value3,Value4,Value5\n"
	}

	b.Run("Small_CSV_10x5", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(smallCSV))
			_, err := server.convertCsvToMarkdown(ctx, reader, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Medium CSV (100 rows, 10 columns)
	mediumCSV := "C1,C2,C3,C4,C5,C6,C7,C8,C9,C10\n"
	for i := 0; i < 100; i++ {
		mediumCSV += "V1,V2,V3,V4,V5,V6,V7,V8,V9,V10\n"
	}

	b.Run("Medium_CSV_100x10", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(mediumCSV))
			_, err := server.convertCsvToMarkdown(ctx, reader, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Large CSV (1000 rows, 20 columns)
	largeCSVHeader := ""
	largeCSVRow := ""
	for col := 1; col <= 20; col++ {
		if col > 1 {
			largeCSVHeader += ","
			largeCSVRow += ","
		}
		largeCSVHeader += "Column" + string(rune('0'+col))
		largeCSVRow += "Value" + string(rune('0'+col))
	}
	largeCSVHeader += "\n"
	largeCSVRow += "\n"

	largeCSV := largeCSVHeader
	for i := 0; i < 1000; i++ {
		largeCSV += largeCSVRow
	}

	b.Run("Large_CSV_1000x20", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(largeCSV))
			_, err := server.convertCsvToMarkdown(ctx, reader, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Benchmark TXT conversion
func BenchmarkConvertTxtToMarkdown(b *testing.B) {
	server := &Server{}
	ctx := context.Background()

	// Small text (1KB)
	smallText := strings.Repeat("This is a line of text.\n", 50)

	b.Run("Small_TXT_1KB", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(smallText))
			_, err := server.convertTxtToMarkdown(ctx, reader)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Medium text (100KB)
	mediumText := strings.Repeat("This is a line of text with some more content to make it longer.\n", 1500)

	b.Run("Medium_TXT_100KB", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(mediumText))
			_, err := server.convertTxtToMarkdown(ctx, reader)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Large text (1MB)
	largeText := strings.Repeat("This is a line of text with substantial content to simulate real-world text files.\n", 12500)

	b.Run("Large_TXT_1MB", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(largeText))
			_, err := server.convertTxtToMarkdown(ctx, reader)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Benchmark with mock pandoc for office formats
func BenchmarkConvertXlsxToMarkdown(b *testing.B) {
	ctx := context.Background()

	// Create mock pandoc that returns simple HTML
	mockPandoc := &mockPandoc{
		anythingToHtmlFunc: func(ctx context.Context, bytes []byte, docFmt string, embedResources bool) (string, error) {
			// Simulate pandoc HTML output
			return "<html><body><table><tr><td>Col1</td><td>Col2</td></tr><tr><td>Val1</td><td>Val2</td></tr></table></body></html>", nil
		},
		htmlToMarkdownFunc: func(ctx context.Context, html string, format string) (string, error) {
			// Simulate pandoc markdown output
			return "| Col1 | Col2 |\n| --- | --- |\n| Val1 | Val2 |\n", nil
		},
	}

	server := &Server{
		pandoc: mockPandoc,
	}

	// Simulate XLSX bytes (just dummy data for benchmark)
	xlsxData := strings.Repeat("mock xlsx binary data ", 1000)

	b.Run("XLSX_With_Mock_Pandoc", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(xlsxData))
			_, err := server.convertXlsxToMarkdown(ctx, reader)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkConvertPptxToMarkdown(b *testing.B) {
	ctx := context.Background()

	mockPandoc := &mockPandoc{
		anythingToHtmlFunc: func(ctx context.Context, bytes []byte, docFmt string, embedResources bool) (string, error) {
			return "<html><body><h1>Slide 1</h1><p>Content</p></body></html>", nil
		},
		htmlToMarkdownFunc: func(ctx context.Context, html string, format string) (string, error) {
			return "# Slide 1\n\nContent\n", nil
		},
	}

	server := &Server{
		pandoc: mockPandoc,
	}

	pptxData := strings.Repeat("mock pptx binary data ", 1000)

	b.Run("PPTX_With_Mock_Pandoc", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(pptxData))
			_, err := server.convertPptxToMarkdown(ctx, reader)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkConvertDocxToMarkdown(b *testing.B) {
	ctx := context.Background()

	mockPandoc := &mockPandoc{
		docxToHtmlFunc: func(ctx context.Context, docxBytes []byte) (string, error) {
			return "<html><body><h1>Title</h1><p>Paragraph</p></body></html>", nil
		},
		htmlToMarkdownFunc: func(ctx context.Context, html string, format string) (string, error) {
			return "# Title\n\nParagraph\n", nil
		},
	}

	server := &Server{
		pandoc: mockPandoc,
	}

	docxData := strings.Repeat("mock docx binary data ", 1000)

	b.Run("DOCX_With_Mock_Pandoc", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			reader := io.NopCloser(strings.NewReader(docxData))
			_, err := server.convertDocxToMarkdown(ctx, reader)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
