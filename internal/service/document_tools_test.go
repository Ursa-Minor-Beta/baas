package service

import (
	"context"
	"net/http"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/internal/doctools"
	"github.com/Ursa-Minor-Beta/baas/internal/service/pdf"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

type fakePandoc struct{ Pandoc }

type fakePdfToImages struct{ pdf.PdfToImagesConverter }

type fakeParser struct{ pdf.Parser }

func toolchain(pandocErr, imagesErr, parserErr error) docToolchain {
	return docToolchain{
		newPandoc: func() (Pandoc, error) {
			if pandocErr != nil {
				return nil, pandocErr
			}
			return fakePandoc{}, nil
		},
		newPdfToImages: func() (pdf.PdfToImagesConverter, error) {
			if imagesErr != nil {
				return nil, imagesErr
			}
			return fakePdfToImages{}, nil
		},
		probeParser: func() error { return parserErr },
	}
}

func newDocToolsServer() *Server {
	return &Server{aiPandocCommit: "3ac23e0", pdfParser: fakeParser{}}
}

func Test_initDocumentTools(t *testing.T) {
	log := logger.NewLogger()
	missing := errors.New("not found in $PATH: pandoc")

	t.Run("full toolchain", func(t *testing.T) {
		RegisterTestingT(t)
		s := newDocToolsServer()
		Expect(s.initDocumentTools(log, toolchain(nil, nil, nil), true)).To(Succeed())
		Expect(s.pandoc).To(Equal(fakePandoc{}))
		Expect(s.pdfToImagesConverter).To(Equal(fakePdfToImages{}))
		Expect(s.pdfParser).To(Equal(fakeParser{}))
		Expect(s.documentTools).To(Equal(documentToolsAvailable))
		Expect(s.aiPandocCommit).To(Equal("3ac23e0"))
	})

	t.Run("slim image boots with stand-ins", func(t *testing.T) {
		RegisterTestingT(t)
		s := newDocToolsServer()
		Expect(s.initDocumentTools(log, toolchain(missing, missing, missing), false)).To(Succeed())
		Expect(s.pandoc).To(BeAssignableToTypeOf(&unavailablePandoc{}))
		Expect(s.pdfToImagesConverter).NotTo(BeNil())
		Expect(s.pdfToImagesConverter).NotTo(Equal(fakePdfToImages{}))
		Expect(s.pdfParser).NotTo(Equal(fakeParser{}))
		Expect(s.documentTools).To(Equal(documentToolsUnavailable))
		Expect(s.aiPandocCommit).To(BeEmpty(), "no pandoc, so no pandoc commit to advertise")
	})

	t.Run("one gap is enough to report unavailable", func(t *testing.T) {
		RegisterTestingT(t)
		s := newDocToolsServer()
		Expect(s.initDocumentTools(log, toolchain(nil, nil, missing), false)).To(Succeed())
		Expect(s.pandoc).To(Equal(fakePandoc{}))
		Expect(s.aiPandocCommit).To(Equal("3ac23e0"))
		Expect(s.documentTools).To(Equal(documentToolsUnavailable))
	})

	t.Run("REQUIRE_DOCUMENT_TOOLS fails boot and names every gap", func(t *testing.T) {
		RegisterTestingT(t)
		s := newDocToolsServer()
		err := s.initDocumentTools(log, toolchain(nil, errors.New("qpdf"), errors.New("pymupdf")), true)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("REQUIRE_DOCUMENT_TOOLS"))
		Expect(err.Error()).To(ContainSubstring("qpdf"))
		Expect(err.Error()).To(ContainSubstring("pymupdf"))
	})
}

// The slim image used to dereference a nil converter here.
func Test_doPdfToImages_slimImageExplains(t *testing.T) {
	RegisterTestingT(t)
	s := newDocToolsServer()
	Expect(s.initDocumentTools(logger.NewLogger(), toolchain(nil, errors.New("qpdf not found"), nil), false)).To(Succeed())

	res, err := s.doPdfToImages(context.Background(), &dto.PdfToImagesConfig{})
	Expect(res).To(BeNil())
	Expect(errorStatus(err)).To(Equal(http.StatusNotImplemented))
	Expect(err.Error()).To(ContainSubstring("DOCUMENT_TOOLS=on"))
}

func Test_errorStatus(t *testing.T) {
	RegisterTestingT(t)
	Expect(errorStatus(errors.Wrap(doctools.Unavailable(errors.New("x")), "failed"))).To(Equal(http.StatusNotImplemented))
	Expect(errorStatus(errors.New("boom"))).To(Equal(http.StatusInternalServerError))
}
