package pdf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

type Parser interface {
	Parse(ctx context.Context, pdfContent []byte, docFmt string) (*dto.ParsedPDF, error)
}

type parser struct {
	pythonPath         string
	openAIAPIKey       string
	openAIModel        string
	openAIMinTokens    int
	openAIMaxTokens    int
	parsePDFScriptPath string
	log                logger.Logger
}

func NewPDFParser(log logger.Logger, pythonPath, openAIAPIKey, openAIModel string, openAIMinTokens, openAIMaxTokens int, parsePDFScriptPath string) Parser {
	return &parser{
		log:                log,
		pythonPath:         pythonPath,
		openAIAPIKey:       openAIAPIKey,
		openAIModel:        openAIModel,
		openAIMinTokens:    openAIMinTokens,
		openAIMaxTokens:    openAIMaxTokens,
		parsePDFScriptPath: parsePDFScriptPath,
	}
}

func (p *parser) Parse(ctx context.Context, pdfContent []byte, docFmt string) (*dto.ParsedPDF, error) {
	if len(pdfContent) == 0 {
		return nil, fmt.Errorf("empty PDF content provided")
	}

	// Create a temporary file for the PDF input
	tempPDFFile, err := os.CreateTemp("", fmt.Sprintf("pdf_input_*.%s", docFmt))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp PDF file: %w", err)
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			p.log.Errorf(p.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary PDF file")
		}
	}(tempPDFFile.Name())

	// Write the PDF content to the temporary file
	if _, err := tempPDFFile.Write(pdfContent); err != nil {
		return nil, fmt.Errorf("failed to write PDF content to temp file: %w", err)
	}
	if err := tempPDFFile.Sync(); err != nil {
		return nil, fmt.Errorf("failed to sync temp PDF file: %w", err)
	}
	if err := tempPDFFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp PDF file: %w", err)
	}

	// Verify that the file is not empty
	fileInfo, err := os.Stat(tempPDFFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to get temp PDF file info: %w", err)
	}
	if fileInfo.Size() == 0 {
		return nil, fmt.Errorf("temp PDF file is empty")
	}

	// Create a temporary file for the JSON output
	tempJSONFile, err := os.CreateTemp("", "pdf_parse_*.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp JSON file: %w", err)
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			p.log.Errorf(p.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary JSON file")
		}
	}(tempJSONFile.Name())

	// Prepare the command to run the Python script
	cmd := exec.CommandContext(ctx, p.pythonPath, p.parsePDFScriptPath, "-i", tempPDFFile.Name(), "-o", tempJSONFile.Name())

	// Set environment variables for the Python script
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("OPENAI_API_KEY=%s", p.openAIAPIKey),
		fmt.Sprintf("OPENAI_MODEL=%s", p.openAIModel),
		fmt.Sprintf("OPENAI_MIN_TOKENS=%d", p.openAIMinTokens),
		fmt.Sprintf("OPENAI_MAX_TOKENS=%d", p.openAIMaxTokens),
	)

	// Run the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute Python script: %w\nOutput: %s", err, string(output))
	}

	// Read the JSON output file
	jsonData, err := os.ReadFile(tempJSONFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON output: %w", err)
	}

	// Unmarshal the JSON data into dto.ParsedPDF
	var result dto.ParsedPDF
	err = json.Unmarshal(jsonData, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &result, nil
}
