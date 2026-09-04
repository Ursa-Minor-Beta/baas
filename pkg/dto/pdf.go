package dto

// Shape follows the Open Layout Format (OLF) document model.

type ParsedPDF struct {
	FormatVersion string     `json:"format_version"`
	Id            string     `json:"id"`
	Title         string     `json:"title"`
	Blocks        []PDFBlock `json:"blocks"`
}

type PDFBlock struct {
	Id      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Type    string `json:"type"`
	Format  string `json:"format,omitempty"`
	Content any    `json:"content,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type ParseDocResult struct {
	Result        any `json:"result"`
	ProcessResult `json:",inline"`
}

type pdfToImagesOutputFormat string

const (
	Png  pdfToImagesOutputFormat = "png"
	Jpeg pdfToImagesOutputFormat = "jpeg"
)

type PdfToImagesConfig struct {
	PdfUrl       string  `json:"pdfUrl" yaml:"pdfUrl"`
	BatchSize    *int    `json:"batchSize,omitempty" yaml:"batchSize,omitempty"`
	Density      *int    `json:"density,omitempty" yaml:"density,omitempty"`
	OutputFormat *string `json:"outputFormat,omitempty" yaml:"outputFormat,omitempty"`
}

type PdfToImagesResult struct {
	ImageUrls []string `json:"imageUrls" yaml:"imageUrls"`
}
