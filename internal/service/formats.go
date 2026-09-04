package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/ledongthuc/pdf"
	"github.com/pkg/errors"
	"github.com/robertkrimen/otto"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// nolint: unused
type fmtType string

const (
	fmtPdf   fmtType = "pdf"   //nolint: unused
	fmtExcel fmtType = "xlsx"  //nolint: unused
	fmtXls   fmtType = "xls"   //nolint: unused
	fmtCsv   fmtType = "csv"   //nolint: unused
	fmtPpt   fmtType = "ppt"   //nolint: unused
	fmtPptx  fmtType = "pptx"  //nolint: unused
	fmtDocx  fmtType = "docx"  //nolint: unused
	fmtDoc   fmtType = "doc"   //nolint: unused
	fmtOdt   fmtType = "odt"   //nolint: unused
	fmtOds   fmtType = "ods"   //nolint: unused
	fmtOdp   fmtType = "odp"   //nolint: unused
	fmtRtf   fmtType = "rtf"   //nolint: unused
	fmtTsv   fmtType = "tsv"   //nolint: unused
	fmtRst   fmtType = "rst"   //nolint: unused
	fmtPlain fmtType = "plain" //nolint: unused
)

var supportedTextMimeTypes = []string{
	"text/plain",
	"text/html",
}

// fmtTypeByExtension maps a URL file extension to a document format. Only
// extensions that map to a dedicated converter are listed; anything else falls
// through to content-type detection.
var fmtTypeByExtension = map[string]fmtType{
	".csv":  fmtCsv,
	".tsv":  fmtTsv,
	".doc":  fmtDoc,
	".docx": fmtDocx,
	".pdf":  fmtPdf,
	".ppt":  fmtPpt,
	".pptx": fmtPptx,
	".xls":  fmtXls,
	".xlsx": fmtExcel,
}

// fmtTypeFromURL guesses a document format from the file extension of rawURL,
// returning nil when the URL is unparseable or the extension is unknown.
func fmtTypeFromURL(rawURL string) *fmtType {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	if f, ok := fmtTypeByExtension[strings.ToLower(path.Ext(parsed.Path))]; ok {
		return &f
	}
	return nil
}

func determineFmtType(ctx context.Context, cfg *dto.ReadabilityConfig) (*fmtType, io.ReadCloser, error) {
	contentType, resp, err := DetermineMimeType(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	respBody := resp.Body

	if (contentType == "" || IsOctetStream(contentType)) && resp != nil {
		data, err := io.ReadAll(respBody)
		respBody = io.NopCloser(bytes.NewReader(data))
		if err != nil {
			contentType = http.DetectContentType(data)
		}
	}

	// A generic content type carries no format information, so fall back to the
	// extension in the URL. Without this, a .csv served as text/plain reaches
	// the plain-text converter and is returned verbatim.
	if IsPlainText(contentType) || contentType == "" || IsOctetStream(contentType) {
		if guessed := fmtTypeFromURL(cfg.URL); guessed != nil {
			return guessed, respBody, nil
		}
	}

	switch {
	case IsDoc(contentType):
		return lo.ToPtr(fmtDoc), respBody, nil
	case IsDocX(contentType):
		return lo.ToPtr(fmtDocx), respBody, nil
	case IsPdf(contentType):
		return lo.ToPtr(fmtPdf), respBody, nil
	case IsXlsx(contentType):
		return lo.ToPtr(fmtExcel), respBody, nil
	case IsXls(contentType):
		return lo.ToPtr(fmtXls), respBody, nil
	case IsCsv(contentType):
		return lo.ToPtr(fmtCsv), respBody, nil
	case IsPptx(contentType):
		return lo.ToPtr(fmtPptx), respBody, nil
	case IsPpt(contentType):
		return lo.ToPtr(fmtPpt), respBody, nil
	case IsPlainText(contentType):
		return lo.ToPtr(fmtPlain), respBody, nil
	}

	return nil, nil, errors.Errorf("unknown document format for content-type: %q", contentType)
}

func DetermineMimeType(ctx context.Context, cfg *dto.ReadabilityConfig) (string, *http.Response, error) {
	log := logger.NewLogger()
	resp, err := FetchFromURL(ctx, cfg)
	if cfg.ForceMimeType != nil {
		return *cfg.ForceMimeType, resp, nil
	}
	if err != nil || strings.HasSuffix(resp.Header.Get("Content-Type"), "/octet-stream") {
		log.Errorf(ctx, "failed to fetch URL: %v, trying to guess it from extension...", err)
		// Try Content-Disposition header first (e.g. SharePoint download.aspx URLs)
		if resp != nil {
			if mimeType, cdErr := guessMimeTypeFromContentDisposition(resp.Header.Get("Content-Disposition")); cdErr == nil {
				return mimeType, resp, nil
			}
		}
		mimeType, err := guessMimeTypeFromURL(cfg.URL)
		if err != nil {
			return "", resp, errors.Wrapf(err, "failed to guess mime type by URL")
		}
		return mimeType, resp, nil
	}
	return resp.Header.Get("Content-Type"), resp, nil
}

func guessMimeTypeFromContentDisposition(contentDisposition string) (string, error) {
	if contentDisposition == "" {
		return "", errors.New("empty Content-Disposition header")
	}
	_, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse Content-Disposition header")
	}
	filename, ok := params["filename"]
	if !ok || filename == "" {
		return "", errors.New("no filename in Content-Disposition header")
	}
	ext := path.Ext(filename)
	guessedType := mime.TypeByExtension(ext)
	if guessedType == "" {
		return "", errors.Errorf("failed to guess MIME type from Content-Disposition filename %q: empty", filename)
	}
	return guessedType, nil
}

