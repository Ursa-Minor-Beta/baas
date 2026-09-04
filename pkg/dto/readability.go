package dto

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/go-shiori/go-readability"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
)

type parseOutputFormat string

const (
	Markdown parseOutputFormat = "markdown"
	Json     parseOutputFormat = "json"
)

type ReadabilityConfig struct {
	URL               string             `json:"URL" yaml:"URL"`                                                 // which URL to fetch (e.g. "https://en.wikipedia.org/Schema_migration")
	UserAgent         *string            `json:"userAgent,omitempty" yaml:"userAgent,omitempty"`                 // which User-Agent to set whilst fetching (default: "*")
	FallbackToBrowser *bool              `json:"fallbackToBrowser,omitempty" yaml:"fallbackToBrowser,omitempty"` // whether to fallback to browser if fetch didn't succeed
	ForceUseBrowser   *bool              `json:"forceUseBrowser,omitempty" yaml:"forceUseBrowser,omitempty"`     // whether to use browser first
	ForcePdf          *bool              `json:"forcePdf,omitempty" yaml:"forcePdf,omitempty"`                   // whether to convert docs to PDF before parsing
	ParseTableData    *bool              `json:"parseTableData,omitempty" yaml:"parseTableData,omitempty"`       // whether to extract table data from the document
	ParseOutputFormat *parseOutputFormat `json:"parseOutputFormat,omitempty" yaml:"parseOutputFormat,omitempty"` // specify output format for parsed documents
	CsvDelimiter      *string            `json:"csvDelimiter,omitempty" yaml:"csvDelimiter,omitempty"`           // specify delimiter for CSV output (default: ",")
	Timeout           string             `json:"timeout" yaml:"timeout"`                                         // overall timeout in duration format, e.g. `10s`, cannot exceed 60s
	UseRandomProxy    *bool              `json:"useRandomProxy,omitempty" yaml:"useRandomProxy,omitempty"`       // whether to use random proxy from the configured proxy pool (default: false)
	UseProxy          *string            `json:"useProxy,omitempty" yaml:"useProxy,omitempty"`                   // use specific proxy in format host:port (default: undefined)
	MaxAttempts       *int               `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`             // max amount of attempts to fetch/process (default: 3)
	ReturnFullHtml    *bool              `json:"returnFullHtml,omitempty" yaml:"returnFullHtml,omitempty"`       // whether to return raw HTML output from fetch in addition to readability article (default: false)
	ReturnPandoc      *string            `json:"returnPandoc,omitempty" yaml:"returnPandoc,omitempty"`           // specify one of supported pandoc formats to return HTML converted onto it in addition to readability article (default: false)
	EmbedResources    *bool              `json:"embedResources,omitempty" yaml:"embedResources,omitempty"`       // whether to embed resources (images, stylesheets) into the returned HTML (default: false)
	DoNotUseCache     *bool              `json:"doNotUseCache,omitempty" yaml:"doNotUseCache,omitempty"`         // do not use cache when processing, always fetch fresh data
	ForceMimeType     *string            `json:"forceMimeType,omitempty" yaml:"forceMimeType,omitempty"`         // force specific MIME type when processing (e.g. "application/pdf")
	IgnoreMimeType    *bool              `json:"ignoreMimetype,omitempty" yaml:"ignoreMimetype,omitempty"`       // ignore MIME type when processing
	Headers           map[string]string  `json:"headers,omitempty" yaml:"headers,omitempty"`                     // additional headers to send with the request
}

func (c *ReadabilityConfig) Hash() string {
	configBytes, _ := json.Marshal(c)
	md5Sum := md5.Sum(configBytes)
	return hex.EncodeToString(md5Sum[:])
}

type Article struct {
	Title         string     `json:"title" yaml:"title"`
	Byline        string     `json:"byline" yaml:"byline"`
	Content       string     `json:"content" yaml:"content"`
	TextContent   string     `json:"textContent" yaml:"textContent"`
	Length        int        `json:"length" yaml:"length"`
	Excerpt       string     `json:"excerpt" yaml:"excerpt"`
	SiteName      string     `json:"siteName" yaml:"siteName"`
	Image         string     `json:"image" yaml:"image"`
	Favicon       string     `json:"favicon" yaml:"favicon"`
	Language      string     `json:"language" yaml:"language"`
	PublishedTime *time.Time `json:"publishedTime,omitempty" yaml:"publishedTime"`
	ModifiedTime  *time.Time `json:"modifiedTime,omitempty" yaml:"modifiedTime"`
	RawContent    string     `json:"rawContent" yaml:"rawContent"`
}

func ToArticle(article readability.Article) Article {
	return Article{
		Title:         article.Title,
		Byline:        article.Byline,
		Content:       article.Content,
		TextContent:   article.TextContent,
		Length:        article.Length,
		Excerpt:       article.Excerpt,
		SiteName:      article.SiteName,
		Image:         article.Image,
		Favicon:       article.Favicon,
		Language:      article.Language,
		PublishedTime: article.PublishedTime,
		ModifiedTime:  article.ModifiedTime,
	}
}

type ReadabilityCachedResult struct {
	ID         string    `bson:"_id"`
	CachedAt   time.Time `bson:"cachedAt"`
	ResultJson string    `bson:"resultJson"`
}

type ProcessResult struct {
	UsedBrowser bool               `json:"usedBrowser" yaml:"usedBrowser"`                 // if browser was used while processing
	UsedProxy   string             `json:"usedProxy,omitempty" yaml:"usedProxy,omitempty"` // which proxy server was used for fetching
	Meta        service.ResultMeta `json:"meta" yaml:"meta"`                               // metadata related to processing
}

type ReadabilityResult struct {
	Article       Article             `json:"article" yaml:"article"`                           // result of readability processing
	RawContent    *string             `json:"rawContent,omitempty" yaml:"rawContent,omitempty"` // raw content of the fetched URL (if requested)
	Pandoc        *string             `json:"pandoc,omitempty" yaml:"pandoc,omitempty"`         // HTML content converted to Pandoc (if requested)
	Tables        []*ReadabilityTable `json:"tables,omitempty" yaml:"tables,omitempty"`         // extracted table data (if any)
	ProcessResult `json:",inline"`
}

type ReadabilityTable struct {
	Name    string   `json:"name,omitempty" yaml:"name,omitempty"`
	Headers []string `json:"headers" yaml:"headers"`
	Rows    [][]any  `json:"rows" yaml:"rows"`
}
