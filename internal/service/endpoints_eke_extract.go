package service

// endpoints_eke_extract.go
//
// POST /api/eke-extract — multipart/form-data file upload endpoint for EKE extraction.
//
// Unlike POST /api/readability (which accepts a JSON body with a URL or base64 content),
// this endpoint receives the raw file bytes as a multipart form part.
// This eliminates the ~33% base64 overhead and the JSON serialization/deserialization
// of large binary payloads, making it significantly faster for binary files.
//
// Form fields:
//   file         — binary file bytes (multipart file part)
//   mimeType     — MIME type of the file, e.g. "application/pdf" (required)
//   returnPandoc — pandoc output format, e.g. "markdown" (optional)

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const (
	// maxUploadSize is the maximum allowed multipart form size (64 MB).
	maxUploadSize = 64 << 20
)

// eKeExtractEndpoint handles POST /api/eke-extract.
//
// @Schemes
// @Security Bearer
// @Description process a binary file uploaded directly (multipart/form-data) and return readable content.
// @Tags run
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "binary file bytes"
// @Param mimeType formData string true "MIME type of the file"
// @Param returnPandoc formData string false "pandoc output format (e.g. markdown)"
// @Success 200 {object} dto.ReadabilityResult
// @Router /api/eke-extract [post]
func (s *Server) eKeExtractEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	req := c.Request()
	if err := req.ParseMultipartForm(maxUploadSize); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "failed to parse multipart form: " + err.Error(),
		})
		return nil
	}

	file, _, err := req.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "missing required form field \"file\": " + err.Error(),
		})
		return nil
	}
	defer func() { _ = file.Close() }()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to read uploaded file: " + err.Error(),
		})
		return nil
	}

	mimeType := strings.TrimSpace(req.FormValue("mimeType"))
	if mimeType == "" {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "missing required form field \"mimeType\"",
		})
		return nil
	}

	var returnPandocPtr *string
	if rp := req.FormValue("returnPandoc"); rp != "" {
		returnPandocPtr = lo.ToPtr(rp)
	}

	result, err := s.doRunEkeExtract(ctx, fileBytes, mimeType, returnPandocPtr)
	if err != nil {
		s.Logger().Errorf(ctx, "eke-extract failed: %v", err)
		c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return nil
	}

	result.Meta = s.GetMeta(ctx)
	c.JSON(http.StatusOK, result)
	return nil
}

// doRunEkeExtract processes file bytes directly without base64 or URL indirection.
// mimeType must be a recognised supported type (see IsSupportedFile / IsAnyText).
func (s *Server) doRunEkeExtract(ctx context.Context, fileBytes []byte, mimeType string, returnPandoc *string) (*dto.ReadabilityResult, error) {
	if len(fileBytes) == 0 {
		return nil, errors.Errorf("file bytes are empty")
	}

	// Build a minimal config — URL is a synthetic placeholder used only in error messages
	// and as a base URL for ParseReadabilityContent (relative-link resolution).
	cfg := &dto.ReadabilityConfig{
		URL:          "http://localhost/upload",
		ReturnPandoc: returnPandoc,
	}

	reader := io.NopCloser(bytes.NewReader(fileBytes))

	switch {
	case IsPdf(mimeType):
		return s.parsePdf(ctx, cfg, reader)
	case IsDocX(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtDocx)
	case IsDoc(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtDoc)
	case IsXls(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtXls)
	case IsExcel(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtExcel)
	case IsPptx(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtPptx)
	case IsPpt(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtPpt)
	case IsTsv(mimeType):
		return s.parseTsv(ctx, cfg, reader)
	case IsCsv(mimeType):
		return s.parseCsv(ctx, cfg, reader)
	case IsOdt(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtOdt)
	case IsOds(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtOds)
	case IsOdp(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtOdp)
	case IsRtf(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtRtf)
	case IsRst(mimeType):
		return s.parseMsOffice(ctx, cfg, reader, fmtRst)
	case IsAnyText(mimeType):
		// I need this to extend functionality without touching existing code.
		return s.parseEkeTextBytes(ctx, cfg, fileBytes)
	default:
		return nil, errors.Errorf("unsupported MIME type for direct upload: %q", mimeType)
	}
}

// parseEkeTextBytes parses raw text/HTML bytes using the readability engine.
func (s *Server) parseEkeTextBytes(ctx context.Context, cfg *dto.ReadabilityConfig, data []byte) (*dto.ReadabilityResult, error) {
	article, err := ParseReadabilityContent(bytes.NewReader(data), cfg.URL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse text content")
	}

	result := s.articleToResult(ctx, cfg, data, dto.ToArticle(*article), false)

	if cfg.ReturnPandoc != nil && result.Pandoc == nil {
		ctx2, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()
		// TODO: remove extra step in v2 of eke endpoint
		md, err := s.pandoc.HtmlToMarkdown(ctx2, string(data), lo.FromPtr(cfg.ReturnPandoc))
		if err == nil {
			result.Pandoc = lo.ToPtr(md)
		}
	}

	return result, nil
}
