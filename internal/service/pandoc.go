package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/internal/exec"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const (
	pandocOutBufferSize = 1024 * 1024 * 5 // 5 Mb buffer for outputs
	pixelsPerInch       = 96
)

type Pandoc interface {
	PdfToHtml(ctx context.Context, pdfBytes []byte) (string, error)
	DocToText(ctx context.Context, docxBytes []byte) (out string, err error)
	DocToHtml(ctx context.Context, docBytes []byte) (out string, err error)
	DocToPdf(ctx context.Context, docBytes []byte, docFmt string) (out []byte, err error)
	HtmlToPdf(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) (out []byte, err error)
	HtmlToDocx(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) (out []byte, err error)
	DocxToText(ctx context.Context, docxBytes []byte) (string, error)
	AnythingToHtml(ctx context.Context, anythingBytes []byte, docFmt string, embedResources bool) (string, error)
	XlsToXlsx(ctx context.Context, xlsBytes []byte) (out []byte, err error)
	DocxToHtml(ctx context.Context, docxBytes []byte) (string, error)
	ExcelToHtml(ctx context.Context, excelBytes []byte) (string, error)
	XlsToHtml(ctx context.Context, xlsBytes []byte) (string, error)
	PptxToHtml(ctx context.Context, pptxBytes []byte) (string, error)
	PptToHtml(ctx context.Context, pptxBytes []byte) (string, error)
	HtmlToMarkdown(ctx context.Context, html string, format string) (string, error)
	OdsToHtml(ctx context.Context, odsBytes []byte) (string, error)
	OdpToHtml(ctx context.Context, odpBytes []byte) (string, error)
	OdtToHtml(ctx context.Context, odtBytes []byte) (string, error)
	RtfToHtml(ctx context.Context, rtfBytes []byte) (string, error)
}

type pandoc struct {
	pandocExecutablePath      string
	libreofficeExecutablePath string
	antiwordExecutablePath    string
	pdftohtmlExecutablePath   string
	log                       logger.Logger
}

func NewPandoc() (Pandoc, error) {
	pandocCmd, err := exec.Lookup("pandoc")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to lookup pandoc executable path")
	}
	antiwordExecutablePath, err := exec.Lookup("antiword")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to lookup antiword executable path")
	}
	pdftohtmlCmd, err := exec.Lookup("pdftohtml")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to lookup pdftohtml executable path")
	}
	libreofficeExecutablePath, err := exec.Lookup("libreoffice")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to lookup libreoffice executable path")
	}
	return &pandoc{
		pandocExecutablePath:      pandocCmd,
		pdftohtmlExecutablePath:   pdftohtmlCmd,
		antiwordExecutablePath:    antiwordExecutablePath,
		libreofficeExecutablePath: libreofficeExecutablePath,
		log:                       logger.NewLogger(),
	}, nil
}

func (p *pandoc) PdfToHtml(ctx context.Context, pdfBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	pdfFilePath := path.Join(outDir, "input.pdf")
	textFilePath := path.Join(outDir, "output.html")
	err = os.WriteFile(pdfFilePath, pdfBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary pdf file")
	}
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.pdftohtmlExecutablePath, "-noframes", "-nodrm", "-dataurls", pdfFilePath, textFilePath}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pdftohtml]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert PDF to html with command %q \n pdftohtml's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(textFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting text file from pdftohtml. command executed: %q \n pdftohtml's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) DocToPdf(ctx context.Context, docBytes []byte, docFmt string) (out []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = errors.Errorf("panic occurred: %s", err.Error())
		}
	}()
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	docFilePath := path.Join(outDir, "input."+docFmt)
	err = os.WriteFile(docFilePath, docBytes, 0o644)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to write temporary doc file")
	}
	pdfFilePath := path.Join(outDir, "output.pdf")
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.pandocExecutablePath, "--pdf-engine=xelatex", "-t", "pdf", docFilePath, "-o", pdfFilePath}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pandoc]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert doc to pdf with command %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(pdfFilePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read resulting pdf file from doc. command executed: %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	return resBytes, nil
}

