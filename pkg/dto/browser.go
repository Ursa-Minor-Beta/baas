package dto

import (
	"encoding/json"
	"regexp"

	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
)

type ControlConfig struct {
	SessionID string `json:"sessionID" yaml:"sessionID"` // sessionID to use when running async requests (must be unique)
}

type Config struct {
	Browser        BrowserOpts `json:"browser" yaml:"browser"`
	SessionID      *string     `json:"sessionID" yaml:"sessionID"`                               // sessionID to use when running async requests (must be unique)
	MaxAttempts    *int        `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`       // max amount of attempts to fetch/process (default: 3)
	UseRandomProxy *bool       `json:"useRandomProxy,omitempty" yaml:"useRandomProxy,omitempty"` // whether to use random proxy from the configured proxy pool (default: true)
	Hold           *bool       `json:"hold" yaml:"hold"`                                         // tells baas to hold the session and send SSE events instead of just starting and responding immediately (default: true)
}

func (c *Config) Sanitized() Config {
	return Config{
		Browser:        c.Browser.Sanitized(),
		MaxAttempts:    c.MaxAttempts,
		UseRandomProxy: c.UseRandomProxy,
		SessionID:      c.SessionID,
	}
}

type ChromeExtensionMode string

const (
	EnableExtensionsReadability ChromeExtensionMode = "readability"
	EnableExtensionsBaas        ChromeExtensionMode = "baas"
)

type ChromeExtensionManifest struct {
	DirPath      string                `json:"dir_path"`
	BaasEnableOn []ChromeExtensionMode `json:"baas_enable_on"`
}

type Result struct {
	Result    *BrowserResponse   `json:"result" yaml:"result"`                           // result returned by browser
	UsedProxy string             `json:"usedProxy,omitempty" yaml:"usedProxy,omitempty"` // which proxy server was used for fetching
	Meta      service.ResultMeta `json:"meta" yaml:"meta"`                               // metadata related to processing
}

type BrowserOpts struct {
	ReturnScreenshot        *bool             `json:"returnScreenshot" default:"false"`                        // whether to return screenshot after execution
	UserAgent               string            `json:"userAgent" default:""`                                    // use user-agent (default: undefined)
	UseProxy                *string           `json:"useProxy" default:""`                                     // use specific proxy server (default: undefined)
	Cookies                 []BrowserCookie   `json:"cookies"`                                                 // cookies to set before executing actions
	Program                 string            `json:"program" required:"true"`                                 // program to run (required)
	Secrets                 map[string]string `json:"secrets" required:"false"`                                // program secrets to use (values can be obtained as getSecret('name'))
	Values                  map[string]string `json:"values" required:"false"`                                 // program values to use (values can be obtained as getValue('name'))
	WaitForFileDownload     *bool             `json:"waitForFileDownload" required:"false" default:"false"`    // whether to wait until file is downloaded
	Timeout                 string            `json:"timeout" example:"60s" default:"60s"`                     // duration string in go duration format (e.g.: 10s)
	Width                   *int              `json:"width" example:"1920" default:"1920"`                     // width of the browser window
	Height                  *int              `json:"height" example:"1080" default:"1080"`                    // height of the browser window
	OperationTimeout        *string           `json:"operationTimeout" example:"20s" default:"20s"`            // timeout to execute a single command (default: 20s)
	ErrorOnOperationTimeout *bool             `json:"errorOnOperationTimeout" required:"false" default:"true"` // whether to return error when a single operation times out (default: true)
	Headers                 map[string]string `json:"headers"`                                                 // additional headers to set before executing actions

	LlmClient                 *string `json:"llmClient" yaml:"llmClient" required:"false"`                // which LLM client to use
	OllamaApiKey              *string `json:"ollamaApiKey" yaml:"ollamaApiKey" required:"false"`          // API key for Ollama LLM client
	OllamaUrl                 *string `json:"ollamaUrl" yaml:"ollamaUrl" required:"false"`                // URL for Ollama LLM client
	OpenaiToken               *string `json:"openaiToken" yaml:"openaiToken" required:"false"`            // token for OpenAI LLM client
	OpenaiOrganization        *string `json:"openaiOrganization" yaml:"openaiOrganization"`               // organization for OpenAI LLM client
	OpenaiModel               *string `json:"openaiModel" yaml:"openaiModel"`                             // model for OpenAI LLM client
	AzureOpenaiEndpoint       *string `json:"azureOpenaiEndpoint" yaml:"azureOpenaiEndpoint"`             // endpoint for Azure OpenAI LLM client
	AzureOpenaiKey            *string `json:"azureOpenaiKey" yaml:"azureOpenaiKey"`                       // API key for Azure OpenAI LLM client
	AzureOpenaiDeploymentName *string `json:"azureOpenaiDeploymentName" yaml:"azureOpenaiDeploymentName"` // deployment name for Azure OpenAI LLM client
	AzureOpenaiApiVersion     *string `json:"azureOpenaiApiVersion" yaml:"azureOpenaiApiVersion"`         // API version for Azure OpenAI LLM client

	EnableExtensions []ChromeExtensionMode `json:"-" yaml:"-"`

	SessionID string `json:"-"`
}

