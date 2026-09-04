package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// mockPandoc is a mock implementation of the Pandoc interface for testing
type mockPandoc struct {
	htmlToMarkdownFunc func(ctx context.Context, html string, format string) (string, error)
	anythingToHtmlFunc func(ctx context.Context, bytes []byte, docFmt string, embedResources bool) (string, error)
	docToHtmlFunc      func(ctx context.Context, docBytes []byte) (string, error)
	docxToHtmlFunc     func(ctx context.Context, docxBytes []byte) (string, error)
	pptToHtmlFunc      func(ctx context.Context, pptBytes []byte) (string, error)
	excelToHtmlFunc    func(ctx context.Context, excelBytes []byte) (string, error)
	xlsToHtmlFunc      func(ctx context.Context, xlsBytes []byte) (string, error)
}

func (m *mockPandoc) PdfToHtml(ctx context.Context, pdfBytes []byte) (string, error) {
	return "", nil
}

func (m *mockPandoc) DocToText(ctx context.Context, docxBytes []byte) (string, error) {
	return "", nil
}

func (m *mockPandoc) DocToHtml(ctx context.Context, docBytes []byte) (string, error) {
	if m.docToHtmlFunc != nil {
		return m.docToHtmlFunc(ctx, docBytes)
	}
	return "<html><body>Mock HTML</body></html>", nil
}

func (m *mockPandoc) DocToPdf(ctx context.Context, docBytes []byte, docFmt string) ([]byte, error) {
	return nil, nil
}

func (m *mockPandoc) HtmlToPdf(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) ([]byte, error) {
	return nil, nil
}

func (m *mockPandoc) HtmlToDocx(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) ([]byte, error) {
	return nil, nil
}

func (m *mockPandoc) DocxToText(ctx context.Context, docxBytes []byte) (string, error) {
	return "", nil
}

func (m *mockPandoc) AnythingToHtml(ctx context.Context, anythingBytes []byte, docFmt string, embedResources bool) (string, error) {
	if m.anythingToHtmlFunc != nil {
		return m.anythingToHtmlFunc(ctx, anythingBytes, docFmt, embedResources)
	}
	return "<html><body>Mock HTML</body></html>", nil
}

func (m *mockPandoc) XlsToXlsx(ctx context.Context, xlsBytes []byte) ([]byte, error) {
	return xlsBytes, nil
}

func (m *mockPandoc) DocxToHtml(ctx context.Context, docxBytes []byte) (string, error) {
	if m.docxToHtmlFunc != nil {
		return m.docxToHtmlFunc(ctx, docxBytes)
	}
	return "<html><body>Mock HTML</body></html>", nil
}

func (m *mockPandoc) ExcelToHtml(ctx context.Context, excelBytes []byte) (string, error) {
	if m.excelToHtmlFunc != nil {
		return m.excelToHtmlFunc(ctx, excelBytes)
	}
	// Default: return markdown pipe table
	return "| A | B |\n| --- | --- |\n| 1 | 2 |", nil
}

func (m *mockPandoc) XlsToHtml(ctx context.Context, xlsBytes []byte) (string, error) {
	if m.xlsToHtmlFunc != nil {
		return m.xlsToHtmlFunc(ctx, xlsBytes)
	}
	// Default: return markdown pipe table
	return "| A | B |\n| --- | --- |\n| 1 | 2 |", nil
}

func (m *mockPandoc) PptxToHtml(ctx context.Context, pptxBytes []byte) (string, error) {
	// Native pandoc PPTX reader returns markdown directly
	return "# Slide 1\n\nSlide content", nil
}

func (m *mockPandoc) PptToHtml(ctx context.Context, pptBytes []byte) (string, error) {
	if m.pptToHtmlFunc != nil {
		return m.pptToHtmlFunc(ctx, pptBytes)
	}
	return "<html><body>Mock HTML</body></html>", nil
}

