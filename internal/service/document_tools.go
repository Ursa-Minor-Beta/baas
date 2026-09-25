package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/internal/service/pdf"
)

const (
	documentToolsAvailable   = "available"
	documentToolsUnavailable = "unavailable"
)

// docToolchain holds the constructors for everything that needs the document
// toolchain, so tests can drive initDocumentTools without the binaries.
type docToolchain struct {
	newPandoc      func() (Pandoc, error)
	newPdfToImages func() (pdf.PdfToImagesConverter, error)
	probeParser    func() error
}

// initDocumentTools installs the real converters, or a stand-in that explains
// itself for each one that cannot start. Base images built with the default
// DOCUMENT_TOOLS=off ship without the toolchain, and browser automation does
// not need it, so by default a gap is a warning. With require set it fails
// boot, for deployments that depend on the document endpoints.
func (s *Server) initDocumentTools(log logger.Logger, tc docToolchain, require bool) error {
	var missing []string

	if p, err := tc.newPandoc(); err != nil {
		missing = append(missing, fmt.Sprintf("pandoc toolchain (%v)", err))
		s.pandoc = newUnavailablePandoc(err)
		s.aiPandocCommit = ""
	} else {
		s.pandoc = p
	}

	if c, err := tc.newPdfToImages(); err != nil {
		missing = append(missing, fmt.Sprintf("pdf-to-images (%v)", err))
		s.pdfToImagesConverter = pdf.NewUnavailablePdfToImagesConverter(err)
	} else {
		s.pdfToImagesConverter = c
	}

	if err := tc.probeParser(); err != nil {
		missing = append(missing, fmt.Sprintf("PDF parser (%v)", err))
		s.pdfParser = pdf.NewUnavailableParser(err)
	}

	if len(missing) == 0 {
		s.documentTools = documentToolsAvailable
		return nil
	}
	s.documentTools = documentToolsUnavailable
	if require {
		return errors.Errorf("REQUIRE_DOCUMENT_TOOLS is set but the document toolchain is incomplete: %s",
			strings.Join(missing, "; "))
	}
	log.Warnf(context.Background(),
		"document conversion disabled, missing: %s. Affected: /api/parse, /api/parse-to-markdown-kv, "+
			"/api/pdf-to-images, /api/render-markdown (docx), /api/extract-markdown, Office and PDF URLs "+
			"in /api/readability and /api/eke-extract, returnPandoc, and the pandoc browser action. "+
			"Build the base image with --build-arg DOCUMENT_TOOLS=on to enable them",
		strings.Join(missing, "; "))
	return nil
}