func (p *pandoc) HtmlToPdf(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) (out []byte, err error) {
	allocOpts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var buf []byte

	var marginLeft float32 = 0.0
	var marginRight float32 = 0.0
	var marginTop float32 = 0.0
	var marginBottom float32 = 0.0

	if template != nil && template.Margins != nil {
		marginLeft = lo.FromPtr(template.Margins.Left)
		marginRight = lo.FromPtr(template.Margins.Right)
		marginTop = lo.FromPtr(template.Margins.Top)
		marginBottom = lo.FromPtr(template.Margins.Bottom)
	}

	// Convert px to inch
	marginLeft /= pixelsPerInch
	marginRight /= pixelsPerInch
	marginTop /= pixelsPerInch
	marginBottom /= pixelsPerInch

	footerTemplate := ""
	if template != nil && lo.FromPtr(template.ShowPageNumbers) {
		footerTemplate = `<div style="width: 100%; text-align: center; font-size: 16px;">Page <span class="pageNumber"></span> of <span class="totalPages"></span></div>`
	}

	if err := chromedp.Run(
		browserCtx,
		chromedp.Navigate("data:text/html,"+url.PathEscape(string(htmlBytes))),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, _, err = page.PrintToPDF().
				WithDisplayHeaderFooter(true).
				WithHeaderTemplate("<div></div>").
				WithFooterTemplate(footerTemplate).
				WithMarginLeft(float64(marginLeft)).
				WithMarginRight(float64(marginRight)).
				WithMarginTop(float64(marginTop)).
				WithMarginBottom(float64(marginBottom)).
				Do(ctx)
			return err
		}),
	); err != nil {
		return nil, err
	}

	return buf, nil
}

func (p *pandoc) HtmlToDocx(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) (out []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = errors.Errorf("panic occurred: %s", err.Error())
		}
	}()
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	htmlFilePath := path.Join(outDir, "input.html")
	err = os.WriteFile(htmlFilePath, htmlBytes, 0o644)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to write temporary html file")
	}
	docxFilePath := path.Join(outDir, "output.docx")
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.pandocExecutablePath, "-f", "html", "-t", "docx", htmlFilePath, "-o", docxFilePath}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pandoc]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert html to docx with command %q \n pandoc's output: %q", executedCommand, debugOutput)
	}

	postprocessedDocxFilePath := path.Join(outDir, "output_postprocessed.docx")
	err = p.postprocessDocx(ctx, docxFilePath, postprocessedDocxFilePath, template)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to postprocess docx")
	}

	resBytes, err := os.ReadFile(postprocessedDocxFilePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read resulting docx file from html. command executed: %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	return resBytes, nil
}