func (s *Server) parseDocument(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser, docFmt fmtType) (*dto.ReadabilityResult, error) {
	switch docFmt {
	case fmtPlain:
		return s.parsePlainText(ctx, cfg, resp)
	case fmtPdf:
		return s.parsePdf(ctx, cfg, resp)
	case fmtCsv:
		return s.parseCsv(ctx, cfg, resp)
	case fmtDocx, fmtDoc, fmtExcel, fmtXls, fmtPptx, fmtPpt:
		return s.parseMsOffice(ctx, cfg, resp, docFmt)
	}
	return nil, errors.Errorf("unknown document format: %q", docFmt)
}

func (s *Server) parseTsv(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser) (*dto.ReadabilityResult, error) {
	return s.parseSeparatedValues(ctx, cfg, resp, '\t')
}

func (s *Server) parsePlainText(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser) (*dto.ReadabilityResult, error) {
	buff := bytes.NewBuffer([]byte{})
	defer func() {
		_ = resp.Close()
	}()
	_, err := io.Copy(buff, resp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read plain text file from %q", cfg.URL)
	}

	plainText := buff.String()

	if cfg.ForceMimeType == nil {
		csvDelimiter, csvScore, csvOk := sniffCsvDelimiter(plainText)
		if csvOk {
			ctx = s.Logger().WithValue(ctx, "sniffedCsvDelimiter", csvDelimiter)
			ctx = s.Logger().WithValue(ctx, "sniffedCsvScore", csvScore)
			s.Logger().Infof(ctx, "sniffed csv delimiter %q with score %.2f, parsing as CSV", csvDelimiter, csvScore)
			cfgClone := *cfg
			cfgClone.CsvDelimiter = lo.ToPtr(string(csvDelimiter))
			return s.parseCsv(ctx, &cfgClone, io.NopCloser(bytes.NewReader(buff.Bytes())))
		}
	}

	article, err := ParseReadabilityContent(strings.NewReader(plainText), cfg.URL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse plain text content for PDF from %q", cfg.URL)
	}
	articleResult := s.articleToResult(ctx, cfg, []byte(plainText), dto.ToArticle(*article), false)
	textContent := buff.String()
	articleResult.Article.TextContent = textContent
	articleResult.Article.Length = len(textContent)

	return articleResult, nil
}

func (s *Server) parseCsv(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser) (*dto.ReadabilityResult, error) {
	return s.parseSeparatedValues(ctx, cfg, resp, ',')
}

