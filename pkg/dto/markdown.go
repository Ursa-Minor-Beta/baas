package dto

type outputFormat string

const (
	Html outputFormat = "html"
	Pdf  outputFormat = "pdf"
	Docx outputFormat = "docx"
)

type RenderMarkdownConfig struct {
	Markdown       string        `json:"markdown" yaml:"markdown"`
	OutputFormat   outputFormat  `json:"outputFormat,omitempty" yaml:"outputFormat,omitempty"`
	Template       *HtmlTemplate `json:"template,omitempty" yaml:"template,omitempty"`
	PreprocessMath *bool         `json:"preprocessMath,omitempty" yaml:"preprocessMath,omitempty"`
	ScaleFactor    *float32      `json:"scaleFactor,omitempty" yaml:"scaleFactor,omitempty"`
}

type RenderMarkdownResult struct {
	Html      string `json:"html,omitempty" yaml:"html,omitempty"`
	OutputUrl string `json:"outputUrl,omitempty" yaml:"outputUrl,omitempty"`
}

type ExtractMarkdownResult struct {
	Markdown string `json:"markdown" yaml:"markdown"`
}