func (p *pandoc) DocToText(ctx context.Context, docxBytes []byte) (out string, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = errors.Errorf("panic occurred: %s", err.Error())
		}
	}()
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	docFilePath := path.Join(outDir, "input.doc")
	err = os.WriteFile(docFilePath, docxBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary doc file")
	}
	textFilePath := path.Join(outDir, "output.txt")
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{"sh", "-c", fmt.Sprintf("%s %s > %s", p.antiwordExecutablePath, docFilePath, textFilePath)}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[antiword]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert doc to text with command %q \n antiword's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(textFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting text file from doc. command executed: %q \n antiword's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) DocToHtml(ctx context.Context, docxBytes []byte) (out string, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = errors.Errorf("panic occurred: %s", err.Error())
		}
	}()
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	docFilePath := path.Join(outDir, "input.doc")
	err = os.WriteFile(docFilePath, docxBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary doc file")
	}
	textFilePath := path.Join(outDir, "input.html")
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.libreofficeExecutablePath, "--headless", "--convert-to", "html", docFilePath, "--outdir", outDir}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[libreoffice]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert doc to html with command %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(textFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting html file from doc. command executed: %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

// OdsToHtml converts an ODS (OpenDocument Spreadsheet) file to HTML using LibreOffice.
func (p *pandoc) OdsToHtml(ctx context.Context, odsBytes []byte) (string, error) {
	return p.libreOfficeToHtml(ctx, odsBytes, "input.ods", "[libreoffice-ods]")
}

// OdpToHtml converts an ODP (OpenDocument Presentation) file to HTML using LibreOffice.
func (p *pandoc) OdpToHtml(ctx context.Context, odpBytes []byte) (string, error) {
	return p.libreOfficeToHtml(ctx, odpBytes, "input.odp", "[libreoffice-odp]")
}

// OdtToHtml converts an ODT (OpenDocument Text) file to HTML using LibreOffice.
// LibreOffice is used instead of pandoc because some ODT variants (e.g. files with change
// tracking and no styles.xml) cause pandoc to fail with EX_USAGE (exit 64).
func (p *pandoc) OdtToHtml(ctx context.Context, odtBytes []byte) (string, error) {
	return p.libreOfficeToHtml(ctx, odtBytes, "input.odt", "[libreoffice-odt]")
}

// RtfToHtml converts an RTF file to HTML using LibreOffice.
// LibreOffice is used instead of pandoc because some RTF files with non-standard hex escapes
// (e.g. \'NN placeholders in document text) cause pandoc to fail with a parse error.
func (p *pandoc) RtfToHtml(ctx context.Context, rtfBytes []byte) (string, error) {
	return p.libreOfficeToHtml(ctx, rtfBytes, "input.rtf", "[libreoffice-rtf]")
}

// libreOfficeToHtml converts an OpenDocument file to HTML using LibreOffice --convert-to html.
// inputFileName must have the correct extension (e.g. "input.ods") so LibreOffice picks the right filter.
// LibreOffice writes the result as <basename>.html in the same outDir.
func (p *pandoc) libreOfficeToHtml(ctx context.Context, fileBytes []byte, inputFileName string, logPrefix string) (out string, err error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	inputFilePath := path.Join(outDir, inputFileName)
	// LibreOffice names the output file <inputBasename>.html — strip the original extension.
	baseName := strings.TrimSuffix(inputFileName, path.Ext(inputFileName))
	htmlFilePath := path.Join(outDir, baseName+".html")
	err = os.WriteFile(inputFilePath, fileBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary %s file", inputFileName)
	}
	buf := bytes.NewBuffer(make([]byte, pandocOutBufferSize))
	cmd := exec.NewExecWithOutput(ctx, buf)
	cmdArgs := []string{p.libreofficeExecutablePath, "--headless", "--convert-to", "html", inputFilePath, "--outdir", outDir}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, logPrefix+": "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert %s to html with command %q \n libreoffice's output: %q", inputFileName, executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(htmlFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting html file. command executed: %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) AnythingToHtml(ctx context.Context, anythingBytes []byte, docFmt string, embedResources bool) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	inputFilePath := path.Join(outDir, "input."+docFmt)
	htmlFilePath := path.Join(outDir, "output.html")
	err = os.WriteFile(inputFilePath, anythingBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary input file")
	}
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.pandocExecutablePath, "-f", docFmt, "-t", "html", inputFilePath, "-o", htmlFilePath}
	if embedResources {
		cmdArgs = append(cmdArgs, "--embed-resources")
	}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pandoc]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert anything to html with command %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(htmlFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting html file from pandoc. command executed: %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) XlsToXlsx(ctx context.Context, xlsBytes []byte) (out []byte, err error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	xlsFilePath := path.Join(outDir, "input.xls")
	err = os.WriteFile(xlsFilePath, xlsBytes, 0o644)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to write temporary xls file")
	}
	xlsxFilePath := path.Join(outDir, "input.xlsx")
	buf := bytes.NewBuffer(make([]byte, pandocOutBufferSize))
	cmd := exec.NewExecWithOutput(ctx, buf)
	cmdArgs := []string{p.libreofficeExecutablePath, "--headless", "--convert-to", "xlsx", xlsFilePath, "--outdir", outDir}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[libreoffice]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert xls to xlsx with command %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(xlsxFilePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read resulting xlsx file from libreoffice. command executed: %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	return resBytes, nil
}

