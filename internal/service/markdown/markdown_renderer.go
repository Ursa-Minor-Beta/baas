package markdown

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

type RenderOpts struct {
	ConvertToImage      bool
	ConvertLatexToImage bool
	ScaleFactor         *float32
	PreprocessMath      *bool
}

type Renderer interface {
	Render(ctx context.Context, markdown string, opts RenderOpts) (string, error)
}

type renderer struct {
	nodePath                 string
	renderMarkdownScriptPath string
	log                      logger.Logger
}

func NewMarkdownRenderer(log logger.Logger, nodePath, renderMarkdownScriptPath string) Renderer {
	return &renderer{
		log:                      log,
		nodePath:                 nodePath,
		renderMarkdownScriptPath: renderMarkdownScriptPath,
	}
}

func (r *renderer) Render(ctx context.Context, markdown string, opts RenderOpts) (string, error) {
	tempMarkdownFile, err := os.CreateTemp("", "markdown_input_*.md")
	if err != nil {
		return "", fmt.Errorf("failed to create temp markdown file: %w", err)
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			r.log.Errorf(r.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary markdown file")
		}
	}(tempMarkdownFile.Name())

	tempHtmlFile, err := os.CreateTemp("", "markdown_output_*.html")
	if err != nil {
		return "", fmt.Errorf("failed to create temp html file: %w", err)
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			r.log.Errorf(r.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary html file")
		}
	}(tempHtmlFile.Name())

	if _, err := tempMarkdownFile.WriteString(markdown); err != nil {
		return "", fmt.Errorf("failed to write markdown content to temp file: %w", err)
	}

	if err := tempMarkdownFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp markdown file: %w", err)
	}

	cmdArgs := []string{r.renderMarkdownScriptPath, tempMarkdownFile.Name(), tempHtmlFile.Name()}
	if opts.ConvertToImage {
		cmdArgs = append(cmdArgs, "--convert-to-image")
	}
	if opts.ConvertLatexToImage {
		cmdArgs = append(cmdArgs, "--convert-latex-to-image")
	}
	if lo.FromPtr(opts.PreprocessMath) {
		cmdArgs = append(cmdArgs, "--preprocess-math")
	}
	if lo.FromPtr(opts.ScaleFactor) != 0 {
		cmdArgs = append(cmdArgs, "--scale-factor", fmt.Sprintf("%f", lo.FromPtr(opts.ScaleFactor)))
	}
	cmd := exec.CommandContext(ctx, r.nodePath, cmdArgs...)

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to execute markdown rendering script: %w", err)
	}

	renderedContent, err := os.ReadFile(tempHtmlFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read rendered HTML content: %w", err)
	}

	return string(renderedContent), nil
}