func (s *Server) parseSeparatedValues(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser, delimiter rune) (*dto.ReadabilityResult, error) {
	buff := bytes.NewBuffer([]byte{})
	defer func() {
		_ = resp.Close()
	}()
	_, err := io.Copy(buff, resp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read csv/tsv file from %q", cfg.URL)
	}
	var plainText string
	var tables []*dto.ReadabilityTable

	buffBytes := buff.Bytes()

	r := csv.NewReader(bytes.NewReader(buffBytes))
	r.FieldsPerRecord = -1 // allow variable number of fields per record
	if len(lo.FromPtr(cfg.CsvDelimiter)) == 1 {
		r.Comma = rune(lo.FromPtr(cfg.CsvDelimiter)[0])
	} else {
		csvDelimiter, csvScore, _ := sniffCsvDelimiter(string(buffBytes))
		ctx := s.Logger().WithValue(ctx, "sniffedCsvDelimiter", csvDelimiter)
		ctx = s.Logger().WithValue(ctx, "sniffedCsvScore", csvScore)
		s.Logger().Infof(ctx, "sniffed csv delimiter %q with score %.2f", csvDelimiter, csvScore)
		r.Comma = rune(csvDelimiter[0])
	}
	records, err := r.ReadAll()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read csv data from %q", cfg.URL)
	}

	plainTextBuf := &bytes.Buffer{}
	plainTextBuf.WriteString("<table>\n")

	table := dto.ReadabilityTable{Name: "CSV Data"}
	rows := make([][]any, len(records)-1)
	table.Headers = records[0]
	for i, record := range records {
		if i == 0 {
			table.Headers = record
			plainTextBuf.WriteString("  <tr>\n")
			for _, header := range record {
				plainTextBuf.WriteString("    <th>" + header + "</th>\n")
			}
			plainTextBuf.WriteString("  </tr>\n")
			continue
		}
		plainTextBuf.WriteString("  <tr>\n")
		row := make([]any, len(record))
		for j, cell := range record {
			plainTextBuf.WriteString("    <td>" + cell + "</td>\n")
			row[j] = cell
		}
		plainTextBuf.WriteString("  </tr>\n")
		rows[i-1] = row
	}
	plainTextBuf.WriteString("</table>\n")
	plainText = plainTextBuf.String()
	table.Rows = rows
	tables = append(tables, &table)

	article, err := ParseReadabilityContent(strings.NewReader(plainText), cfg.URL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse plain text content for PDF from %q", cfg.URL)
	}
	articleResult := s.articleToResult(ctx, cfg, []byte(plainText), dto.ToArticle(*article), false)
	if lo.FromPtr(cfg.ParseTableData) {
		articleResult.Tables = tables
	}
	textContent := buff.String()
	articleResult.Article.TextContent = textContent
	articleResult.Article.Length = len(textContent)

	return articleResult, nil
}

