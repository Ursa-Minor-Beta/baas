package service

import (
	"context"

	"github.com/pkg/errors"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// unavailablePandoc stands in when the document toolchain is absent, which is
// the case for a base image built with the default DOCUMENT_TOOLS=off. Every
// call fails with an explanation instead of the service refusing to boot or
// the document endpoints dereferencing a nil converter.
type unavailablePandoc struct {
	reason error
}

func newUnavailablePandoc(reason error) Pandoc {
	return &unavailablePandoc{reason: reason}
}

func (p *unavailablePandoc) err() error {
	return errors.Wrap(p.reason,
		"document conversion is unavailable: this image was built without the document toolchain, "+
			"rebuild the base image with --build-arg DOCUMENT_TOOLS=on")
}

func (p *unavailablePandoc) PdfToHtml(ctx context.Context, pdfBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) DocToText(ctx context.Context, docxBytes []byte) (out string, err error) {
	return "", p.err()
}

func (p *unavailablePandoc) DocToHtml(ctx context.Context, docBytes []byte) (out string, err error) {
	return "", p.err()
}

func (p *unavailablePandoc) DocToPdf(ctx context.Context, docBytes []byte, docFmt string) (out []byte, err error) {
	return nil, p.err()
}

func (p *unavailablePandoc) HtmlToPdf(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) (out []byte, err error) {
	return nil, p.err()
}

func (p *unavailablePandoc) HtmlToDocx(ctx context.Context, htmlBytes []byte, template *dto.HtmlTemplate) (out []byte, err error) {
	return nil, p.err()
}

func (p *unavailablePandoc) DocxToText(ctx context.Context, docxBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) AnythingToHtml(ctx context.Context, anythingBytes []byte, docFmt string, embedResources bool) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) XlsToXlsx(ctx context.Context, xlsBytes []byte) (out []byte, err error) {
	return nil, p.err()
}

func (p *unavailablePandoc) DocxToHtml(ctx context.Context, docxBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) ExcelToHtml(ctx context.Context, excelBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) XlsToHtml(ctx context.Context, xlsBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) PptxToHtml(ctx context.Context, pptxBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) PptToHtml(ctx context.Context, pptxBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) HtmlToMarkdown(ctx context.Context, html string, format string) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) OdsToHtml(ctx context.Context, odsBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) OdpToHtml(ctx context.Context, odpBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) OdtToHtml(ctx context.Context, odtBytes []byte) (string, error) {
	return "", p.err()
}

func (p *unavailablePandoc) RtfToHtml(ctx context.Context, rtfBytes []byte) (string, error) {
	return "", p.err()
}
