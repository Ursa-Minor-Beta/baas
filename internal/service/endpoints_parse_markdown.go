package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// @Schemes
// @Security Bearer
// @Description Parses documents (Excel: XLSX/XLSM/XLSB/XLS/XLTX/XLTM/XLT, Word: DOCX/DOC, PowerPoint: PPTX/PPT, CSV, TXT) and returns standard Markdown. For Excel files, each sheet becomes a separate section with ## heading.
// @Tags parse
// @Accept json
// @Produce json
// @Param data body dto.ReadabilityConfig true "parse config"
// @Success 200 {object} dto.ParseToMarkdownResult
// @Router /api/parse-to-markdown-kv [post]
func (s *Server) parseToMarkdownKvEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	if result, ok := service.WithReadBody(ctx, s, c, "Parse to Markdown", func(cfg *dto.ReadabilityConfig) (*dto.ParseToMarkdownResult, error) {
		cfg.MaxAttempts = lo.If(cfg.MaxAttempts != nil, cfg.MaxAttempts).Else(lo.ToPtr(1))
		cfg.Timeout = lo.If(cfg.Timeout == "", DefaultTimeout).Else(cfg.Timeout)
		configHash := cfg.Hash()
		ctx = s.Logger().WithValue(ctx, "configHash", configHash)
		ctx = s.Logger().WithValue(ctx, "config", cfg)

		res, err := retry.With(retry.Config[*dto.ParseToMarkdownResult]{
			Action: func() (*dto.ParseToMarkdownResult, error) {
				return s.doParseToMarkdownKv(ctx, cfg)
			},
			MaxRetries: lo.FromPtr(cfg.MaxAttempts),
			AttemptErrorCallback: func(i int, err error) {
				ctx := s.Logger().WithValue(ctx, "proxy", lo.FromPtr(cfg.UseProxy))
				ctx = s.Logger().WithValue(ctx, "attempt", i)
				ctx = s.Logger().WithValue(ctx, "error", err.Error())
				s.Logger().Errorf(ctx, "failed to process URL %q with proxy %q, attempt %d out of %d", cfg.URL, lo.FromPtr(cfg.UseProxy), i, lo.FromPtr(cfg.MaxAttempts))
				if lo.FromPtr(cfg.UseRandomProxy) {
					cfg.UseProxy = s.proxy.Host()
				}
			},
			NoMoreAttemptsCallback: func(err error) {
				s.Logger().Errorf(ctx, "failed to process URL %q, no more attempts left", cfg.URL)
			},
		})
		if err != nil {
			return nil, err
		}
		return lo.FromPtr(res), nil
	}); ok && result != nil {
		keepErr := result.Meta.Error
		result.Meta = s.GetMeta(ctx)
		result.Meta.Error = keepErr
		c.JSON(http.StatusOK, result)
	}
	return nil
}

// convertFunc defines the signature for format conversion functions
type convertFunc func(context.Context, io.ReadCloser) (string, error)

func (s *Server) doParseToMarkdownKv(ctx context.Context, cfg *dto.ReadabilityConfig) (*dto.ParseToMarkdownResult, error) {
	// Determine format type
	docFmt, respBody, err := determineFmtType(ctx, cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to determine document format")
	}

	// Map of conversion functions by format
	convertFunctions := map[fmtType]convertFunc{
		fmtXls:   s.convertXlsToMarkdown,
		fmtExcel: s.convertXlsxToMarkdown,
		fmtDoc:   s.convertDocToMarkdown,
		fmtDocx:  s.convertDocxToMarkdown,
		fmtPpt:   s.convertPptToMarkdown,
		fmtPptx:  s.convertPptxToMarkdown,
		fmtCsv: func(ctx context.Context, reader io.ReadCloser) (string, error) {
			return s.convertCsvToMarkdown(ctx, reader, cfg.CsvDelimiter)
		},
	}

	// Get converter for the format, default to text converter for plain text
	converter, ok := convertFunctions[*docFmt]
	if !ok {
		// Only fallback to text converter for actual text formats
		// Otherwise return error to avoid returning binary content as text
		converter = s.convertTxtToMarkdown
	}

	// Convert document to markdown
	markdown, err := converter(ctx, respBody)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert %s to markdown", *docFmt)
	}

	var usedBrowser bool
	var usedProxy string

	if lo.FromPtr(cfg.ForceUseBrowser) {
		usedBrowser = true
	}
	if lo.FromPtr(cfg.UseRandomProxy) || cfg.UseProxy != nil {
		usedProxy = lo.FromPtr(cfg.UseProxy)
	}

	return &dto.ParseToMarkdownResult{
		Markdown: markdown,
		ProcessResult: dto.ProcessResult{
			UsedBrowser: usedBrowser,
			UsedProxy:   usedProxy,
		},
	}, nil
}