func (p *pandoc) DocxToText(ctx context.Context, docxBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	docxFilePath := path.Join(outDir, "input.docx")
	textFilePath := path.Join(outDir, "output.txt")
	err = os.WriteFile(docxFilePath, docxBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary pdf file")
	}
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.pandocExecutablePath, "-f", "docx", "-t", "plain", docxFilePath, "-o", textFilePath}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[doctotext]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert Docx to text with command %q \n doctotext's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(textFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting text file from doctotext. command executed: %q \n doctotext's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) DocxToHtml(ctx context.Context, docxBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	docxFilePath := path.Join(outDir, "input.docx")
	htmlFilePath := path.Join(outDir, "input.html")
	err = os.WriteFile(docxFilePath, docxBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary docx file")
	}
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.libreofficeExecutablePath, "--headless", "--convert-to", "html", docxFilePath, "--outdir", outDir}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[libreoffice]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert DOCX to HTML with command %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(htmlFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting HTML file from libreoffice. command executed: %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) ExcelToHtml(ctx context.Context, excelBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	xlsxFilePath := path.Join(outDir, "input.xlsx")
	mdFilePath := path.Join(outDir, "output.md")
	err = os.WriteFile(xlsxFilePath, excelBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary xlsx file")
	}

	// Try native pandoc XLSX reader first (supports all sheets with headers)
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{
		p.pandocExecutablePath,
		"-f", "xlsx",
		"-t", "markdown+pipe_tables-simple_tables-multiline_tables-grid_tables",
		xlsxFilePath,
		"-o", mdFilePath,
	}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pandoc xlsx]: "+s)
		},
	})
	if err != nil {
		p.log.Warnf(ctx, "failed to convert XLSX with pandoc native reader, falling back to in2csv: %v", err)
		// Fallback to in2csv method
		return p.convertExcelViaIn2csv(ctx, xlsxFilePath, outDir)
	}

	// Read the markdown output
	mdBytes, err := os.ReadFile(mdFilePath)
	if err != nil {
		p.log.Warnf(ctx, "failed to read markdown from pandoc, falling back to in2csv: %v", err)
		return p.convertExcelViaIn2csv(ctx, xlsxFilePath, outDir)
	}

	markdown := strings.TrimSpace(string(mdBytes))
	if markdown == "" {
		p.log.Warnf(ctx, "pandoc returned empty markdown, falling back to in2csv")
		return p.convertExcelViaIn2csv(ctx, xlsxFilePath, outDir)
	}

	return markdown, nil
}

// convertExcelViaIn2csv is fallback method using in2csv from csvkit
func (p *pandoc) convertExcelViaIn2csv(ctx context.Context, xlsxFilePath, outDir string) (string, error) {
	// Step 1: Get list of all sheet names using in2csv --names
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	namesCmdArgs := []string{"/home/seluser/venv/bin/in2csv", "--names", xlsxFilePath}
	_, err := cmd.ExecCommand(namesCmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[in2csv]: "+s)
		},
	})
	if err != nil {
		p.log.Warnf(ctx, "failed to get sheet names with in2csv, falling back to LibreOffice: %v", err)
		// Fallback to LibreOffice for single sheet
		return p.convertExcelToMarkdownViaCsv(ctx, xlsxFilePath, outDir)
	}

	// Parse sheet names (one per line)
	sheetNamesOutput := strings.TrimSpace(buf.String())
	if sheetNamesOutput == "" {
		p.log.Warnf(ctx, "no sheet names returned from in2csv")
		return p.convertExcelToMarkdownViaCsv(ctx, xlsxFilePath, outDir)
	}

	sheetNames := strings.Split(sheetNamesOutput, "\n")
	p.log.Infof(ctx, "found %d sheets: %v", len(sheetNames), sheetNames)

	// Step 2: Convert each sheet to CSV and then to markdown
	var allMarkdown strings.Builder

	for i, sheetName := range sheetNames {
		sheetName = strings.TrimSpace(sheetName)
		if sheetName == "" {
			continue
		}

		// Add sheet heading if multiple sheets
		if len(sheetNames) > 1 {
			if i > 0 {
				allMarkdown.WriteString("\n")
			}
			allMarkdown.WriteString(fmt.Sprintf("## %s\n\n", sheetName))
		}

		// Convert this specific sheet to CSV
		csvBuf := &bytes.Buffer{}
		csvCmd := exec.NewExecWithOutput(ctx, csvBuf)

		csvCmdArgs := []string{"/home/seluser/venv/bin/in2csv", "--sheet", sheetName, xlsxFilePath}
		_, err = csvCmd.ExecCommand(csvCmdArgs, exec.Opts{
			Wd: outDir,
			Callback: func(s string) {
				p.log.Infof(ctx, "[in2csv]: "+s)
			},
		})
		if err != nil {
			p.log.Warnf(ctx, "failed to convert sheet %q: %v", sheetName, err)
			continue
		}

		// Parse CSV and create markdown table
		csvData := csvBuf.Bytes()
		reader := csv.NewReader(bytes.NewReader(csvData))
		reader.LazyQuotes = true // Handle bare quotes
		reader.TrimLeadingSpace = true
		rows, err := reader.ReadAll()
		if err != nil {
			p.log.Warnf(ctx, "failed to parse CSV for sheet %q: %v", sheetName, err)
			continue
		}

		if len(rows) == 0 {
			p.log.Infof(ctx, "sheet %q is empty, skipping", sheetName)
			continue
		}

		// Build markdown table with pipe format
		// First row as header
		allMarkdown.WriteString("| ")
		allMarkdown.WriteString(strings.Join(rows[0], " | "))
		allMarkdown.WriteString(" |\n")

		// Separator row
		allMarkdown.WriteString("|")
		for range rows[0] {
			allMarkdown.WriteString(" --- |")
		}
		allMarkdown.WriteString("\n")

		// Data rows
		for _, row := range rows[1:] {
			allMarkdown.WriteString("| ")
			allMarkdown.WriteString(strings.Join(row, " | "))
			allMarkdown.WriteString(" |\n")
		}
	}

	markdown := allMarkdown.String()
	if markdown == "" {
		return "", errors.New("no sheets could be converted to markdown")
	}

	return markdown, nil
}

