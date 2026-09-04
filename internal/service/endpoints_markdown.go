package service

import (
	"context"
	"net/http"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry"

	"github.com/Ursa-Minor-Beta/baas/internal/service/markdown"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// @Schemes
// @Security Bearer
// @Description renders markdown content to HTML.
// @Tags run
// @Accept json
// @Produce json
// @Param data body dto.RenderMarkdownConfig true "markdown config"
// @Success 200 {object} dto.RenderMarkdownResult
// @Router /api/render-markdown [post]
func (s *Server) renderMarkdownEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	if result, ok := service.WithReadBody(ctx, s, c, "Markdown render", func(cfg *dto.RenderMarkdownConfig) (*dto.RenderMarkdownResult, error) {
		return s.doRenderMarkdown(ctx, cfg)
	}); ok && result != nil {
		c.JSON(http.StatusOK, result)
	}

	return nil
}

func (s *Server) doRenderMarkdown(ctx context.Context, cfg *dto.RenderMarkdownConfig) (*dto.RenderMarkdownResult, error) {
	// Check if markdown renderer is initialized
	if s.markdownRenderer == nil {
		return nil, errors.New("markdown renderer is not initialized - check SCRIPTS_DIR environment variable and script file permissions")
	}

	// Check if html templater is initialized
	if s.htmlTemplater == nil {
		return nil, errors.New("html templater is not initialized")
	}

	html, err := s.markdownRenderer.Render(ctx, cfg.Markdown, markdown.RenderOpts{
		ConvertToImage:      cfg.OutputFormat != "html",
		ConvertLatexToImage: cfg.OutputFormat == "docx",
		ScaleFactor:         cfg.ScaleFactor,
		PreprocessMath:      cfg.PreprocessMath,
	})
	if err != nil {
		return nil, err
	}
	fullHtml, err := s.htmlTemplater.ApplyHtmlTemplate([]byte(html), cfg.Template, ctx)
	if err != nil {
		return nil, err
	}

	switch cfg.OutputFormat {
	case "pdf":
		pdfContent, err := s.pandoc.HtmlToPdf(ctx, []byte(fullHtml), cfg.Template)
		if err != nil {
			return nil, err
		}

		if s.storage == nil {
			return nil, errors.New("storage service is not initialized")
		}
		pdfUploadResult, err := s.storage.Upload(ctx, pdfContent, "application/pdf")
		if err != nil {
			return nil, err
		}
		return &dto.RenderMarkdownResult{OutputUrl: pdfUploadResult.Link}, nil
	case "docx":
		if s.pandoc == nil {
			return nil, errors.New("pandoc is not initialized - DOCX conversion is not available")
		}
		docxContent, err := s.pandoc.HtmlToDocx(ctx, []byte(html), cfg.Template)
		if err != nil {
			return nil, err
		}

		if s.storage == nil {
			return nil, errors.New("storage service is not initialized")
		}
		docxUploadResult, err := s.storage.Upload(ctx, docxContent, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		if err != nil {
			return nil, err
		}
		return &dto.RenderMarkdownResult{OutputUrl: docxUploadResult.Link}, nil
	case "html":
		if s.storage == nil {
			return nil, errors.New("storage service is not initialized")
		}
		htmlUploadResult, err := s.storage.Upload(ctx, []byte(fullHtml), "text/html")
		if err != nil {
			return nil, err
		}
		return &dto.RenderMarkdownResult{Html: html, OutputUrl: htmlUploadResult.Link}, nil
	default:
		return nil, errors.Errorf("unsupported output format: %q", cfg.OutputFormat)
	}
}

// @Schemes
// @Security Bearer
// @Description extracts markdown from a document by its URL.
// @Tags run
// @Accept json
// @Produce json
// @Param data body dto.ReadabilityConfig true "run config"
// @Success 200 {object} dto.ExtractMarkdownResult
// @Router /api/extract-markdown [post]
func (s *Server) extractMarkdown(c service.HttpAdapter) error {
	ctx := c.Context()

	if result, ok := service.WithReadBody(ctx, s, c, "PDF parse", func(cfg *dto.ReadabilityConfig) (*dto.ExtractMarkdownResult, error) {
		cfg.MaxAttempts = lo.If(cfg.MaxAttempts != nil, cfg.MaxAttempts).Else(lo.ToPtr(1))
		cfg.Timeout = lo.If(cfg.Timeout == "", DefaultTimeout).Else(cfg.Timeout)
		configHash := cfg.Hash()
		ctx = s.Logger().WithValue(ctx, "configHash", configHash)
		ctx = s.Logger().WithValue(ctx, "config", cfg)
		res, err := retry.With(retry.Config[*dto.ExtractMarkdownResult]{
			Action: func() (*dto.ExtractMarkdownResult, error) {
				parseResult, err := s.doRunDocParse(ctx, cfg)
				if err != nil {
					return nil, err
				}
				output := markdown.ExtractMarkdownFromParsedPdf(*parseResult.Result.(*dto.ParsedPDF))
				return &dto.ExtractMarkdownResult{Markdown: output}, nil
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
		c.JSON(http.StatusOK, result)
	}
	return nil
}