func (s *Server) parseMsOffice(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser, docFmt fmtType) (*dto.ReadabilityResult, error) {
	buff := bytes.NewBuffer([]byte{})
	defer func() {
		_ = resp.Close()
	}()
	_, err := io.Copy(buff, resp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s file from %q", docFmt, cfg.URL)
	}
	var plainText string
	var tables []*dto.ReadabilityTable
	switch docFmt {
	case fmtDoc:
		plainText, err = s.pandoc.DocToHtml(ctx, buff.Bytes())
		if err != nil {
			plainText, err = s.pandoc.DocToText(ctx, buff.Bytes())
			if err != nil {
				return nil, errors.Wrapf(err, "failed to read plain text from doc at %q", cfg.URL)
			}
		}
	case fmtPptx:
		plainText, err = s.pandoc.AnythingToHtml(ctx, buff.Bytes(), string(docFmt), lo.FromPtr(cfg.EmbedResources))
		if err != nil {
			// TODO: remove fallback once PPTX parsing issues are fixed in aipandoc fork
			plainText, err = s.pandoc.PptxToHtml(ctx, buff.Bytes())
			if err != nil {
				return nil, errors.Wrapf(err, "failed to read plain text from pptx at %q", cfg.URL)
			}
		}
	case fmtPpt:
		plainText, err = s.pandoc.PptToHtml(ctx, buff.Bytes())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from ppt at %q", cfg.URL)
		}
	case fmtDocx, fmtExcel:
		plainText, err = s.pandoc.AnythingToHtml(ctx, buff.Bytes(), string(docFmt), lo.FromPtr(cfg.EmbedResources))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from %s at %q", docFmt, cfg.URL)
		}
		if docFmt == fmtExcel || docFmt == fmtXls {
			// TODO: results are inconsistent with Pandoc output when calculated formulas are present.
			tables, err = s.parseXlsxTableData(ctx, cfg, buff)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to parse table data from excel at %q", cfg.URL)
			}
		}
	case fmtOdt:
		plainText, err = s.pandoc.OdtToHtml(ctx, buff.Bytes())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from odt at %q", cfg.URL)
		}
	case fmtOds:
		plainText, err = s.pandoc.OdsToHtml(ctx, buff.Bytes())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from ods at %q", cfg.URL)
		}
	case fmtOdp:
		plainText, err = s.pandoc.OdpToHtml(ctx, buff.Bytes())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from odp at %q", cfg.URL)
		}
	case fmtRtf:
		plainText, err = s.pandoc.RtfToHtml(ctx, buff.Bytes())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from rtf at %q", cfg.URL)
		}
	case fmtRst:
		plainText, err = s.pandoc.AnythingToHtml(ctx, buff.Bytes(), "rst", false)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read plain text from rst at %q", cfg.URL)
		}
	case fmtXls:
		xlsxBytes, err := s.pandoc.XlsToXlsx(ctx, buff.Bytes())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to convert xls to xlsx at %q", cfg.URL)
		}
		return s.parseMsOffice(ctx, cfg, io.NopCloser(bytes.NewReader(xlsxBytes)), fmtExcel)
	default:
		return nil, errors.Errorf("failed to convert MS Office document to text: mimeType mismatch")
	}

	article, err := ParseReadabilityContent(strings.NewReader(plainText), cfg.URL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse plain text content for PDF from %q", cfg.URL)
	}
	articleResult := s.articleToResult(ctx, cfg, []byte(plainText), dto.ToArticle(*article), false)
	if lo.FromPtr(cfg.ParseTableData) {
		articleResult.Tables = tables
	}
	return articleResult, nil
}

func (s Server) parseXlsxTableData(ctx context.Context, cfg *dto.ReadabilityConfig, xlsxBuffer *bytes.Buffer) ([]*dto.ReadabilityTable, error) {
	var tables []*dto.ReadabilityTable
	parsed, err := s.pdfParser.Parse(ctx, xlsxBuffer.Bytes(), "xlsx") // pdfParser is a legacy name, it can parse XLSX as well
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse table data from excel at %q", cfg.URL)
	}
	for _, block := range parsed.Blocks {
		if block.Type == "data" && block.Content != nil {
			tableJson, err := json.Marshal(block.Content)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to marshal table data from excel at %q", cfg.URL)
			}
			table := dto.ReadabilityTable{Name: block.Name}
			if err := json.Unmarshal(tableJson, &table); err != nil {
				return nil, errors.Wrapf(err, "failed to unmarshal table data from excel at %q", cfg.URL)
			}
			tables = append(tables, &table)
		}
	}
	return tables, nil
}

func trimJunkAtPdfStart(data []byte) ([]byte, error) {
	idx := bytes.Index(data, []byte("%PDF-"))
	if idx == -1 {
		return nil, errors.Errorf("not a PDF file: %q header not found", "%PDF-")
	}
	return data[idx:], nil
}

func trimJunkAtPdfEnd(data []byte) ([]byte, error) {
	eofMarker := []byte("%%EOF")
	idx := bytes.LastIndex(data, eofMarker)
	if idx == -1 {
		return nil, errors.Errorf("not a PDF file: missing %q", "%%EOF")
	}
	end := idx + len(eofMarker)
	return data[:end], nil
}

