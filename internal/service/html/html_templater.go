package html

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const outBufferSize = 1024 * 1024 * 5 // 5 Mb buffer for outputs

type HtmlTemplater interface {
	ApplyHtmlTemplate(htmlBytes []byte, template *dto.HtmlTemplate, ctx context.Context) ([]byte, error)
}

type htmlTemplater struct {
	nodePath string
	log      logger.Logger
}

func NewHtmlTemplater(log logger.Logger, nodePath string) HtmlTemplater {
	return &htmlTemplater{
		log:      log,
		nodePath: nodePath,
	}
}

func (h *htmlTemplater) ApplyHtmlTemplate(htmlBytes []byte, t *dto.HtmlTemplate, ctx context.Context) ([]byte, error) {
	err, css := h.generateCss(t, ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate css")
	}

	htmlBytes = []byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<style>%s</style>
</head>
<body>
%s%s
</body>
</html>`, css, h.createHeaderHtml(t), string(htmlBytes)))
	return htmlBytes, nil
}

func (h *htmlTemplater) createHeaderHtml(t *dto.HtmlTemplate) string {
	if t == nil {
		return ""
	}
	if lo.FromPtr(t.HeaderLogo) == "" && lo.FromPtr(t.HeaderText) == "" && lo.FromPtr(t.CompanyName) == "" {
		return ""
	}

	templatePath := path.Join(os.Getenv("SCRIPTS_DIR"), "html_template.html")
	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		return ""
	}
	templateStr := string(templateBytes)

	defaultFontSize := 14.0
	fontSize := lo.FromPtrOr(t.FontSize, defaultFontSize)

	headerHeight := lo.FromPtrOr(t.HeaderHeight, 60.0)
	headerLogo := ""
	if lo.FromPtr(t.HeaderLogo) != "" {
		headerLogo = fmt.Sprintf(`<img src="%s"
			style="max-width: %.2fpx; max-height: %.2fpx; object-fit: contain"
		/>`, template.HTMLEscapeString(lo.FromPtr(t.HeaderLogo)), lo.FromPtrOr(t.HeaderLogoWidth, 120), lo.FromPtrOr(t.HeaderLogoHeight, 40))
	}
	companyName := template.HTMLEscapeString(lo.FromPtr(t.CompanyName))
	companyNameFontSize := fontSize + 4
	companyTagLine := template.HTMLEscapeString(lo.FromPtr(t.CompanyTagline))
	companyTagLineFontSize := fontSize - 2
	primaryColor := template.HTMLEscapeString(lo.FromPtr(t.PrimaryColor))
	secondaryColor := template.HTMLEscapeString(lo.FromPtr(t.SecondaryColor))
	documentTitle := template.HTMLEscapeString(lo.FromPtr(t.DocumentTitle))
	documentTitleFontSize := fontSize + 2
	exportDate := time.Now().Format("January 2, 2006")
	exportDateFontSize := fontSize - 2
	headerText := template.HTMLEscapeString(lo.FromPtr(t.HeaderText))
	headerTextFontSize := fontSize - 1

	templateStr = strings.ReplaceAll(templateStr, "{headerHeight}", fmt.Sprintf("%.2fpx", headerHeight))
	templateStr = strings.ReplaceAll(templateStr, "{headerLogo}", headerLogo)
	templateStr = strings.ReplaceAll(templateStr, "{companyName}", companyName)
	templateStr = strings.ReplaceAll(templateStr, "{companyNameFontSize}", fmt.Sprintf("%.2fpx", companyNameFontSize))
	templateStr = strings.ReplaceAll(templateStr, "{companyTagline}", companyTagLine)
	templateStr = strings.ReplaceAll(templateStr, "{companyTaglineFontSize}", fmt.Sprintf("%.2fpx", companyTagLineFontSize))
	templateStr = strings.ReplaceAll(templateStr, "{primaryColor}", primaryColor)
	templateStr = strings.ReplaceAll(templateStr, "{secondaryColor}", secondaryColor)
	templateStr = strings.ReplaceAll(templateStr, "{documentTitle}", documentTitle)
	templateStr = strings.ReplaceAll(templateStr, "{documentTitleFontSize}", fmt.Sprintf("%.2fpx", documentTitleFontSize))
	templateStr = strings.ReplaceAll(templateStr, "{exportDate}", exportDate)
	templateStr = strings.ReplaceAll(templateStr, "{exportDateFontSize}", fmt.Sprintf("%.2fpx", exportDateFontSize))
	templateStr = strings.ReplaceAll(templateStr, "{headerText}", headerText)
	templateStr = strings.ReplaceAll(templateStr, "{headerTextFontSize}", fmt.Sprintf("%.2fpx", headerTextFontSize))

	return templateStr
}

func (h *htmlTemplater) generateCss(t *dto.HtmlTemplate, ctx context.Context) (error, string) {
	tmpDir, err := os.MkdirTemp("", "markdown_template_css_")
	if err != nil {
		return errors.Wrapf(err, "failed to create temp dir"), ""
	}
	defer func(name string) {
		err := os.RemoveAll(name)
		if err != nil {
			h.log.Errorf(h.log.WithValue(ctx, "error", err.Error()), "failed to remove temporary dir")
		}
	}(tmpDir)

	inputCssPath := path.Join(os.Getenv("SCRIPTS_DIR"), "pdf_export_template.css")
	outputCssPath := path.Join(tmpDir, "output.css")

	var templateJson []byte
	if t != nil {
		templateJson, err = json.Marshal(t)
		if err != nil {
			return errors.Wrapf(err, "failed to marshal template to JSON"), ""
		}
	} else {
		templateJson = []byte("{}")
	}

	preprocessCssScriptPath := filepath.Join(os.Getenv("SCRIPTS_DIR"), "template_css.mjs")
	buf := bytes.NewBuffer(make([]byte, outBufferSize))
	cmdArgs := []string{preprocessCssScriptPath, inputCssPath, outputCssPath, "--template", string(templateJson)}
	cmd := exec.CommandContext(ctx, h.nodePath, cmdArgs...)
	cmd.Stdout = buf
	cmd.Stderr = buf
	err = cmd.Run()

	executedCommand := strings.Join(cmdArgs, " ")
	debugOutput := strings.TrimSpace(string(bytes.Trim(buf.Bytes(), "\x00")))
	if err != nil {
		return errors.Wrapf(err, "failed to preprocess css with command %q \n node's output: %q", executedCommand, debugOutput), ""
	}

	outputCss, err := os.ReadFile(outputCssPath)
	if err != nil {
		return errors.Wrapf(err, "failed to read output css file"), ""
	}
	if t != nil && t.CustomCss != nil {
		outputCss = append(outputCss, []byte("\n"+*t.CustomCss)...)
	}
	return nil, strings.TrimSpace(string(outputCss))
}
