package dto

// ParseToMarkdownResult represents the result of document-to-markdown conversion
type ParseToMarkdownResult struct {
	Markdown      string `json:"markdown" yaml:"markdown"` // markdown output
	ProcessResult `json:",inline"`
}