// convertExcelToMarkdownViaCsv is fallback method using LibreOffice (single sheet only)
func (p *pandoc) convertExcelToMarkdownViaCsv(ctx context.Context, xlsxFilePath, outDir string) (string, error) {
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.libreofficeExecutablePath, "--headless", "--convert-to", "csv", xlsxFilePath, "--outdir", outDir}
	_, err := cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[libreoffice]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert XLSX to CSV with command %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}

	// Read CSV and convert to markdown table directly
	csvFilePath := path.Join(outDir, "input.csv")
	csvBytes, err := os.ReadFile(csvFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting CSV file from libreoffice. command executed: %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}

	// Parse CSV and create markdown table
	reader := csv.NewReader(bytes.NewReader(csvBytes))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return "", errors.Wrap(err, "failed to parse CSV")
	}

	if len(rows) == 0 {
		return "", nil
	}

	// Build markdown table with pipe format
	var markdown strings.Builder

	// First row as header
	markdown.WriteString("| ")
	markdown.WriteString(strings.Join(rows[0], " | "))
	markdown.WriteString(" |\n")

	// Separator row
	markdown.WriteString("|")
	for range rows[0] {
		markdown.WriteString(" --- |")
	}
	markdown.WriteString("\n")

	// Data rows
	for _, row := range rows[1:] {
		markdown.WriteString("| ")
		markdown.WriteString(strings.Join(row, " | "))
		markdown.WriteString(" |\n")
	}

	return markdown.String(), nil
}

func (p *pandoc) PptxToHtml(ctx context.Context, pptxBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	pptxFilePath := path.Join(outDir, "input.pptx")
	mdFilePath := path.Join(outDir, "output.md")
	err = os.WriteFile(pptxFilePath, pptxBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary pptx file")
	}

	// Try native pandoc PPTX reader first (supports SmartArt and better parsing)
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{
		p.pandocExecutablePath,
		"-f", "pptx",
		"-t", "markdown+pipe_tables-simple_tables-multiline_tables-grid_tables",
		pptxFilePath,
		"-o", mdFilePath,
	}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pandoc pptx]: "+s)
		},
	})
	if err != nil {
		p.log.Warnf(ctx, "failed to convert PPTX with pandoc native reader, falling back to LibreOffice: %v", err)
		// Fallback to LibreOffice method
		return p.convertPptxViaLibreOffice(ctx, pptxFilePath, outDir)
	}

	// Read the markdown output
	mdBytes, err := os.ReadFile(mdFilePath)
	if err != nil {
		p.log.Warnf(ctx, "failed to read markdown from pandoc, falling back to LibreOffice: %v", err)
		return p.convertPptxViaLibreOffice(ctx, pptxFilePath, outDir)
	}

	markdown := strings.TrimSpace(string(mdBytes))
	if markdown == "" {
		p.log.Warnf(ctx, "pandoc returned empty markdown, falling back to LibreOffice")
		return p.convertPptxViaLibreOffice(ctx, pptxFilePath, outDir)
	}

	return markdown, nil
}