func trimJunkInPdf(data []byte) ([]byte, error) {
	data, err := trimJunkAtPdfStart(data)
	if err != nil {
		return nil, err
	}
	data, err = trimJunkAtPdfEnd(data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Server) parsePdf(ctx context.Context, cfg *dto.ReadabilityConfig, resp io.ReadCloser) (*dto.ReadabilityResult, error) {
	buff := bytes.NewBuffer([]byte{})
	defer func() {
		_ = resp.Close()
	}()
	_, err := io.Copy(buff, resp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read PDF file from %q", cfg.URL)
	}
	data, err := trimJunkInPdf(buff.Bytes())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to trim junk in PDF file from %q", cfg.URL)
	}

	plainText, err := s.pandoc.PdfToHtml(ctx, data)
	if err != nil {
		s.Logger().Errorf(ctx, "failed to convert PDF to text with pdftotext, falling back to embedded pdf reader")
		pdfReader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}

		totalPage := pdfReader.NumPage()
		textBuf := bytes.Buffer{}
		for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
			p := pdfReader.Page(pageIndex)
			if p.V.IsNull() {
				continue
			}
			columns, _ := p.GetTextByColumn()
			for _, col := range columns {
				for _, word := range col.Content {
					textBuf.WriteString(word.S)
				}
				textBuf.WriteString("\n")
			}
		}
		plainText = textBuf.String()
	}
	bufText := strings.NewReader(plainText)
	article, err := ParseReadabilityContent(bufText, cfg.URL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse plain text content for PDF from %q", cfg.URL)
	}
	return s.articleToResult(ctx, cfg, []byte(plainText), dto.ToArticle(*article), false), nil
}

func (s *Server) articleToResult(ctx context.Context, cfg *dto.ReadabilityConfig, rawContent []byte, article dto.Article, usedBrowser bool) *dto.ReadabilityResult {
	res := &dto.ReadabilityResult{
		ProcessResult: dto.ProcessResult{
			UsedBrowser: usedBrowser,
			UsedProxy:   lo.FromPtr(cfg.UseProxy),
		},
		Article: article,
	}
	if lo.FromPtr(cfg.ReturnFullHtml) {
		res.RawContent = lo.ToPtr(string(rawContent))
	}
	if cfg.ReturnPandoc != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()
		md, err := s.pandoc.HtmlToMarkdown(ctx, string(rawContent), lo.FromPtr(cfg.ReturnPandoc))
		if err != nil {
			ctx := s.Logger().WithValue(ctx, "error", err.Error())
			s.Logger().Errorf(ctx, "failed to convert html to markdown: %v", err)
			res.Meta.Error = lo.ToPtr(err.Error())
		} else {
			res.Pandoc = lo.ToPtr(md)
		}
	}
	ctx = s.Logger().WithValue(ctx, "resultMetadata", res.Meta)
	s.Logger().Infof(ctx, "Returning result")
	return res
}

var readabilitySupportedMimeTypeFuncs = []func(string) bool{
	IsPdf, IsDocxOrDoc, IsAnyText,
}

func IsReadabilityMimeType(mimeType string) bool {
	return lo.ContainsBy(readabilitySupportedMimeTypeFuncs, func(item func(string) bool) bool {
		return item(mimeType)
	})
}

func IsPdf(mimeType string) bool {
	return strings.HasPrefix(mimeType, "application/pdf")
}

func IsExcel(mimeType string) bool {
	// Covers all Excel formats: .xls, .xlsx, .xlsm, .xlsb, .xlt, .xltx, .xltm
	return IsXls(mimeType) || IsXlsx(mimeType)
}

func IsXls(mimeType string) bool {
	return strings.HasPrefix(mimeType, "application/vnd.ms-excel")
}

func IsXlsx(mimeType string) bool {
	// Covers: .xlsx, .xlsm, .xlsb, .xltx, .xltm
	return strings.Contains(mimeType, "spreadsheetml.sheet") ||
		strings.Contains(mimeType, "spreadsheetml.template") ||
		strings.Contains(mimeType, "excel.sheet.macroEnabled") ||
		strings.Contains(mimeType, "excel.sheet.binary") ||
		strings.Contains(mimeType, "excel.template.macroEnabled")
}