func (m *mockPandoc) HtmlToMarkdown(ctx context.Context, html string, format string) (string, error) {
	if m.htmlToMarkdownFunc != nil {
		return m.htmlToMarkdownFunc(ctx, html, format)
	}
	// Default mock implementation - convert simple HTML to markdown
	md := strings.ReplaceAll(html, "<h1>", "# ")
	md = strings.ReplaceAll(md, "</h1>", "")
	md = strings.ReplaceAll(md, "<p>", "")
	md = strings.ReplaceAll(md, "</p>", "\n")
	md = strings.ReplaceAll(md, "<body>", "")
	md = strings.ReplaceAll(md, "</body>", "")
	md = strings.ReplaceAll(md, "<html>", "")
	md = strings.ReplaceAll(md, "</html>", "")
	return strings.TrimSpace(md), nil
}

func (m *mockPandoc) OdtToHtml(_ context.Context, _ []byte) (string, error) { return "", nil }
func (m *mockPandoc) OdsToHtml(_ context.Context, _ []byte) (string, error) { return "", nil }
func (m *mockPandoc) OdpToHtml(_ context.Context, _ []byte) (string, error) { return "", nil }
func (m *mockPandoc) RtfToHtml(_ context.Context, _ []byte) (string, error) { return "", nil }

