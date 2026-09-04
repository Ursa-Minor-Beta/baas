package pdf

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	e "github.com/Ursa-Minor-Beta/baas/internal/exec"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const pdfToCairoBatchSize = 10

type cleanupFunc func()

type PdfToImagesConverter interface {
	Convert(ctx context.Context, cfg *dto.PdfToImagesConfig) ([]string, cleanupFunc, error)
}

type pdfToImagesConverter struct {
	log                      logger.Logger
	qpdfExecutablePath       string
	pdftocairoExecutablePath string
}

func NewPdfToImagesConverter(log logger.Logger) (PdfToImagesConverter, error) {
	qpdfExecutablePath, err := e.Lookup("qpdf")
	if err != nil {
		return nil, fmt.Errorf("failed to locate qpdf executable: %w", err)
	}

	pdftocairoExecutablePath, err := e.Lookup("pdftocairo")
	if err != nil {
		return nil, fmt.Errorf("failed to locate pdftocairo executable: %w", err)
	}

	return &pdfToImagesConverter{
		log:                      log,
		qpdfExecutablePath:       qpdfExecutablePath,
		pdftocairoExecutablePath: pdftocairoExecutablePath,
	}, nil
}

func (p *pdfToImagesConverter) Convert(ctx context.Context, cfg *dto.PdfToImagesConfig) ([]string, cleanupFunc, error) {
	tempPdfFile, err := os.CreateTemp("", "pdf_input_*.pdf")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp PDF file: %w", err)
	}
	defer func(name string) {
		if err := os.Remove(name); err != nil {
			p.log.Errorf(p.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary PDF file")
		}
	}(tempPdfFile.Name())

	tempOutputDir, err := os.MkdirTemp("", "pdf_to_images_output_*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp output directory: %w", err)
	}
	var cleanup cleanupFunc = func() {
		if err := os.RemoveAll(tempOutputDir); err != nil {
			p.log.Errorf(p.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary output directory")
		}
	}

	// fetch file from remote URL
	resp, err := http.Get(cfg.PdfUrl)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to fetch PDF from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, cleanup, fmt.Errorf("failed to fetch PDF from URL: status %s", resp.Status)
	}

	pdfResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to read PDF response body: %w", err)
	}

	if _, err := tempPdfFile.Write(pdfResp); err != nil {
		return nil, cleanup, fmt.Errorf("failed to write PDF content to temp file: %w", err)
	}

	if err := tempPdfFile.Close(); err != nil {
		return nil, cleanup, fmt.Errorf("failed to close temp PDF file: %w", err)
	}

	// calculate number of pages in PDF via qpdf
	numPages, err := p.getNumPagesInPdf(ctx, tempPdfFile.Name())
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to get number of pages in PDF: %w", err)
	}

	imagePaths := make([]string, numPages)

	batchSize := lo.FromPtrOr(cfg.BatchSize, pdfToCairoBatchSize)

	// process pages in batches
	for startPage := 1; startPage <= numPages; startPage += batchSize {
		endPage := min(startPage+batchSize-1, numPages)
		batchSize := endPage - startPage + 1

		// run pdftocairo for each page in the batch in parallel
		type pageResult struct {
			pageNum   int
			imagePath string
			err       error
		}
		resultCh := make(chan pageResult, batchSize)

		var formatOpt string
		var formatExt string
		switch lo.FromPtrOr(cfg.OutputFormat, "png") {
		case "png":
			formatOpt = "-png"
			formatExt = "png"
		case "jpeg":
			formatOpt = "-jpeg"
			formatExt = "jpg"
		default:
			return nil, cleanup, fmt.Errorf("unsupported output format: %s", lo.FromPtrOr(cfg.OutputFormat, "png"))
		}

		for pageNum := startPage; pageNum <= endPage; pageNum++ {
			go func(page int) {
				paddingSize := len(fmt.Sprintf("%d", numPages))
				paddedPageNum := fmt.Sprintf("%0*d", paddingSize, page)
				outputImagePath := fmt.Sprintf("%s/out-%s.%s", tempOutputDir, paddedPageNum, formatExt)
				cmdArgs := []string{
					formatOpt,
					"-f", fmt.Sprintf("%d", page),
					"-l", fmt.Sprintf("%d", page),
				}
				if cfg.Density != nil {
					cmdArgs = append(cmdArgs, "-r", fmt.Sprintf("%d", *cfg.Density))
				}
				cmdArgs = append(cmdArgs, tempPdfFile.Name(), fmt.Sprintf("%s/out", tempOutputDir))
				cmd := exec.CommandContext(ctx, p.pdftocairoExecutablePath, cmdArgs...)
				output, err := cmd.CombinedOutput()
				if err != nil {
					resultCh <- pageResult{pageNum: page, err: fmt.Errorf("failed to execute pdftocairo command: %w, output: %s", err, string(output))}
					return
				}
				resultCh <- pageResult{pageNum: page, imagePath: outputImagePath, err: nil}
			}(pageNum)
		}

		// collect results
		for range batchSize {
			res := <-resultCh
			if res.err != nil {
				return nil, cleanup, res.err
			}
			imagePaths[res.pageNum-1] = res.imagePath
		}
	}

	return imagePaths, cleanup, nil
}

func (p *pdfToImagesConverter) getNumPagesInPdf(ctx context.Context, pdfPath string) (int, error) {
	cmdArgs := []string{"--show-npages", pdfPath}
	cmd := exec.CommandContext(ctx, p.qpdfExecutablePath, cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to execute qpdf command: %w, output: %s", err, string(output))
	}

	var numPages int
	_, err = fmt.Sscanf(string(output), "%d", &numPages)
	if err != nil {
		return 0, fmt.Errorf("failed to parse qpdf output: %w, output: %s", err, string(output))
	}

	return numPages, nil
}
