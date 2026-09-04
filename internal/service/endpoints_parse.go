package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry"

	"github.com/Ursa-Minor-Beta/baas/internal/service/markdown"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// @Schemes
// @Security Bearer
// @Description parses document by its URL and returns the parsed document.
// @Tags run
// @Accept json
// @Produce json
// @Param data body dto.ReadabilityConfig true "run config"
// @Success 200 {object} dto.ParseDocResult
// @Router /api/parse [post]
func (s *Server) parseDocumentEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	if result, ok := service.WithReadBody(ctx, s, c, "PDF parse", func(cfg *dto.ReadabilityConfig) (*dto.ParseDocResult, error) {
		cfg.MaxAttempts = lo.If(cfg.MaxAttempts != nil, cfg.MaxAttempts).Else(lo.ToPtr(1))
		cfg.Timeout = lo.If(cfg.Timeout == "", DefaultTimeout).Else(cfg.Timeout)
		configHash := cfg.Hash()
		ctx = s.Logger().WithValue(ctx, "configHash", configHash)
		ctx = s.Logger().WithValue(ctx, "config", cfg)
		res, err := retry.With(retry.Config[*dto.ParseDocResult]{
			Action: func() (*dto.ParseDocResult, error) {
				parseResult, err := s.doRunDocParse(ctx, cfg)
				if err != nil {
					return nil, err
				}
				switch lo.FromPtr(cfg.ParseOutputFormat) {
				case "markdown":
					markdown := markdown.ExtractMarkdownFromParsedPdf(*parseResult.Result.(*dto.ParsedPDF))
					return &dto.ParseDocResult{
						Result:        markdown,
						ProcessResult: parseResult.ProcessResult,
					}, nil
				case "json", "":
					return parseResult, nil
				default:
					return nil, errors.Errorf("unsupported output format: %q", lo.FromPtr(cfg.ParseOutputFormat))
				}
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

// @Schemes
// @Security Bearer
// @Description converts PDF document by its URL to images and returns the URLs of the images.
// @Tags run
// @Accept json
// @Produce json
// @Param data body dto.PdfToImagesConfig true "run config"
// @Success 200 {object} dto.PdfToImagesResult
// @Router /api/pdf-to-images [post]
func (s *Server) pdfToImagesEndpoint(c service.HttpAdapter) error {
	ctx := c.Context()

	if result, ok := service.WithReadBody(ctx, s, c, "PDF to images", func(cfg *dto.PdfToImagesConfig) (*dto.PdfToImagesResult, error) {
		return s.doPdfToImages(ctx, cfg)
	}); ok && result != nil {
		c.JSON(http.StatusOK, result)
	}

	return nil
}

func (s *Server) doPdfToImages(ctx context.Context, cfg *dto.PdfToImagesConfig) (*dto.PdfToImagesResult, error) {
	imagePaths, cleanup, err := s.pdfToImagesConverter.Convert(ctx, cfg)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert PDF to images")
	}

	var contentType string
	switch lo.FromPtrOr(cfg.OutputFormat, "png") {
	case "png":
		contentType = "image/png"
	case "jpeg":
		contentType = "image/jpeg"
	default:
		return nil, errors.Errorf("unsupported output format: %q", lo.FromPtrOr(cfg.OutputFormat, "png"))
	}

	imageUrls := make([]string, 0, len(imagePaths))
	for _, imagePath := range imagePaths {
		imageBytes, err := os.ReadFile(imagePath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read image %q", imagePath)
		}
		uploadResult, err := s.storage.Upload(ctx, imageBytes, contentType)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to upload image %q to storage", imagePath)
		}
		imageUrls = append(imageUrls, uploadResult.Link)
	}
	return &dto.PdfToImagesResult{ImageUrls: imageUrls}, nil
}

func (s *Server) doRunDocParse(ctx context.Context, cfg *dto.ReadabilityConfig) (*dto.ParseDocResult, error) {
	if cfg == nil {
		return nil, errors.Errorf("config is nil")
	}
	s.Logger().Infof(ctx, "determining document format of request...")
	docFmt, _, err := determineFmtType(ctx, cfg)
	if err != nil || docFmt == nil {
		return nil, errors.Errorf("failed to determine file format from URL %q", cfg.URL)
	}
	s.Logger().Infof(ctx, "fetching url...")
	var pdfBytes []byte
	usedBrowser := false
	var resp *http.Response
	if !lo.FromPtr(cfg.ForceUseBrowser) {
		resp, err = FetchFromURL(ctx, cfg)
		if err == nil && resp != nil {
			pdfBytes = readBytes(resp.Body)
		} else if resp == nil {
			err = errors.Errorf("response is nil")
		}
	}
	if err != nil || lo.FromPtr(cfg.ForceUseBrowser) {
		if err != nil {
			ctx = s.Logger().WithValue(ctx, "error", err.Error())
		}
		if cfg.FallbackToBrowser == nil || lo.FromPtr(cfg.FallbackToBrowser) || lo.FromPtr(cfg.ForceUseBrowser) {
			s.Logger().Infof(ctx, "processed URL with plain fetch resulted in %v, trying with chrome...", err)
			usedBrowser = true
			pdfBytes, err = s.docDownloadWithChrome(ctx, cfg)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to download file with chrome")
			}
		}
	}
	if lo.FromPtr(cfg.ForcePdf) {
		s.Logger().Infof(ctx, "converting document to PDF before parsing...")
		pdfBytes, err = s.pandoc.DocToPdf(ctx, pdfBytes, string(*docFmt))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to convert document to PDF")
		}
		docFmt = lo.ToPtr(fmtPdf)
	}
	result, err := s.pdfParser.Parse(ctx, pdfBytes, string(*docFmt))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse PDF")
	}

	return &dto.ParseDocResult{
		Result: result,
		ProcessResult: dto.ProcessResult{
			UsedBrowser: usedBrowser,
			UsedProxy:   lo.FromPtr(cfg.UseProxy),
		},
	}, nil
}

// nolint: unused
func readBytes(stream io.Reader) []byte {
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(stream)
	return buf.Bytes()
}

func (s *Server) docDownloadWithChrome(ctx context.Context, cfg *dto.ReadabilityConfig) ([]byte, error) {
	s.Logger().Infof(ctx, "Trying to fetch with Chrome...")
	opts := dto.BrowserOpts{}
	if cfg.UserAgent != nil {
		opts.UserAgent = lo.FromPtr(cfg.UserAgent)
	}
	if cfg.UseProxy != nil {
		opts.UseProxy = s.proxy.Host()
	}
	opts.EnableExtensions = []dto.ChromeExtensionMode{dto.EnableExtensionsReadability, dto.EnableExtensionsBaas}
	opts.Program = fmt.Sprintf(`
var resp = navigateResponse('%s'); 
waitReady('body');

if (resp === undefined) {
   reload();
   waitReady('body');
}
var startTimeout = '10s';
if (evaluateJS("document.querySelector('#footer-text').innerText") == 'Performance & security by Cloudflare') {
  startTimeout = '20s';
}
if (!waitFileDownloadStarted(startTimeout)) {
  throw 'File download did not start in ' + startTimeout;
}
if (!waitFileDownload('10s')) {
  throw 'File was not downloaded in 10s';
}
`, cfg.URL)
	res, err := s.browser.Run(ctx, opts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch url %q with chrome", cfg.URL)
	}
	if res.DownloadedFile == nil {
		return nil, errors.Errorf("failed to download file url %q: %q", cfg.URL, lo.FromPtr(res.Error))
	}
	return res.DownloadedFile, nil
}