func IsCsv(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/csv") || strings.HasPrefix(mimeType, "application/csv")
}

func IsPptx(mimeType string) bool {
	return strings.Contains(mimeType, "presentationml.presentation")
}

func IsPpt(mimeType string) bool {
	return strings.HasPrefix(mimeType, "application/vnd.ms-powerpoint")
}

func IsSupportedFile(mimeType string) bool {
	return IsDocxOrDoc(mimeType) || IsPdf(mimeType) || IsXlsx(mimeType) || IsPptx(mimeType) || IsPpt(mimeType) || IsCsv(mimeType)
}

func IsAnyText(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/")
}

func IsPlainText(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/plain")
}

func IsDocX(mimeType string) bool {
	return strings.Contains(mimeType, "wordprocessingml.document")
}

func IsDoc(mimeType string) bool {
	return strings.HasPrefix(mimeType, "application/msword")
}

func IsDocxOrDoc(mimeType string) bool {
	return IsDocX(mimeType) || IsDoc(mimeType)
}

func IsOctetStream(mimeType string) bool {
	return strings.HasPrefix("application/octet-stream", mimeType)
}

func IsOdt(mimeType string) bool {
	return strings.Contains(mimeType, "opendocument.text")
}

func IsOds(mimeType string) bool {
	return strings.Contains(mimeType, "opendocument.spreadsheet")
}

func IsOdp(mimeType string) bool {
	return strings.Contains(mimeType, "opendocument.presentation")
}

func IsRtf(mimeType string) bool {
	return strings.HasPrefix(mimeType, "application/rtf") || strings.HasPrefix(mimeType, "text/rtf")
}

func IsTsv(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/tab-separated-values")
}

func IsRst(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/x-rst") || strings.HasPrefix(mimeType, "text/restructuredtext")
}

func guessMimeTypeFromURL(pageURL string) (string, error) {
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse provided URL %q", pageURL)
	}
	fileExtension := path.Ext(parsedURL.Path)
	guessedType := mime.TypeByExtension(fileExtension)
	if guessedType == "" {
		return "", errors.Errorf("failed to guess MIME type from extension for path %q: empty", parsedURL.Path)
	}
	return guessedType, nil
}

func (b *browser) toReadableContentFunction(ctx context.Context, pCtx *browserProgramCtx, argsFunction func(call otto.FunctionCall) []any, parseFunction func(ctx context.Context, outHtml string, pageURL string, args ...any) (string, error)) func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
	return func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
		pageURL, outHtml, err := b.geHtmlElementsForReadability(ctx)
		if err != nil {
			return chromedp.ActionFunc(func(ctx context.Context) error {
				return err
			}), noValue
		}
		var args []any
		if argsFunction != nil {
			args = argsFunction(call)
		}
		var readableContent string
		return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
			readableContent, err = parseFunction(ctx, outHtml, pageURL, args...)
			return err
		}), func() any {
			return readableContent
		}, b.parseActionOpts(call, 0), pCtx)
	}
}

func (b *browser) geHtmlElementsForReadability(ctx context.Context) (string, string, error) {
	var outHtml string
	var pageURL string
	if err := chromedp.EvaluateAsDevTools(`document.location.toString()`, &pageURL).Do(ctx); err != nil {
		return "", "", err
	}
	// TODO: dirty hack to support linkedin profiles for the time being
	if strings.Contains(pageURL, "linkedin.com") {
		findCtx := context.WithValue(ctx, includeInvisibleOption, true)
		if elementsHtml, err := b.findHtmlElements(findCtx, llmElReq{
			elems: ReadabilityAllowedElements,
		}); err != nil {
			return "", "", err
		} else {
			outHtml = elementsHtml
		}
	} else { // for other pages it's ok to use the whole html body
		err := chromedp.OuterHTML("body", &outHtml).Do(ctx)
		if err != nil {
			return pageURL, outHtml, errors.Wrapf(err, "readability content not found")
		}
	}
	return pageURL, outHtml, nil
}