// convertPptxViaLibreOffice is fallback method using LibreOffice
func (p *pandoc) convertPptxViaLibreOffice(ctx context.Context, pptxFilePath, outDir string) (string, error) {
	htmlFilePath := path.Join(outDir, "input.html")
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	cmdArgs := []string{p.libreofficeExecutablePath, "--headless", "--convert-to", "html:impress_html_Export", pptxFilePath, "--outdir", outDir}
	_, err := cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[libreoffice]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert PPTX to HTML with command %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(htmlFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting HTML file from libreoffice. command executed: %q \n libreoffice's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) PptToHtml(ctx context.Context, pptBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	pptFilePath := path.Join(outDir, "input.ppt")
	err = os.WriteFile(pptFilePath, pptBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary ppt file")
	}

	// PPT (old format) - only LibreOffice can handle it, no native pandoc support
	return p.convertPptxViaLibreOffice(ctx, pptFilePath, outDir)
}

func (p *pandoc) XlsToHtml(ctx context.Context, xlsBytes []byte) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	xlsFilePath := path.Join(outDir, "input.xls")
	err = os.WriteFile(xlsFilePath, xlsBytes, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary xls file")
	}

	// Step 1: Get list of all sheet names using in2csv --names (works for XLS too!)
	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	namesCmdArgs := []string{"/home/seluser/venv/bin/in2csv", "--names", xlsFilePath}
	_, err = cmd.ExecCommand(namesCmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[in2csv]: "+s)
		},
	})
	if err != nil {
		p.log.Warnf(ctx, "failed to get sheet names with in2csv, falling back to LibreOffice: %v", err)
		// Fallback to LibreOffice for single sheet
		return p.convertExcelToMarkdownViaCsv(ctx, xlsFilePath, outDir)
	}

	// Parse sheet names (one per line)
	sheetNamesOutput := strings.TrimSpace(buf.String())
	if sheetNamesOutput == "" {
		p.log.Warnf(ctx, "no sheet names returned from in2csv")
		return p.convertExcelToMarkdownViaCsv(ctx, xlsFilePath, outDir)
	}

	sheetNames := strings.Split(sheetNamesOutput, "\n")
	p.log.Infof(ctx, "found %d sheets in XLS: %v", len(sheetNames), sheetNames)

	// Step 2: Convert each sheet to CSV and then to markdown
	var allMarkdown strings.Builder

	for i, sheetName := range sheetNames {
		sheetName = strings.TrimSpace(sheetName)
		if sheetName == "" {
			continue
		}

		// Add sheet heading if multiple sheets
		if len(sheetNames) > 1 {
			if i > 0 {
				allMarkdown.WriteString("\n")
			}
			allMarkdown.WriteString(fmt.Sprintf("## %s\n\n", sheetName))
		}

		// Convert this specific sheet to CSV
		csvBuf := &bytes.Buffer{}
		csvCmd := exec.NewExecWithOutput(ctx, csvBuf)

		csvCmdArgs := []string{"/home/seluser/venv/bin/in2csv", "--sheet", sheetName, xlsFilePath}
		_, err = csvCmd.ExecCommand(csvCmdArgs, exec.Opts{
			Wd: outDir,
			Callback: func(s string) {
				p.log.Infof(ctx, "[in2csv]: "+s)
			},
		})
		if err != nil {
			p.log.Warnf(ctx, "failed to convert sheet %q: %v", sheetName, err)
			continue
		}

		// Parse CSV and create markdown table
		csvData := csvBuf.Bytes()
		reader := csv.NewReader(bytes.NewReader(csvData))
		reader.LazyQuotes = true // Handle bare quotes
		reader.TrimLeadingSpace = true
		rows, err := reader.ReadAll()
		if err != nil {
			p.log.Warnf(ctx, "failed to parse CSV for sheet %q: %v", sheetName, err)
			continue
		}

		if len(rows) == 0 {
			p.log.Infof(ctx, "sheet %q is empty, skipping", sheetName)
			continue
		}

		// Build markdown table with pipe format
		// First row as header
		allMarkdown.WriteString("| ")
		allMarkdown.WriteString(strings.Join(rows[0], " | "))
		allMarkdown.WriteString(" |\n")

		// Separator row
		allMarkdown.WriteString("|")
		for range rows[0] {
			allMarkdown.WriteString(" --- |")
		}
		allMarkdown.WriteString("\n")

		// Data rows
		for _, row := range rows[1:] {
			allMarkdown.WriteString("| ")
			allMarkdown.WriteString(strings.Join(row, " | "))
			allMarkdown.WriteString(" |\n")
		}
	}

	markdown := allMarkdown.String()
	if markdown == "" {
		return "", errors.New("no sheets could be converted to markdown")
	}

	return markdown, nil
}