// convertViaHtml is a helper for formats that need HTML intermediate step
func (s *Server) convertViaHtml(ctx context.Context, reader io.ReadCloser, format string, toHtml func(context.Context, []byte) (string, error)) (string, error) {
	defer reader.Close()
	docBytes := readBytes(reader)

	html, err := toHtml(ctx, docBytes)
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert %s to HTML", format)
	}

	// Use "gfm" (GitHub-flavored markdown) to convert tables to pipe syntax
	// instead of leaving them as raw HTML
	markdown, err := s.pandoc.HtmlToMarkdown(ctx, html, "gfm")
	if err != nil {
		return "", errors.Wrap(err, "failed to convert HTML to Markdown")
	}

	// Post-process: convert any remaining HTML tables to markdown
	markdown = convertHtmlTablesToMarkdown(markdown)

	return markdown, nil
}

func (s *Server) convertXlsToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	defer reader.Close()
	return s.pandoc.XlsToHtml(ctx, readBytes(reader))
}

func (s *Server) convertXlsxToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	defer reader.Close()
	return s.pandoc.ExcelToHtml(ctx, readBytes(reader))
}

func (s *Server) convertPptxToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	defer reader.Close()
	// Native pandoc PPTX reader returns markdown directly
	return s.pandoc.PptxToHtml(ctx, readBytes(reader))
}

func (s *Server) convertDocToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	return s.convertViaHtml(ctx, reader, "DOC", s.pandoc.DocToHtml)
}

func (s *Server) convertDocxToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	return s.convertViaHtml(ctx, reader, "DOCX", s.pandoc.DocxToHtml)
}

func (s *Server) convertPptToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	return s.convertViaHtml(ctx, reader, "PPT", s.pandoc.PptToHtml)
}

func (s *Server) convertCsvToMarkdown(ctx context.Context, reader io.ReadCloser, csvDelimiter *string) (string, error) {
	defer reader.Close()

	// Read CSV bytes
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, reader)
	if err != nil {
		return "", errors.Wrap(err, "failed to read CSV bytes")
	}

	// Parse CSV
	r := csv.NewReader(bytes.NewReader(buf.Bytes()))
	r.FieldsPerRecord = -1    // Allow variable number of fields
	r.LazyQuotes = true       // Handle bare quotes in CSV
	r.TrimLeadingSpace = true // Trim leading spaces
	// Set custom delimiter if provided
	if len(lo.FromPtr(csvDelimiter)) == 1 {
		r.Comma = rune(lo.FromPtr(csvDelimiter)[0])
	}
	records, err := r.ReadAll()
	if err != nil {
		return "", errors.Wrap(err, "failed to parse CSV")
	}

	if len(records) == 0 {
		return "", nil
	}

	// Preprocess records for markdown compatibility:
	// - Replace newlines with spaces (markdown tables don't support multi-line cells)
	// - Escape pipes: CRITICAL for correctness! Without escaping, markdown parsers
	//   interpret pipes in cell content as column separators, breaking the table structure.
	//   Example: "Data|with|pipes" → parser sees 3 columns instead of 1 cell with content.
	for i := range records {
		for j := range records[i] {
			// Replace newlines with spaces
			records[i][j] = strings.ReplaceAll(records[i][j], "\n", " ")
			records[i][j] = strings.ReplaceAll(records[i][j], "\r", " ")
			// Escape pipes: | → \| so markdown parsers treat them as literal characters
			records[i][j] = strings.ReplaceAll(records[i][j], "|", "\\|")
		}
	}

	// Generate Markdown table using tablewriter
	output := &bytes.Buffer{}
	table := tablewriter.NewWriter(output)

	// Configure for markdown format
	table.SetHeader(records[0])
	table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
	table.SetCenterSeparator("|")
	table.SetAutoFormatHeaders(false) // Preserve original header case
	table.AppendBulk(records[1:])
	table.Render()

	return output.String(), nil
}