// Test convertXlsxToMarkdown
func TestServer_convertXlsxToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name          string
		input         string
		mockExcelToMd func(ctx context.Context, excelBytes []byte) (string, error)
		expected      string
		expectError   bool
	}

	tests := []test{
		{
			name:  "simple excel conversion",
			input: "mock excel data",
			mockExcelToMd: func(ctx context.Context, excelBytes []byte) (string, error) {
				return "| A | B |\n| --- | --- |", nil
			},
			expected:    "| A | B |\n| --- | --- |",
			expectError: false,
		},
		{
			name:  "excel with multiple sheets",
			input: "mock excel data with sheets",
			mockExcelToMd: func(ctx context.Context, excelBytes []byte) (string, error) {
				return "# Sheet1\n\n| A |\n| --- |\n\n# Sheet2\n\n| X |\n| --- |", nil
			},
			expected:    "# Sheet1\n\n| A |\n| --- |\n\n# Sheet2\n\n| X |\n| --- |",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{
				pandoc: &mockPandoc{
					excelToHtmlFunc: tc.mockExcelToMd,
				},
			}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertXlsxToMarkdown(ctx, reader)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Test convertPptxToMarkdown
func TestServer_convertPptxToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name        string
		input       string
		expected    string
		expectError bool
	}

	tests := []test{
		{
			name:        "simple pptx conversion with native pandoc reader",
			input:       "mock pptx data",
			expected:    "# Slide 1\n\nSlide content",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{
				pandoc: &mockPandoc{},
			}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertPptxToMarkdown(ctx, reader)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Test convertDocxToMarkdown
func TestServer_convertDocxToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name         string
		input        string
		mockDocxTo   func(ctx context.Context, docxBytes []byte) (string, error)
		mockHtmlToMd func(ctx context.Context, html string, format string) (string, error)
		expected     string
		expectError  bool
	}

	tests := []test{
		{
			name:  "simple docx conversion",
			input: "mock docx data",
			mockDocxTo: func(ctx context.Context, docxBytes []byte) (string, error) {
				return "<html><body><h1>Title</h1><p>Paragraph</p></body></html>", nil
			},
			mockHtmlToMd: func(ctx context.Context, html string, format string) (string, error) {
				return "# Title\n\nParagraph", nil
			},
			expected:    "# Title\n\nParagraph",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{
				pandoc: &mockPandoc{
					docxToHtmlFunc:     tc.mockDocxTo,
					htmlToMarkdownFunc: tc.mockHtmlToMd,
				},
			}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertDocxToMarkdown(ctx, reader)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Test convertDocToMarkdown
func TestServer_convertDocToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name         string
		input        string
		mockDocTo    func(ctx context.Context, docBytes []byte) (string, error)
		mockHtmlToMd func(ctx context.Context, html string, format string) (string, error)
		expected     string
		expectError  bool
	}

	tests := []test{
		{
			name:  "simple doc conversion",
			input: "mock doc data",
			mockDocTo: func(ctx context.Context, docBytes []byte) (string, error) {
				return "<html><body><h1>Title</h1><p>Paragraph</p></body></html>", nil
			},
			mockHtmlToMd: func(ctx context.Context, html string, format string) (string, error) {
				return "# Title\n\nParagraph", nil
			},
			expected:    "# Title\n\nParagraph",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{
				pandoc: &mockPandoc{
					docToHtmlFunc:      tc.mockDocTo,
					htmlToMarkdownFunc: tc.mockHtmlToMd,
				},
			}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertDocToMarkdown(ctx, reader)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Test convertPptToMarkdown
func TestServer_convertPptToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name         string
		input        string
		mockPptTo    func(ctx context.Context, pptBytes []byte) (string, error)
		mockHtmlToMd func(ctx context.Context, html string, format string) (string, error)
		expected     string
		expectError  bool
	}

	tests := []test{
		{
			name:  "simple ppt conversion",
			input: "mock ppt data",
			mockPptTo: func(ctx context.Context, pptBytes []byte) (string, error) {
				return "<html><body><h1>Slide 1</h1><p>Content</p></body></html>", nil
			},
			mockHtmlToMd: func(ctx context.Context, html string, format string) (string, error) {
				return "# Slide 1\n\nContent", nil
			},
			expected:    "# Slide 1\n\nContent",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{
				pandoc: &mockPandoc{
					pptToHtmlFunc:      tc.mockPptTo,
					htmlToMarkdownFunc: tc.mockHtmlToMd,
				},
			}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertPptToMarkdown(ctx, reader)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Test convertCsvToMarkdown
func TestServer_convertCsvToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name        string
		input       string
		expected    string
		expectError bool
	}

	tests := []test{
		{
			name:  "simple csv with headers",
			input: "Name,Age,City\nJohn,30,NYC\nJane,25,LA",
			expected: "| Name | Age | City |\n" +
				"|------|-----|------|\n" +
				"| John |  30 | NYC  |\n" +
				"| Jane |  25 | LA   |\n",
			expectError: false,
		},
		{
			name:  "csv with pipes",
			input: "Name,Data\nTest,Data|with|pipes",
			expected: "| Name |       Data        |\n" +
				"|------|-------------------|\n" +
				"| Test | Data\\|with\\|pipes |\n",
			expectError: false,
		},
		{
			name:  "csv with newlines in cells",
			input: "Name,Data\nTest,\"Data\nwith\nnewlines\"",
			expected: "| Name |        Data        |\n" +
				"|------|--------------------|" + "\n" +
				"| Test | Data with newlines |\n",
			expectError: false,
		},
		{
			name:        "empty csv",
			input:       "",
			expected:    "",
			expectError: false,
		},
		{
			name:  "csv with single column",
			input: "Header\nValue1\nValue2",
			expected: "| Header |\n" +
				"|--------|\n" +
				"| Value1 |\n" +
				"| Value2 |\n",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertCsvToMarkdown(ctx, reader, nil)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Test convertTxtToMarkdown
func TestServer_convertTxtToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name        string
		input       string
		expected    string
		expectError bool
	}

	tests := []test{
		{
			name:        "simple text",
			input:       "This is plain text content",
			expected:    "This is plain text content",
			expectError: false,
		},
		{
			name:        "text with newlines",
			input:       "Line 1\nLine 2\nLine 3",
			expected:    "Line 1\nLine 2\nLine 3",
			expectError: false,
		},
		{
			name:        "empty text",
			input:       "",
			expected:    "",
			expectError: false,
		},
		{
			name:        "text with special characters",
			input:       "Text with | pipes and # hashes",
			expected:    "Text with | pipes and # hashes",
			expectError: false,
		},
	}

	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			server := &Server{}

			reader := io.NopCloser(strings.NewReader(tc.input))
			result, err := server.convertTxtToMarkdown(ctx, reader)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).To(Equal(tc.expected))
			}
		})
	}
}