func (o *BrowserOpts) Sanitized() BrowserOpts {
	bytesOut, _ := json.Marshal(o)
	optsCopy := BrowserOpts{}
	_ = json.Unmarshal(bytesOut, &optsCopy)
	optsCopy.Secrets = nil
	optsCopy.Cookies = nil
	return optsCopy
}

type BrowserMessageIn struct {
	SessionID               string            `json:"sessionID" required:"true"`                               // sessionID to send event to
	RequestID               string            `json:"requestID"`                                               // ID of the current request (used for internal purposes)
	Program                 string            `json:"program" required:"true"`                                 // program to run (required)
	Secrets                 map[string]string `json:"secrets" required:"false"`                                // program secrets to use (values can be obtained as getSecret('name'))
	Values                  map[string]string `json:"values" required:"false"`                                 // program values to use (values can be obtained as getValue('name'))
	Timeout                 string            `json:"timeout" example:"10s" default:"10s"`                     // timeout to process message
	OperationTimeout        *string           `json:"operationTimeout" example:"20s" default:"20s"`            // timeout to execute a single command (default: 20s)
	StopSession             *bool             `json:"stopSession" example:"true" default:"false"`              // tells browser to stop session
	ErrorOnOperationTimeout *bool             `json:"errorOnOperationTimeout" required:"false" default:"true"` // whether to return error when a single operation times out (default: true)
}

var llmLoginRegexp = regexp.MustCompile(`llmLogin\([^)]+\)`)

func (i *BrowserMessageIn) Sanitized() any {
	bytesIn, _ := json.Marshal(i)
	inCopy := BrowserMessageIn{}
	_ = json.Unmarshal(bytesIn, &inCopy)
	inCopy.Secrets = nil
	// TODO temporary hack to avoid secrets being leaked
	inCopy.Program = llmLoginRegexp.ReplaceAllString(inCopy.Program, "llmLogin(<secret>,<secret>)")
	return inCopy
}

type BrowserMessageOut struct {
	Timestamp          string             `json:"timestamp" required:"true"`            // timestamp of the response
	SessionID          string             `json:"sessionID" required:"true"`            // sessionID to send event to
	RequestID          string             `json:"requestID"`                            // ID of the current request (used for internal purposes)
	Meta               service.ResultMeta `json:"meta" yaml:"meta"`                     // metadata related to processing
	GenerationInfo     map[string]any     `json:"generationInfo" yaml:"generationInfo"` // metadata related to generation via LLM
	Error              string             `json:"error,omitempty" yaml:"error"`         // error happened when running program
	Value              any                `json:"value,omitempty" yaml:"value"`         // return value
	Screenshots        map[string][]byte  `json:"screenshots,omitempty"`
	Log                []string           `json:"log,omitempty"`
	DownloadedFile     []byte             `json:"downloadedFile,omitempty"`
	DownloadedFileName string             `json:"downloadedFileName,omitempty"`
	OutHTML            string             `json:"outHtml"`
	ReadabilityArticle *Article           `json:"readabilityArticle"`
}

func (o *BrowserMessageOut) Sanitized() BrowserMessageOut {
	bytesOut, _ := json.Marshal(o)
	outCopy := BrowserMessageOut{}
	_ = json.Unmarshal(bytesOut, &outCopy)
	if len(outCopy.DownloadedFile) > 0 {
		outCopy.DownloadedFile = []byte("sanitized")
	}
	for k := range lo.Assign(outCopy.Screenshots) {
		outCopy.Screenshots[k] = []byte("sanitized")
	}
	return outCopy
}

type BrowserCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
}

type BrowserResponse struct {
	OutHTML            string            `json:"outHtml"`
	Value              any               `json:"value,omitempty"`
	Screenshot         []byte            `json:"screenshot,omitempty"`
	Screenshots        map[string][]byte `json:"screenshots,omitempty"`
	DownloadedFile     []byte            `json:"downloadedFile,omitempty"`
	Cookies            []BrowserCookie   `json:"cookies"`
	Log                []string          `json:"log,omitempty"`
	URL                string            `json:"url"`
	Error              *string           `json:"error"`
	GenerationInfo     map[string]any    `json:"generationInfo" yaml:"generationInfo"` // metadata related to generation via LLM
	SessionID          string            `json:"sessionID,omitempty"`                  // sessionID of the browser
	ReadabilityArticle *Article          `json:"readabilityArticle"`
}

type XYCoords struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type ElementPosition struct {
	TopLeft     XYCoords `json:"topLeft"`
	TopRight    XYCoords `json:"topRight"`
	BottomRight XYCoords `json:"bottomRight"`
	BottomLeft  XYCoords `json:"bottomLeft"`
}