func (s *Server) convertTxtToMarkdown(ctx context.Context, reader io.ReadCloser) (string, error) {
	defer reader.Close()

	// Read text bytes
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, reader)
	if err != nil {
		return "", errors.Wrap(err, "failed to read text bytes")
	}

	// Return as-is for plain text
	return buf.String(), nil
}

// convertHtmlTablesToMarkdown finds all HTML tables in markdown and converts them to pipe tables
func convertHtmlTablesToMarkdown(markdown string) string {
	// Parse the entire document using goquery
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(markdown))
	if err != nil {
		return markdown // Return original on parse error
	}

	// Find all tables and convert each to markdown
	doc.Find("table").Each(func(i int, table *goquery.Selection) {
		// Extract table rows
		var rows [][]string

		// Find every row within this table
		table.Find("tr").Each(func(i int, rowHtml *goquery.Selection) {
			var cells []string

			// Find every cell (header or data) within the row
			rowHtml.Find("th, td").Each(func(j int, cellHtml *goquery.Selection) {
				// Extract and clean the text
				cellText := strings.TrimSpace(cellHtml.Text())
				cells = append(cells, cellText)
			})

			// Only add non-empty rows
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
		})

		// If no rows extracted, skip this table
		if len(rows) == 0 {
			return
		}

		// Remove empty rows at the end
		for len(rows) > 0 && isEmptyRow(rows[len(rows)-1]) {
			rows = rows[:len(rows)-1]
		}

		if len(rows) == 0 {
			return
		}

		// Generate markdown table using tablewriter
		output := &bytes.Buffer{}
		markdownTable := tablewriter.NewWriter(output)
		markdownTable.SetHeader(rows[0])
		markdownTable.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
		markdownTable.SetCenterSeparator("|")
		markdownTable.SetAutoFormatHeaders(false)

		if len(rows) > 1 {
			markdownTable.AppendBulk(rows[1:])
		}
		markdownTable.Render()

		// Replace the HTML table with markdown table
		// Add newline before table and trim trailing newline to match markdown spacing
		table.ReplaceWithHtml("\n" + strings.TrimSuffix(output.String(), "\n"))
	})

	// Return the modified HTML with tables converted to markdown
	// Get only the body content without <html><head></head><body> wrappers
	bodyHtml, err := doc.Find("body").Html()
	if err != nil {
		return markdown
	}

	// Normalize multiple newlines (3+ in a row) to double newlines
	// This handles edge cases where table replacement creates extra spacing
	normalized := strings.ReplaceAll(bodyHtml, "\n\n\n", "\n\n")
	for strings.Contains(normalized, "\n\n\n") {
		normalized = strings.ReplaceAll(normalized, "\n\n\n", "\n\n")
	}

	// Unescape HTML entities (&amp; -> &, &lt; -> <, etc.)
	return html.UnescapeString(normalized)
}

// isEmptyRow checks if all cells in a row are empty
func isEmptyRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