func (p *pandoc) HtmlToMarkdown(ctx context.Context, html string, format string) (string, error) {
	outDir, cleanup, err := p.createTempDir(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to init temp dir")
	}
	defer cleanup()
	htmlFilePath := path.Join(outDir, "input.html")
	mdFilePath := path.Join(outDir, "output.md")
	err = os.WriteFile(htmlFilePath, []byte(html), 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write temporary html file")
	}

	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)

	// Use markdown with pipe_tables extension for proper table formatting
	targetFormat := format
	if format == "markdown" {
		targetFormat = "markdown+pipe_tables-simple_tables-multiline_tables-grid_tables"
	}

	cmdArgs := []string{p.pandocExecutablePath, "--wrap=preserve", "-f", "html", "-t", targetFormat, htmlFilePath, "-o", mdFilePath}
	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{
		Wd: outDir,
		Callback: func(s string) {
			p.log.Infof(ctx, "[pandoc]: "+s)
		},
	})
	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return "", errors.Wrapf(err, "failed to convert HTML to Pandoc with command %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	resBytes, err := os.ReadFile(mdFilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read resulting md file from pandoc. command executed: %q \n pandoc's output: %q", executedCommand, debugOutput)
	}
	return strings.TrimSpace(string(resBytes)), nil
}

func (p *pandoc) createTempDir(ctx context.Context) (string, func(), error) {
	uid, err := uuid.NewRandom()
	if err != nil {
		return "", nil, errors.Wrapf(err, "failed to generate new UUID")
	}
	outDir, err := os.MkdirTemp(os.TempDir(), uid.String())
	if err != nil {
		return "", nil, errors.Wrapf(err, "failed to create temp dir")
	}
	cleanup := func() {
		if err := os.RemoveAll(outDir); err != nil {
			p.log.Errorf(ctx, "failed to cleanup dir %s: %q", outDir, err)
		}
		if err := removeAllGlob(fmt.Sprintf("%s/*", outDir)); err != nil {
			p.log.Errorf(ctx, "failed to cleanup temp dir: %v", err)
		}
	}
	return outDir, cleanup, err
}

func removeAllGlob(path string) (err error) {
	contents, err := filepath.Glob(path)
	if err != nil {
		return err // nolint: gofumpt
	}
	for _, item := range contents {
		err = os.RemoveAll(item)
		if err != nil {
			return err // nolint: gofumpt
		}
	}
	return err
}

func (p *pandoc) postprocessDocx(ctx context.Context, inputDocxPath, outputDocxPath string, template *dto.HtmlTemplate) error {
	postprocessDocxScriptPath := filepath.Join(os.Getenv("SCRIPTS_DIR"), "postprocess_docx.py")

	var templateJson []byte
	var err error
	if template != nil {
		templateJson, err = json.Marshal(template)
		if err != nil {
			return errors.Wrapf(err, "failed to marshal template to JSON")
		}
	} else {
		templateJson = []byte("{}")
	}

	buf := &bytes.Buffer{}
	cmd := exec.NewExecWithOutput(ctx, buf)
	cmdArgs := []string{"python3", postprocessDocxScriptPath, inputDocxPath, outputDocxPath, "--template", string(templateJson)}

	_, err = cmd.ExecCommand(cmdArgs, exec.Opts{})

	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return errors.Wrapf(err, "failed to postprocess docx with command %q \n python's output: %q", executedCommand, debugOutput)
	}
	return nil
}