// Integration tests for doParseToMarkdownKv
func TestServer_doParseToMarkdownKv_Integration(t *testing.T) {
	RegisterTestingT(t)

	// Create a test HTTP server that serves CSV content
	csvServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Name,Age,City\nJohn,30,NYC\nJane,25,LA"))
	}))
	defer csvServer.Close()

	type test struct {
		name        string
		config      *dto.ReadabilityConfig
		server      *httptest.Server
		expected    string
		expectError bool
	}

	tests := []test{
		{
			name: "csv file integration",
			config: &dto.ReadabilityConfig{
				Timeout: "30s",
			},
			server: csvServer,
			expected: "| Name | Age | City |\n" +
				"|------|-----|------|\n" +
				"| John |  30 | NYC  |\n" +
				"| Jane |  25 | LA   |\n",
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Set the URL to the test server
			tc.config.URL = tc.server.URL
			tc.config.MaxAttempts = lo.ToPtr(1)

			server := &Server{
				pandoc: &mockPandoc{},
			}

			result, err := server.doParseToMarkdownKv(ctx, tc.config)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).NotTo(BeNil())
				Expect(result.Markdown).To(Equal(tc.expected))
			}
		})
	}
}

// Integration test for retry mechanism with CSV
// Note: This test is skipped because retry mechanism is tested at a higher level
func TestServer_doParseToMarkdownKv_RetryMechanism(t *testing.T) {
	t.Skip("Retry mechanism is complex to test at this level due to internal FetchFromURL behavior")
	RegisterTestingT(t)

	attemptCount := 0
	// Create a server that fails twice then succeeds
	flakeyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Name,Value\nTest,123"))
	}))
	defer flakeyServer.Close()

	t.Run("retry succeeds on third attempt", func(t *testing.T) {
		ctx := context.Background()
		cfg := &dto.ReadabilityConfig{
			URL:         flakeyServer.URL + "/data.csv", // Add .csv extension to URL
			Timeout:     "30s",
			MaxAttempts: lo.ToPtr(3),
		}

		server := &Server{
			pandoc: &mockPandoc{},
		}

		result, err := server.doParseToMarkdownKv(ctx, cfg)

		Expect(err).To(BeNil())
		Expect(result).NotTo(BeNil())
		Expect(result.Markdown).To(Equal("| Name | Value |\n|------|-------|\n| Test |   123 |\n"))
		Expect(attemptCount).To(Equal(3))
	})
}

// Test format detection with different MIME types
func TestServer_FormatDetection(t *testing.T) {
	RegisterTestingT(t)

	type testServer struct {
		contentType string
		filename    string
		content     []byte
	}

	type test struct {
		name        string
		server      testServer
		expectedMd  string
		expectError bool
	}

	tests := []test{
		{
			name: "detect CSV from content-type",
			server: testServer{
				contentType: "text/csv",
				filename:    "data.csv",
				content:     []byte("Col1,Col2\nA,B"),
			},
			expectedMd:  "| Col1 | Col2 |\n|------|------|\n| A    | B    |\n",
			expectError: false,
		},
		{
			name: "detect CSV from filename when content-type is text/plain",
			server: testServer{
				contentType: "text/plain",
				filename:    "test.csv",
				content:     []byte("X,Y\n1,2"),
			},
			expectedMd:  "| X | Y |\n|---|---|\n| 1 | 2 |\n",
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create test server for this test case
			testSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.server.contentType)
				if tc.server.filename != "" {
					w.Header().Set("Content-Disposition", "attachment; filename=\""+tc.server.filename+"\"")
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.server.content)
			}))
			defer testSrv.Close()

			ctx := context.Background()
			// Add filename to URL path so determineFmtType can detect it
			url := testSrv.URL
			if tc.server.filename != "" {
				url += "/" + tc.server.filename
			}
			cfg := &dto.ReadabilityConfig{
				URL:         url,
				Timeout:     "30s",
				MaxAttempts: lo.ToPtr(1),
			}

			server := &Server{
				pandoc: &mockPandoc{},
			}

			result, err := server.doParseToMarkdownKv(ctx, cfg)

			if tc.expectError {
				Expect(err).NotTo(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(result).NotTo(BeNil())
				Expect(result.Markdown).To(Equal(tc.expectedMd))
			}
		})
	}
}
