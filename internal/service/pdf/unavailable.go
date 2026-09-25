package pdf

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Ursa-Minor-Beta/baas/internal/doctools"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// parserModules are what scripts/mupdf.py imports at the top level; any one
// missing makes every parse fail.
var parserModules = []string{"pymupdf", "PIL", "openpyxl"}

// ProbeParser checks that the parse script exists and that its interpreter
// can import the modules it needs, so a slim image reports that once at boot
// instead of returning a Python traceback on every request.
func ProbeParser(ctx context.Context, pythonPath, scriptPath string) error {
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("parse script %s: %w", scriptPath, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, pythonPath, "-c", "import "+strings.Join(parserModules, ", "))
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s cannot import %s: %w: %s",
			pythonPath, strings.Join(parserModules, ", "), err, lastLine(out.String()))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

type unavailableParser struct {
	reason error
}

// NewUnavailableParser answers every Parse with the reason the parser could
// not start.
func NewUnavailableParser(reason error) Parser {
	return &unavailableParser{reason: reason}
}

func (p *unavailableParser) Parse(context.Context, []byte, string) (*dto.ParsedPDF, error) {
	return nil, doctools.Unavailable(p.reason)
}

type unavailablePdfToImagesConverter struct {
	reason error
}

// NewUnavailablePdfToImagesConverter answers every Convert with the reason the
// converter could not start.
func NewUnavailablePdfToImagesConverter(reason error) PdfToImagesConverter {
	return &unavailablePdfToImagesConverter{reason: reason}
}

func (c *unavailablePdfToImagesConverter) Convert(context.Context, *dto.PdfToImagesConfig) ([]string, cleanupFunc, error) {
	return nil, nil, doctools.Unavailable(c.reason)
}
