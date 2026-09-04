package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/sync/errgroup"

	cu "github.com/Davincible/chromedp-undetected"
	"github.com/antchfx/htmlquery"
	cdpBrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/go-rod/stealth"
	"github.com/go-shiori/go-readability"
	"github.com/microcosm-cc/bluemonday"
	"github.com/pkg/errors"
	"github.com/robertkrimen/otto"
	"github.com/samber/lo"
	lop "github.com/samber/lo/parallel"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/awsutil"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/service"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/maps"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/util/retry"

	"github.com/Ursa-Minor-Beta/baas/internal/llm"
	"github.com/Ursa-Minor-Beta/baas/internal/messagebus"
	"github.com/Ursa-Minor-Beta/baas/internal/service/embed"
	"github.com/Ursa-Minor-Beta/baas/internal/sessions"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

type Browser interface {
	Run(ctx context.Context, bOpts dto.BrowserOpts) (*dto.BrowserResponse, error)
	RunAsync(ctx context.Context, sessionID string, bOpts dto.BrowserOpts, getMeta func() service.ResultMeta) (*dto.BrowserResponse, error)
	DoAsyncAndWait(ctx context.Context, inMsg dto.BrowserMessageIn) (*dto.BrowserMessageOut, error)
	SupportedActions() map[string]string
	SetRequestsDebug(debug bool)
	SetChromeDebug(debug bool)
	StopAsync(ctx context.Context, sessionID string) (*dto.BrowserMessageOut, error)
}

var DefaultOperationTimeout = time.Second * 20

var ReadabilityAllowedElements = []string{"html", "head", "meta", "title", "section", "div", "span", "article", "p", "b", "h1", "h2", "h3", "h4", "h5", "content", "li", "ul"}

type browser struct {
	log            logger.Logger
	executablePath string
	inMessages     messagebus.Broker[dto.BrowserMessageIn]
	outMessages    messagebus.Broker[dto.BrowserMessageOut]
	registry       sessions.Registry
	extensions     []dto.ChromeExtensionManifest
	pandoc         Pandoc
	headful        bool
	requestsDebug  bool
	chromeDebug    bool
}

func NewBrowser(log logger.Logger, executable string, extensions []dto.ChromeExtensionManifest, pandoc Pandoc,
	inMsg messagebus.Broker[dto.BrowserMessageIn], outMsg messagebus.Broker[dto.BrowserMessageOut], registry sessions.Registry,
) Browser {
	headful := true
	if useHeadful, err := strconv.ParseBool(os.Getenv("BROWSER_HEADFUL")); err == nil {
		headful = useHeadful
	}
	return &browser{
		log:            log,
		executablePath: executable,
		extensions:     extensions,
		inMessages:     inMsg,
		outMessages:    outMsg,
		pandoc:         pandoc,
		headful:        headful,
		registry:       registry,
	}
}

type downloadFileState struct {
	guid     string
	fileName string
}

type downloadFile struct {
	dir            string
	started        *atomic.Bool
	fileState      chan downloadFileState
	complete       *atomic.Bool
	content        []byte
	downloadedName string
}

type browserProgramCtx struct {
	bOpts              dto.BrowserOpts
	outHtml            *string
	log                *[]string
	sessionID          string
	downloadFileInfo   *downloadFile
	screenshots        map[string][]byte
	values             map[string]string
	secrets            map[string]string
	generationInfo     map[string]any
	operationTimeout   *string
	errOnTimeout       *bool
	outValue           any
	outError           error
	actionOpts         opOption
	enableExtensions   []dto.ChromeExtensionMode
	readabilityArticle *dto.Article
}

// addLogEntry appends a log message to the program context log
func (pCtx *browserProgramCtx) addLogEntry(message string) {
	*pCtx.log = append(*pCtx.log, message)
}

func (b *browser) SetRequestsDebug(debug bool) {
	b.requestsDebug = debug
}

func (b *browser) SetChromeDebug(debug bool) {
	b.chromeDebug = debug
}

func (b *browser) SupportedActions() map[string]string {
	res := make(map[string]string)
	for name, a := range b.supportedActions(context.Background(), &browserProgramCtx{}) {
		res[name] = a.desc
	}
	return res
}

func (b *browser) DoAsyncAndWait(ctx context.Context, inMsg dto.BrowserMessageIn) (*dto.BrowserMessageOut, error) {
	done := make(chan dto.BrowserMessageOut)
	timeout, err := time.ParseDuration(inMsg.Timeout)
	if err != nil {
		b.log.Warnf(ctx, "failed to parse provided duration: %q", inMsg.Timeout)
		timeout = time.Second * 10
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if inMsg.RequestID == "" {
		// generate random request ID
		inMsg.RequestID = lo.RandomString(10, lo.LowerCaseLettersCharset)
	}
	go func() {
		_ = b.outMessages.OnMessage(ctx, inMsg.SessionID,
			func() {
				if err := b.inMessages.Send(ctx, inMsg.SessionID, inMsg); err != nil {
					b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to send in message")
				}
			},
			func(evt dto.BrowserMessageOut) {
				if evt.RequestID == inMsg.RequestID {
					done <- evt
				} else {
					b.log.Warnf(ctx, "got event with wrong request id: %q", evt.RequestID)
				}
			})
	}()
	select {
	case <-ctx.Done():
		return nil, errors.Wrapf(ctx.Err(), "failed to wait until response message")
	case evt := <-done:
		return &evt, nil
	}
}

func (b *browser) StopAsync(ctx context.Context, sessionID string) (*dto.BrowserMessageOut, error) {
	return b.DoAsyncAndWait(ctx, dto.BrowserMessageIn{
		SessionID:   sessionID,
		StopSession: lo.ToPtr(true),
	})
}

func (b *browser) RunAsync(ctx context.Context, sessionID string, bOpts dto.BrowserOpts, getMeta func() service.ResultMeta) (*dto.BrowserResponse, error) {
	inMsg := make(chan dto.BrowserMessageIn)
	done := make(chan bool)

	ctxDeadline, _ := ctx.Deadline()
	ctx = b.log.WithValues(ctx, map[string]any{
		"contextDeadline": time.Until(ctxDeadline).String(),
	})

	b.log.Infof(ctx, "starting async session")

	go func() {
		defer func() {
			done <- true
		}()
		_ = b.inMessages.OnMessage(ctx, sessionID, func() {}, func(evt dto.BrowserMessageIn) {
			inMsg <- evt
		})
	}()

	return b.doRun(ctx, bOpts, func(ctx context.Context, programCtx *browserProgramCtx) (chromedp.Action, error) {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			vm := otto.New()
			if err := b.setupOttoActions(ctx, programCtx, vm); err != nil {
				return err
			}
			for {
				select {
				case <-done:
					b.log.Infof(ctx, "async execution finished")
					return nil
				case <-ctx.Done():
					b.log.Infof(ctx, "async execution finished")
					return nil
				case msg := <-inMsg:
					ctx = b.log.WithValue(ctx, "message", msg.Sanitized())
					if lo.FromPtr(msg.StopSession) {
						b.log.Infof(ctx, "stopping session by request")
						ctx := context.WithoutCancel(ctx)
						if err := chromedp.Cancel(ctx); err != nil {
							b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to stop browser session")
						}
						if err := b.outMessages.Send(ctx, sessionID, b.toCurrentResponseOut(msg, programCtx)); err != nil {
							b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "error while responding to stop request")
						}
						return nil
					}
					programCtx.sessionID = sessionID
					programCtx.screenshots = make(map[string][]byte)
					programCtx.generationInfo = make(map[string]any)
					programCtx.log = lo.ToPtr(make([]string, 0))
					programCtx.secrets = msg.Secrets
					programCtx.values = msg.Values
					programCtx.outError = nil // erase previously occurred error
					// erase download file info, so we can download file multiple times
					programCtx.downloadFileInfo = b.newDownloadFile(programCtx.downloadFileInfo.dir)
					programCtx.operationTimeout = msg.OperationTimeout
					programCtx.errOnTimeout = msg.ErrorOnOperationTimeout
					b.log.Infof(ctx, "starting to process message's program")
					val, err := vm.Eval(msg.Program)
					b.log.Infof(ctx, "completed to process message's program")
					msgOut := b.toCurrentResponseOut(msg, programCtx)
					if getMeta != nil {
						msgOut.Meta = getMeta()
					}
					ctx = b.log.WithValue(ctx, "generationInfo", programCtx.generationInfo)
					msgOut.GenerationInfo = programCtx.generationInfo
					if err != nil {
						b.log.Infof(b.log.WithValue(ctx, "error", err.Error()), "sending error back to client")
						msgOut.Error = err.Error()
					} else {
						msgOut.Value, _ = val.Export()
						ctx := b.log.WithValue(ctx, "value", msgOut.Value)
						ctx = b.log.WithValue(ctx, "outFileSize", len(msgOut.DownloadedFile))
						ctx = b.log.WithValue(ctx, "outScreenshots", lo.Keys(msgOut.Screenshots))
						ctx = b.log.WithValue(ctx, "outHtml", msgOut.OutHTML)
						b.log.Infof(ctx, "sending value back to client")
					}
					err = b.outMessages.Send(ctx, sessionID, msgOut)
					if err != nil {
						b.log.Infof(b.log.WithValue(ctx, "error", err.Error()), "failed to send value back to client")
					} else {
						b.log.Infof(ctx, "response has been sent back to client")
					}
				}
			}
		}), nil
	})
}

// estimateMessageSize estimates the approximate size of a BrowserMessageOut in bytes
func (b *browser) estimateMessageSize(msg dto.BrowserMessageOut) int {
	// Use JSON marshaling to get a rough estimate of the serialized size
	data, err := json.Marshal(msg)
	if err != nil {
		// Fallback estimation if marshaling fails
		size := len(msg.SessionID) + len(msg.RequestID) + len(msg.Timestamp) + len(msg.OutHTML)
		size += len(msg.Error)
		for _, logEntry := range msg.Log {
			size += len(logEntry)
		}
		for _, screenshot := range msg.Screenshots {
			size += len(screenshot)
		}
		size += len(msg.DownloadedFile) + len(msg.DownloadedFileName)
		return size
	}
	return len(data)
}

// truncateLogEntry truncates a log entry to the specified maximum length
func (b *browser) truncateLogEntry(entry string, maxLength int) string {
	if len(entry) <= maxLength {
		return entry
	}

	// Ensure we don't break UTF-8 characters
	if maxLength < 20 {
		maxLength = 20 // Minimum reasonable length
	}

	truncated := entry
	if utf8.ValidString(entry) {
		runes := []rune(entry)
		if len(runes) > maxLength-10 { // Reserve space for "... [truncated]"
			truncated = string(runes[:maxLength-13]) + "... [truncated]"
		}
	} else {
		// Fallback for non-UTF8 strings
		if len(entry) > maxLength-13 {
			truncated = entry[:maxLength-13] + "... [truncated]"
		}
	}

	return truncated
}

// truncateLogsIfNeeded truncates log entries if the total message size exceeds MongoDB limits
func (b *browser) truncateLogsIfNeeded(msg dto.BrowserMessageOut) dto.BrowserMessageOut {
	estimatedSize := b.estimateMessageSize(msg)

	if estimatedSize <= MaxMongoDocumentSize {
		return msg // No truncation needed
	}

	// Create a copy to avoid modifying the original
	truncatedMsg := msg
	truncatedLogs := make([]string, len(msg.Log))

	// First, try truncating individual log entries
	for i, logEntry := range msg.Log {
		truncatedLogs[i] = b.truncateLogEntry(logEntry, MaxLogEntryLength)
	}

	truncatedMsg.Log = truncatedLogs

	// Check size again after truncation
	newEstimatedSize := b.estimateMessageSize(truncatedMsg)

	if newEstimatedSize <= MaxMongoDocumentSize {
		// Add a note about truncation
		truncatedMsg.Log = append(truncatedMsg.Log, fmt.Sprintf("[SYSTEM] Log entries were truncated due to size limits. Original size: ~%d bytes, truncated size: ~%d bytes", estimatedSize, newEstimatedSize))
		return truncatedMsg
	}

	// If still too large, keep only the most recent log entries
	maxLogEntries := len(truncatedLogs) / 2
	if maxLogEntries < 10 {
		maxLogEntries = 10 // Keep at least 10 entries if possible
	}

	if len(truncatedLogs) > maxLogEntries {
		// Keep the most recent entries
		recentLogs := truncatedLogs[len(truncatedLogs)-maxLogEntries:]
		truncatedMsg.Log = append([]string{fmt.Sprintf("[SYSTEM] Showing only the most recent %d log entries due to size limits. Original count: %d entries", maxLogEntries, len(msg.Log))}, recentLogs...)
	}

	return truncatedMsg
}

func (b *browser) toCurrentResponseOut(msg dto.BrowserMessageIn, programCtx *browserProgramCtx) dto.BrowserMessageOut {
	res := dto.BrowserMessageOut{
		SessionID:          msg.SessionID,
		RequestID:          msg.RequestID,
		Timestamp:          time.Now().Format(time.DateTime),
		Screenshots:        programCtx.screenshots,
		Log:                lo.FromPtr(programCtx.log),
		DownloadedFile:     lo.FromPtr(programCtx.downloadFileInfo).content,
		DownloadedFileName: lo.FromPtr(programCtx.downloadFileInfo).downloadedName,
		OutHTML:            lo.FromPtr(programCtx.outHtml),
		ReadabilityArticle: programCtx.readabilityArticle,
	}
	if programCtx.outError != nil {
		res.Error = programCtx.outError.Error()
	}

	// Apply log truncation if the message size exceeds MongoDB limits
	return b.truncateLogsIfNeeded(res)
}

func (b *browser) Run(ctx context.Context, bOpts dto.BrowserOpts) (*dto.BrowserResponse, error) {
	return b.doRun(ctx, bOpts, func(ctx context.Context, programCtx *browserProgramCtx) (chromedp.Action, error) {
		return b.ottoRunCdpAction(programCtx)
	})
}

func (b *browser) doRun(ctx context.Context, bOpts dto.BrowserOpts, setupRunAction func(ctx context.Context, programCtx *browserProgramCtx) (chromedp.Action, error)) (*dto.BrowserResponse, error) {
	var opts []chromedp.ExecAllocatorOption

	if bOpts.UserAgent != "" {
		opts = append(
			opts,
			chromedp.UserAgent(bOpts.UserAgent),
		)
	}
	if bOpts.UseProxy != nil {
		opts = append(
			opts,
			chromedp.ProxyServer(lo.FromPtr(bOpts.UseProxy)),
		)
		b.log.Infof(ctx, "[chrome]: using proxy: %q", bOpts.UseProxy)
		b.log.Warnf(ctx, "[chrome]: NOT using proxy: %q", bOpts.UseProxy)
	}

	width := lo.If(bOpts.Width != nil, lo.FromPtr(bOpts.Width)).Else(1920)
	height := lo.If(bOpts.Height != nil, lo.FromPtr(bOpts.Height)).Else(1080)
	opts = append(opts, chromedp.WindowSize(width, height))
	if b.executablePath != "" {
		opts = append(opts, chromedp.ExecPath(b.executablePath))
	}

	logReader, logWriter := io.Pipe()
	opts = append(opts, chromedp.CombinedOutput(logWriter))

	var cancel func()
	var err error

	programCtx := browserProgramCtx{
		bOpts:            bOpts,
		outHtml:          lo.ToPtr(""),
		log:              lo.ToPtr([]string{}),
		screenshots:      make(map[string][]byte),
		generationInfo:   make(map[string]any),
		secrets:          bOpts.Secrets,
		values:           bOpts.Values,
		operationTimeout: bOpts.OperationTimeout,
		errOnTimeout:     bOpts.ErrorOnOperationTimeout,
		enableExtensions: bOpts.EnableExtensions,
		sessionID:        bOpts.SessionID,
	}

	if b.headful { // use undetected webdriver
		opts = append(
			opts,
			// chromedp.Flag("single-process", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-background-networking", true),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("disable-web-security", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("verbose", true),
			chromedp.Flag("ignore-certificate-errors", true),
		)
		var cuConfig cu.Config
		cuConfig.Headless = false
		b.log.Infof(ctx, "Running headful chrome with undetected webdriver")
		cuConfig = cu.NewConfig(cu.WithContext(ctx))
		cuConfig.ContextOptions = append(
			cuConfig.ContextOptions,
			chromedp.WithLogf(b.chromeLogFunction(ctx, &programCtx, "log")),
			chromedp.WithDebugf(b.chromeLogFunction(ctx, &programCtx, "debug")),
			chromedp.WithErrorf(b.chromeLogFunction(ctx, &programCtx, "error")),
		)
		cuConfig.LogLevel = 10
		cuConfig.ChromeFlags = opts
		cuConfig.Extensions = lo.Map(lo.Filter(b.extensions, func(ext dto.ChromeExtensionManifest, _ int) bool {
			return len(lo.Intersect(ext.BaasEnableOn, bOpts.EnableExtensions)) > 0
		}), func(ext dto.ChromeExtensionManifest, _ int) string {
			return ext.DirPath
		})
		b.log.Infof(ctx, "[chrome]: using extensions list: %v", cuConfig.Extensions)
		if b.executablePath != "" {
			opts = append(opts, chromedp.ExecPath(b.executablePath))
			cuConfig.ChromePath = b.executablePath
		}
		cuConfig.Ctx = ctx
		cuConfig.ChromeFlags = opts
		ctx, cancel, err = cu.New(cuConfig)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to init undetected webdriver")
		}
	} else {
		b.log.Infof(ctx, "Running regular headless chrome")
		opts = append(
			opts,
			// After Puppeteer's default behavior.
			chromedp.Flag("disable-background-networking", true),
			chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
			chromedp.Flag("disable-background-timer-throttling", true),
			chromedp.Flag("disable-backgrounding-occluded-windows", true),
			chromedp.Flag("disable-breakpad", true),
			chromedp.Flag("disable-client-side-phishing-detection", true),
			chromedp.Flag("disable-default-apps", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			// chromedp.Flag("disable-web-security", true),
			chromedp.Flag("disable-extensions", true),
			chromedp.Flag("disable-features", "site-per-process,Translate,BlinkGenPropertyTrees"),
			chromedp.Flag("disable-hang-monitor", true),
			chromedp.Flag("disable-ipc-flooding-protection", true),
			chromedp.Flag("disable-popup-blocking", true),
			chromedp.Flag("disable-prompt-on-repost", true),
			chromedp.Flag("disable-renderer-backgrounding", true),
			chromedp.Flag("disable-sync", true),
			chromedp.Flag("force-color-profile", "srgb"),
			chromedp.Flag("metrics-recording-only", true),
			chromedp.Flag("safebrowsing-disable-auto-update", true),
			chromedp.Flag("enable-automation", true),
			chromedp.Flag("password-store", "basic"),
			chromedp.Flag("use-mock-keychain", true),
			chromedp.Headless,
			chromedp.NoSandbox,
			chromedp.NoDefaultBrowserCheck,
			chromedp.DisableGPU,
			chromedp.NoFirstRun,
			chromedp.Flag("single-process", true),
		)

		allocCtx, cancelAllocator := chromedp.NewExecAllocator(ctx, opts...)
		defer cancelAllocator()
		ctx, cancel = chromedp.NewContext(allocCtx, chromedp.WithLogf(func(s string, i ...interface{}) {
			b.log.Infof(ctx, "[chrome]: %v", i...)
		}))
	}
	defer cancel()

	// navigate to a page
	errG, ctx := errgroup.WithContext(ctx)
	errG.Go(util.ReaderToCallbackFunc(ctx, logReader, func(logLine string) {
		b.log.Infof(ctx, "[chrome]: "+logLine)
	}))

	var actions []chromedp.Action

	if b.headful {
		actions = append(actions, b.disablePdfViewer())
	}

	downloadDir, err := os.MkdirTemp(os.TempDir(), "download")
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create temp dir")
	}
	defer func() {
		_ = os.RemoveAll(downloadDir)
	}()

	if bOpts.Timeout == "" {
		bOpts.Timeout = "60s"
	}
	timeout, err := time.ParseDuration(bOpts.Timeout)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse timeout duration from string %q", bOpts.Timeout)
	}

	programCtx.downloadFileInfo = b.newDownloadFile(downloadDir)

	runAction, err := setupRunAction(ctx, &programCtx)
	if err != nil {
		ctx = b.log.WithValue(ctx, "error", err.Error())
		b.log.Errorf(ctx, "failed to setup otto program")
		return nil, errors.Wrapf(err, "failed to setup otto program")
	}

	// pre actions
	actions = append(
		actions, setCookies(bOpts.Cookies), cdpBrowser.
			SetDownloadBehavior(cdpBrowser.SetDownloadBehaviorBehaviorAllowAndName).
			WithDownloadPath(downloadDir).
			WithEventsEnabled(true),
	)
	// only append fetch.enable if chromeDebug is true
	if b.chromeDebug {
		actions = append(actions, fetch.Enable())
	}
	headers := make(network.Headers)
	for k, v := range bOpts.Headers {
		headers[k] = v
	}
	// Add default Accept-Language header if not specified
	if _, exists := headers["Accept-Language"]; !exists {
		headers["Accept-Language"] = "en-US,en;q=0.9,*;q=0.5"
	}
	actions = append(actions, network.SetExtraHTTPHeaders(headers))
	initErr := chromedp.Run(ctx, actions...)
	if initErr != nil {
		return nil, initErr
	}

	chromedp.ListenTarget(ctx, func(v interface{}) {
		defer func() {
			if r := recover(); r != nil {
				b.log.Errorf(b.log.WithValue(ctx, "panic", r), "recovered from panic in download event listener")
			}
		}()
		fileState := downloadFileState{}
		if ev, ok := v.(*cdpBrowser.EventDownloadWillBegin); ok {
			b.log.Infof(ctx, "Got event EventDownloadWillBegin")
			fileState.fileName = ev.SuggestedFilename
		} else if ev, ok := v.(*cdpBrowser.EventDownloadProgress); ok {
			b.log.Infof(ctx, "Got event EventDownloadProgress")
			if ev.State == cdpBrowser.DownloadProgressStateInProgress && programCtx.downloadFileInfo.started.CompareAndSwap(false, true) {
				b.log.Infof(ctx, "file download has started")
			}
			if ev.State == cdpBrowser.DownloadProgressStateCompleted {
				fileState.guid = ev.GUID
				if fileState.fileName == "" {
					fileState.fileName = fileState.guid
				}
				if programCtx.downloadFileInfo.complete.CompareAndSwap(false, true) {
					programCtx.downloadFileInfo.fileState <- fileState
					close(programCtx.downloadFileInfo.fileState)
				}
			}
		} else if ev, ok := v.(*fetch.EventRequestPaused); ok {
			go func() {
				c := chromedp.FromContext(ctx)
				ctx := cdp.WithExecutor(ctx, c.Target)
				// Continue the request normally to get the original response
				err := fetch.ContinueRequest(ev.RequestID).Do(ctx)
				if err != nil {
					b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to continue request")
				}
			}()
		} else if ev, ok := v.(*network.EventResponseReceived); ok {

			if ev.Type != network.ResourceTypeXHR && ev.Type != network.ResourceTypePreflight {
				b.log.Infof(ctx, "skip interception for request for %q", ev.Response.URL)
				return
			}

			go func() {
				b.log.Infof(ctx, "intercepted request for %q", ev.Response.URL)

				c := chromedp.FromContext(ctx)
				ctx := cdp.WithExecutor(ctx, c.Target)

				// Get the original response body
				body, err := fetch.GetResponseBody(fetch.RequestID(ev.RequestID)).Do(ctx)
				if err != nil {
					b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to get response body")
					return
				}

				// Prepare headers with original headers plus CORS headers
				headers := make([]*fetch.HeaderEntry, 0)

				// Define CORS headers with their values
				corsHeaders := map[string]string{
					"Access-Control-Allow-Origin":      "*",
					"Access-Control-Allow-Methods":     "GET, POST, PUT, DELETE, OPTIONS, HEAD, PATCH",
					"Access-Control-Allow-Headers":     "*",
					"Access-Control-Allow-Credentials": "true",
					"Access-Control-Max-Age":           "86400",
				}

				// Copy original headers, excluding existing CORS headers
				for name, value := range ev.Response.Headers {
					if !lo.Contains(lo.Keys(corsHeaders), name) {
						headers = append(headers, &fetch.HeaderEntry{Name: name, Value: fmt.Sprintf("%v", value)})
					}
				}

				// Add CORS headers
				for name, value := range corsHeaders {
					headers = append(headers, &fetch.HeaderEntry{Name: name, Value: value})
				}

				// Fulfill the request with original body, status, and modified headers
				err = fetch.FulfillRequest(fetch.RequestID(ev.RequestID), ev.Response.Status).
					WithBody(string(body)).
					WithResponseHeaders(headers).
					Do(ctx)
				if err != nil {
					b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to fulfill request with CORS headers")
				} else if b.chromeDebug {
					b.log.Infof(ctx, "fulfilled request with CORS headers for %q", ev.Response.URL)
				}
				programCtx.addLogEntry(fmt.Sprintf("fulfilled request with CORS headers for %q", ev.Response.URL))
			}()
		}
	})

	if err := b.registry.UpsertActiveSession(ctx, programCtx.sessionID, bOpts); err != nil {
		b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to register session in registry")
	}

	defer func() {
		ctx := context.WithoutCancel(ctx)
		if err := b.registry.RemoveActiveSession(ctx, programCtx.sessionID); err != nil {
			b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to remove session from registry")
		}
	}()

	// run action
	var resp *network.Response
	var runErr error
	runErr = b.doWithTimeout(ctx, timeout, func(ctx context.Context) error {
		resp, runErr = chromedp.RunResponse(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			b.log.Infof(ctx, "[chrome]: evaluating stealth.JS...")
			_, err := page.AddScriptToEvaluateOnNewDocument(stealth.JS).Do(ctx)
			return err
		}), runAction)
		return runErr
	})

	if resp != nil && resp.Status != http.StatusOK {
		ctx = b.log.WithValue(ctx, "statusCode", resp.Status)
		ctx = b.log.WithValue(ctx, "statusText", resp.StatusText)
		b.log.Errorf(ctx, "Response status is not %d (%d): %s", http.StatusOK, resp.Status, resp.StatusText)
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	// post actions
	var cookies []dto.BrowserCookie
	var url string
	var finalScreenshot []byte
	actions = []chromedp.Action{}
	actions = append(
		actions,
		outCookies(&cookies),
		chromedp.Location(&url),
	)
	if lo.FromPtr(bOpts.ReturnScreenshot) {
		actions = append(actions, chromedp.FullScreenshot(&finalScreenshot, 90))
	}

	err = chromedp.Run(ctx, actions...)

	response := dto.BrowserResponse{
		OutHTML:            lo.FromPtr(programCtx.outHtml),
		Cookies:            cookies,
		URL:                url,
		Screenshot:         finalScreenshot,
		Log:                lo.FromPtr(programCtx.log),
		Screenshots:        programCtx.screenshots,
		GenerationInfo:     programCtx.generationInfo,
		Value:              programCtx.outValue,
		ReadabilityArticle: programCtx.readabilityArticle,
	}
	if runErr != nil && err == nil {
		response.Error = lo.ToPtr(runErr.Error())
		b.log.Errorf(b.log.WithValue(ctx, "error", runErr.Error()), "otto program failed to execute")
	} else if err != nil {
		b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to run post-actions on chrome")
		return nil, errors.Wrapf(err, "failed to run post-actions on chrome")
	}
	if programCtx.downloadFileInfo.content != nil {
		response.DownloadedFile = programCtx.downloadFileInfo.content
	}

	return &response, nil
}

func (b *browser) getCenterOfElement(ctx context.Context, sel string) (*dto.XYCoords, error) {
	res := &dto.XYCoords{}
	err := chromedp.QueryAfter(sel, func(ctx context.Context, id runtime.ExecutionContextID, node ...*cdp.Node) error {
		if len(node) == 0 {
			return fmt.Errorf("node not found by selector")
		}
		boxes, err := dom.GetContentQuads().WithNodeID(node[0].NodeID).Do(ctx)
		if err != nil {
			return err
		}

		box := boxes[0]
		c := len(box)
		if c%2 != 0 || c < 1 {
			return chromedp.ErrInvalidDimensions
		}

		var x, y float64
		for i := 0; i < c; i += 2 {
			x += box[i]
			y += box[i+1]
		}
		x /= float64(c / 2)
		y /= float64(c / 2)
		res.X = x
		res.Y = y
		return nil
	}).Do(ctx)
	return res, err
}

func (b *browser) dragAndDropBySelectors(ctx context.Context, fromElementSelector, toElementSelector string) error {
	ctx = b.log.WithValue(ctx, "fromElementSelector", fromElementSelector)
	ctx = b.log.WithValue(ctx, "toElementSelector", toElementSelector)
	fromCoords, err := b.getCenterOfElement(ctx, fromElementSelector)
	if err != nil {
		return errors.Wrapf(err, "failed to determine coordinates of from element")
	}
	toCoords, err := b.getCenterOfElement(ctx, toElementSelector)
	if err != nil {
		return errors.Wrapf(err, "failed to determine coordinates of to element")
	}
	return b.mouseDragAndDrop(ctx, fromCoords, toCoords)
}

func (b *browser) mouseDragAndDrop(ctx context.Context, fromCoords, toCoords *dto.XYCoords) error {
	p := &input.DispatchMouseEventParams{
		Type:       input.MousePressed,
		X:          fromCoords.X,
		Y:          fromCoords.Y,
		Button:     input.Left,
		ClickCount: 1,
	}

	if err := p.Do(ctx); err != nil {
		return err
	}

	// Mouse Move
	p.Type = input.MouseMoved
	p.X = lo.FromPtr(toCoords).X
	p.Y = lo.FromPtr(toCoords).Y

	if err := p.Do(ctx); err != nil {
		return err
	}

	p.Type = input.MouseReleased
	return p.Do(ctx)
}

// waitForNetworkIdle waits until there are no active network connections for a specified duration.
// ideal for generic "wait until page settles" logic.
func (b *browser) waitForNetworkIdle(ctx context.Context, idleDuration time.Duration) error {
	// Create a new context for this specific wait action
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second) // Safety timeout
	defer cancel()

	var mu sync.Mutex
	inflight := 0
	lastActivity := time.Now()

	// Define listeners for network events
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		mu.Lock()
		defer mu.Unlock()

		switch ev.(type) {
		case *network.EventRequestWillBeSent:
			inflight++
			lastActivity = time.Now()
		case *network.EventLoadingFinished, *network.EventLoadingFailed:
			inflight--
			lastActivity = time.Now()
			// Inflight can technically go below 0 due to race conditions in CDP or
			// attaching listeners mid-request, so we clamp it.
			if inflight < 0 {
				inflight = 0
			}
		}
	})

	// Ensure we enable network events so we actually receive them
	if err := chromedp.Run(ctx, network.Enable()); err != nil {
		return err
	}

	// Ticker to poll for idleness
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			mu.Lock()
			// If no requests are in flight AND enough time has passed since the last activity
			if inflight == 0 && time.Since(lastActivity) > idleDuration {
				mu.Unlock()
				return nil // We are idle!
			}
			mu.Unlock()
		}
	}
}

// nolint: unused
// TODO:
func (b *browser) listenFileDownload(ctx context.Context, programCtx *browserProgramCtx) func(ev interface{}) {
	return func(v interface{}) {
		defer func() {
			if r := recover(); r != nil {
				b.log.Errorf(b.log.WithValue(ctx, "panic", r), "recovered from panic in listenFileDownload")
			}
		}()
		fileState := downloadFileState{}
		if ev, ok := v.(*cdpBrowser.EventDownloadWillBegin); ok {
			fileState.fileName = ev.SuggestedFilename
		} else if ev, ok := v.(*cdpBrowser.EventDownloadProgress); ok {
			if ev.State == cdpBrowser.DownloadProgressStateInProgress && programCtx.downloadFileInfo.started.CompareAndSwap(false, true) {
				b.log.Infof(ctx, "file download has started")
			}
			if ev.State == cdpBrowser.DownloadProgressStateCompleted {
				fileState.guid = ev.GUID
				if fileState.fileName == "" {
					fileState.fileName = fileState.guid
				}
				if programCtx.downloadFileInfo.complete.CompareAndSwap(false, true) {
					programCtx.downloadFileInfo.fileState <- fileState
					close(programCtx.downloadFileInfo.fileState)
				}
			}
		}
	}
}

func (b *browser) newDownloadFile(downloadDir string) *downloadFile {
	return &downloadFile{
		dir:       downloadDir,
		fileState: make(chan downloadFileState, 1),
		complete:  new(atomic.Bool),
		started:   &atomic.Bool{},
	}
}

func (b *browser) downloadFileFromURL(ctx context.Context, sessionID, fileURL, destDir string) (string, error) {
	filePath, err := downloadURLToTempFile(ctx, b.log, fileURL, destDir, "upload-")
	if err != nil {
		return "", err
	}
	if sessionID != "" {
		if err := b.registry.RecordTempFile(ctx, sessionID, filePath); err != nil {
			b.log.Warnf(b.log.WithValue(ctx, "error", err.Error()), "failed to record temp file for session")
		}
	}
	return filePath, nil
}

func (b *browser) waitForFileDownload(ctx context.Context, info *downloadFile, duration time.Duration) ([]byte, string, error) {
	b.log.Infof(ctx, "waiting until file is being downloaded...")
	select {
	case fileState := <-info.fileState:
		fileBytes, err := os.ReadFile(filepath.Join(info.dir, fileState.guid))
		if err != nil {
			return nil, "", errors.Wrapf(err, "failed to read downloaded file")
		}
		return fileBytes, fileState.fileName, nil
	case <-time.After(duration):
		return nil, "", errors.Errorf("timeout while waiting for file download")
	}
}

type valuePromise func() any

var noValue valuePromise = func() any { return nil }

type runActionFunc func(call otto.FunctionCall) (chromedp.Action, valuePromise)

type ottoCdpAction struct {
	desc         string
	sanitizeArgs bool
	run          runActionFunc
}

func (b *browser) ottoRunCdpAction(pCtx *browserProgramCtx) (chromedp.Action, error) {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		vm := otto.New()
		if err := b.setupOttoActions(ctx, pCtx, vm); err != nil {
			return err
		}
		val, err := vm.Run(pCtx.bOpts.Program)
		if err == nil {
			pCtx.outValue, _ = val.Export()
		}
		return err
	}), nil
}

func (b *browser) setupOttoActions(ctx context.Context, pCtx *browserProgramCtx, vm *otto.Otto) (retErr error) {
	for actionName, action := range b.supportedActions(ctx, pCtx) {
		err := vm.Set(actionName, func(call otto.FunctionCall) (ret otto.Value) {
			var argVals []string
			var actionWithArgVals string
			defer func() {
				if err := recover(); err != nil {
					b.log.Errorf(ctx, "panic occurred when executing action %q: \n %v \n %s", actionWithArgVals, err, string(debug.Stack()))
					pCtx.addLogEntry(fmt.Sprintf("PANIC occurred when executing action %q: %v", actionWithArgVals, err))
					pCtx.outError = errors.Errorf("panic occurred when executing action %q: %v", actionWithArgVals, err)
				}
			}()

			argVals = lo.Map(call.ArgumentList, func(arg otto.Value, _ int) string {
				res, _ := arg.Export()
				return fmt.Sprintf("%s", res)
			})
			ctx = context.WithValue(ctx, secretArgsOption, action.sanitizeArgs)
			actionWithArgVals = fmt.Sprintf("%s(%s)", actionName, strings.Join(b.sanitizeSecrets(ctx, actionName, argVals, pCtx), ", "))
			ctx = b.log.WithValue(ctx, "action", actionWithArgVals)
			ctxDeadline, _ := ctx.Deadline()
			ctx = b.log.WithValues(ctx, map[string]any{
				"contextDeadline": time.Until(ctxDeadline).String(),
			})

			b.log.Infof(ctx, "executing action...")
			cdpAction, getReturn := action.run(call)

			opTimeout := DefaultOperationTimeout

			if timeoutFromOpt, err := time.ParseDuration(lo.FromPtr(pCtx.actionOpts.WithTimeout)); err == nil {
				opTimeout = timeoutFromOpt
			} else if parsedOpTimeout, err := time.ParseDuration(lo.FromPtr(pCtx.operationTimeout)); err == nil {
				opTimeout = parsedOpTimeout
			}
			ignoreNavigationErrors := lo.FromPtr(pCtx.actionOpts.WithIgnoreNavigationErrors)
			withoutTimeout := lo.FromPtr(pCtx.actionOpts.WithoutTimeout)
			doFunc := func(ctx context.Context) error {
				// Execute the original action
				if err := cdpAction.Do(ctx); err != nil {
					return err
				}

				// Wait for network idle after action if option is provided
				if pCtx.actionOpts.WithWaitForNetworkIdle != nil {
					durationStr := lo.FromPtr(pCtx.actionOpts.WithWaitForNetworkIdle)
					duration, err := time.ParseDuration(durationStr)
					if err != nil {
						// Default to 500ms if parsing fails
						duration = 500 * time.Millisecond
						b.log.Warnf(ctx, "Failed to parse waitForNetworkIdle duration %q, using default 500ms", durationStr)
					}

					if err := b.waitForNetworkIdle(ctx, duration); err != nil {
						// Log the error but don't fail the action - network idle is best effort
						b.log.Warnf(ctx, "waitForNetworkIdle failed after action %q: %v", actionWithArgVals, err)
						pCtx.addLogEntry(fmt.Sprintf("waitForNetworkIdle warning after action %q: %v", actionWithArgVals, err))
					} else {
						b.log.Infof(ctx, "Network idle achieved after action %q (duration: %s)", actionWithArgVals, duration)
					}
				}

				return nil
			}
			var err error
			if withoutTimeout {
				err = doFunc(ctx)
			} else {
				err = b.doWithTimeout(ctx, opTimeout, doFunc)
			}
			if errors.Is(err, context.DeadlineExceeded) && !lo.If(pCtx.errOnTimeout != nil, lo.FromPtr(pCtx.errOnTimeout)).Else(true) {
				b.log.Warnf(ctx, "got operation timeout, but ignoring it, since error on operation timeout is disabled")
				pCtx.addLogEntry(fmt.Sprintf("Operation timeout occurred for action %q, but ignoring due to configuration", actionWithArgVals))
				err = nil
			}
			if err != nil && (!ignoreNavigationErrors || !strings.Contains(err.Error(), "net::ERR_ABORTED")) {
				b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "error while executing action")
				pCtx.addLogEntry(fmt.Sprintf("Error while executing action %q: %v", actionWithArgVals, err))
				res, err := otto.ToValue(err)
				if err != nil {
					panic(err)
				}
				return res
			}
			returnValue := getReturn()
			b.log.Infof(b.log.WithValue(ctx, "returnValue", returnValue), "got return value of action")
			res, err := vm.ToValue(returnValue)
			if err != nil {
				b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "error while getting return value of action")
				panic(err)
			}
			return res
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type opOption struct {
	WithoutTimeout             *bool
	WithTimeout                *string
	WithIframe                 *string
	WithSelector               *string
	WithIncludeInvisible       *bool
	WithSecretArgs             *bool
	WithAllowAttributes        *string
	WithAllowTags              *string
	WithIgnoreNavigationErrors *bool
	WithWaitForNetworkIdle     *string
}

type actionOption string

const (
	withoutTimeoutOption         actionOption = "withoutTimeout"
	timeoutOption                actionOption = "timeout"
	iframeOption                 actionOption = "iframe"
	selectorOption               actionOption = "selector"
	includeInvisibleOption       actionOption = "includeInvisible"
	secretArgsOption             actionOption = "secretArgs"
	allowAttributesOption        actionOption = "allowAttributes"
	allowTagsOption              actionOption = "allowTags"
	ignoreNavigationErrorsOption actionOption = "ignoreNavigationErrors"
	waitForNetworkIdleOption     actionOption = "waitForNetworkIdle"
)

const (
	// MongoDB BSON document limit is 16MB, we'll use 14MB as safe limit to account for overhead
	MaxMongoDocumentSize = 14 * 1024 * 1024 // 14MB
	// Maximum length for individual log entries when truncation is needed
	MaxLogEntryLength = 500
)

func (b *browser) withTimeoutOption(originalAction chromedp.Action, timeout *string) chromedp.Action {
	if parsedTimeout, err := time.ParseDuration(lo.FromPtr(timeout)); err == nil {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			ctx = context.WithValue(ctx, timeoutOption, parsedTimeout)
			b.log.Infof(ctx, "timeout of %q option was specified for action", lo.FromPtr(timeout))
			err := b.doWithTimeout(ctx, parsedTimeout, func(ctx context.Context) error {
				return originalAction.Do(ctx)
			})
			if err != nil && errors.Is(err, context.DeadlineExceeded) {
				b.log.Errorf(ctx, "timeout occurred while executing action")
			}
			return err
		})
	}
	return originalAction
}

func (b *browser) withOptionBoolContextValue(originalAction chromedp.Action, optionName actionOption) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		ctx = context.WithValue(ctx, optionName, true)
		b.log.Infof(ctx, "%q option was specified for action", optionName)
		return originalAction.Do(ctx)
	})
}

func (b *browser) withAllowTagsOption(originalAction chromedp.Action, tags *string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		ctx = context.WithValue(ctx, allowTagsOption, lo.FromPtr(tags))
		b.log.Infof(ctx, "allow tags option was specified for action with value %q", lo.FromPtr(tags))
		return originalAction.Do(ctx)
	})
}

func (b *browser) withAllowAttributesOption(originalAction chromedp.Action, attributes *string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		ctx = context.WithValue(ctx, allowAttributesOption, lo.FromPtr(attributes))
		b.log.Infof(ctx, "allow attributes option was specified for action with value %q", lo.FromPtr(attributes))
		return originalAction.Do(ctx)
	})
}

func (b *browser) withSelectorOption(originalAction chromedp.Action, selector *string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		ctx = context.WithValue(ctx, selectorOption, lo.FromPtr(selector))
		b.log.Infof(ctx, "selector option was specified for action with selector %q", lo.FromPtr(selector))
		return originalAction.Do(ctx)
	})
}

func (b *browser) withIframeOption(originalAction chromedp.Action, selector *string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		ctx = context.WithValue(ctx, iframeOption, selector)
		b.log.Errorf(ctx, "iframe option was specified for action with selector %q", lo.FromPtr(selector))
		return b.withIframeBySelector(ctx, lo.FromPtr(selector), func(ctx context.Context, opts ...chromedp.QueryOption) error {
			if qa, ok := originalAction.(*chromedp.Selector); ok {
				for _, opt := range opts {
					opt(qa)
				}
			}
			return originalAction.Do(ctx)
		})
	})
}

func (b *browser) withActionOptions(originalAction chromedp.Action, promise valuePromise, opts []opOption, pCtx *browserProgramCtx) (chromedp.Action, valuePromise) {
	wrappedAction := originalAction
	var actionOpts opOption
	for _, opt := range opts {
		if opt.WithTimeout != nil {
			actionOpts.WithTimeout = opt.WithTimeout
			wrappedAction = b.withTimeoutOption(wrappedAction, opt.WithTimeout)
		}
		if opt.WithIframe != nil {
			actionOpts.WithIframe = opt.WithIframe
			wrappedAction = b.withIframeOption(wrappedAction, opt.WithIframe)
		}
		if opt.WithSelector != nil {
			actionOpts.WithSelector = opt.WithSelector
			wrappedAction = b.withSelectorOption(wrappedAction, opt.WithSelector)
		}
		if opt.WithIncludeInvisible != nil && *opt.WithIncludeInvisible {
			actionOpts.WithIncludeInvisible = opt.WithIncludeInvisible
			wrappedAction = b.withOptionBoolContextValue(wrappedAction, includeInvisibleOption)
		}
		if opt.WithSecretArgs != nil && *opt.WithSecretArgs {
			actionOpts.WithSecretArgs = opt.WithSecretArgs
			wrappedAction = b.withOptionBoolContextValue(wrappedAction, secretArgsOption)
		}
		if opt.WithoutTimeout != nil && *opt.WithoutTimeout {
			actionOpts.WithTimeout = opt.WithTimeout
			wrappedAction = b.withOptionBoolContextValue(wrappedAction, withoutTimeoutOption)
		}
		if opt.WithAllowAttributes != nil {
			actionOpts.WithAllowAttributes = opt.WithAllowAttributes
			wrappedAction = b.withAllowAttributesOption(wrappedAction, opt.WithAllowAttributes)
		}
		if opt.WithAllowTags != nil {
			actionOpts.WithAllowTags = opt.WithAllowTags
			wrappedAction = b.withAllowTagsOption(wrappedAction, opt.WithAllowTags)
		}
		if opt.WithIgnoreNavigationErrors != nil {
			actionOpts.WithIgnoreNavigationErrors = opt.WithIgnoreNavigationErrors
			wrappedAction = b.withOptionBoolContextValue(wrappedAction, ignoreNavigationErrorsOption)
		}
		if opt.WithWaitForNetworkIdle != nil {
			actionOpts.WithWaitForNetworkIdle = opt.WithWaitForNetworkIdle
		}
	}
	pCtx.actionOpts = actionOpts
	return wrappedAction, promise
}

func (b *browser) parseActionOpts(call otto.FunctionCall, startArg int, defaultOpts ...opOption) []opOption {
	opts := make([]opOption, 0)
	opts = append(opts, defaultOpts...)
	if len(call.ArgumentList) >= startArg {
		for index := startArg; index < len(call.ArgumentList); index++ {
			arg := call.Argument(index)
			if !arg.IsString() {
				continue
			}
			argValue := arg.String()
			// TODO: migrate to map
			if strings.HasPrefix(argValue, string(timeoutOption)) {
				timeout := strings.TrimPrefix(argValue, fmt.Sprintf("%s:", timeoutOption))
				opts = append(opts, opOption{
					WithTimeout: lo.ToPtr(timeout),
				})
			} else if strings.HasPrefix(argValue, string(ignoreNavigationErrorsOption)) {
				opts = append(opts, opOption{
					WithIgnoreNavigationErrors: lo.ToPtr(true),
				})
			} else if strings.HasPrefix(argValue, string(includeInvisibleOption)) {
				opts = append(opts, opOption{
					WithIncludeInvisible: lo.ToPtr(true),
				})
			} else if strings.HasPrefix(argValue, string(iframeOption)) {
				selector := strings.TrimPrefix(argValue, fmt.Sprintf("%s:", iframeOption))
				opts = append(opts, opOption{
					WithIframe: lo.ToPtr(selector),
				})
			} else if strings.HasPrefix(argValue, string(allowAttributesOption)) {
				allowedAttributes := strings.TrimPrefix(argValue, fmt.Sprintf("%s:", allowAttributesOption))
				opts = append(opts, opOption{
					WithAllowAttributes: lo.ToPtr(allowedAttributes),
				})
			} else if strings.HasPrefix(argValue, string(selectorOption)) {
				selector := strings.TrimPrefix(argValue, fmt.Sprintf("%s:", selectorOption))
				opts = append(opts, opOption{
					WithSelector: lo.ToPtr(selector),
				})
			} else if strings.HasPrefix(argValue, string(secretArgsOption)) {
				opts = append(opts, opOption{
					WithSecretArgs: lo.ToPtr(true),
				})
			} else if strings.HasPrefix(argValue, string(withoutTimeoutOption)) {
				opts = append(opts, opOption{
					WithoutTimeout: lo.ToPtr(true),
				})
			} else if strings.HasPrefix(argValue, string(allowTagsOption)) {
				allowedTags := strings.TrimPrefix(argValue, fmt.Sprintf("%s:", allowTagsOption))
				opts = append(opts, opOption{
					WithAllowTags: lo.ToPtr(allowedTags),
				})
			} else if strings.HasPrefix(argValue, string(waitForNetworkIdleOption)) {
				duration := strings.TrimPrefix(argValue, fmt.Sprintf("%s:", waitForNetworkIdleOption))
				if duration == string(waitForNetworkIdleOption) {
					// If no duration specified, use default
					duration = "500ms"
				}
				opts = append(opts, opOption{
					WithWaitForNetworkIdle: lo.ToPtr(duration),
				})
			}
		}
	}
	return opts
}

type NavigateResponseStruct struct {
	Status              int64
	MimeType            string
	MimeTypeReadability bool
}

func (b *browser) supportedActions(ctx context.Context, pCtx *browserProgramCtx) map[string]ottoCdpAction {
	return map[string]ottoCdpAction{
		"navigate": {
			desc: "accepts string url where to navigate\n " +
				"opens this url in the browser\n " +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				u := call.Argument(0).String()
				return b.withActionOptions(chromedp.Navigate(u), noValue, b.parseActionOpts(call, 1, opOption{WithIgnoreNavigationErrors: lo.ToPtr(true)}), pCtx)
			},
		},
		"navigateResponse": {
			desc: "accepts string url where to navigate\n " +
				"opens this url in the browser\n " +
				"returns response object after navigation",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				targetURL := call.Argument(0).String()
				var response *network.Response
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					resp, err := chromedp.RunResponse(ctx, chromedp.Navigate(targetURL))
					if err != nil {
						return err
					}
					response = resp
					return nil
				}), func() any {
					r := lo.FromPtr(response)
					return NavigateResponseStruct{
						Status:              r.Status,
						MimeType:            r.MimeType,
						MimeTypeReadability: IsReadabilityMimeType(r.MimeType),
					}
				}, b.parseActionOpts(call, 1, opOption{WithIgnoreNavigationErrors: lo.ToPtr(true)}), pCtx)
			},
		},
		"navigateStatus": {
			desc: "accepts string url where to navigate\n " +
				"opens this url in the browser\n " +
				"returns http status of the page",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				targetURL := call.Argument(0).String()
				var response *network.Response
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					resp, err := chromedp.RunResponse(ctx, chromedp.Navigate(targetURL))
					if err != nil {
						return err
					}
					response = resp
					return nil
				}), func() any { return lo.FromPtr(response).Status }, b.parseActionOpts(call, 1, opOption{WithIgnoreNavigationErrors: lo.ToPtr(true)}), pCtx)
			},
		},
		"getValue": {
			desc: "accepts name of the value\n " +
				"returns value of the defined value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error { return nil }), func() any {
					name := call.Argument(0).String()
					return pCtx.values[name]
				}, b.parseActionOpts(call, 0), pCtx)
			},
		},
		"getSecret": {
			desc: "accepts name of the secret\n " +
				"returns value of the secret",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error { return nil }), func() any {
					name := call.Argument(0).String()
					return pCtx.secrets[name]
				}, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"getInnerText": {
			desc: "accepts css selector for html element\n " +
				"returns inner text of the found html element",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error { return nil }), func() any {
					var text string
					_ = chromedp.Text(selector, &text).Do(ctx)
					return text
				}, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"takeScreenshot": {
			desc: "accepts name of the screenshot to take\n " +
				"takes screenshot and preserves it under the specified name\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				name := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.takeScreenshot(ctx, name, pCtx)
				}), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"dragAndDropBySelectors": {
			desc: "accepts 2 css selectors for 2 html elements\n " +
				"simulates the event of mouse drag and mouse drop from one to another\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				from := call.Argument(0).String()
				to := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.dragAndDropBySelectors(ctx, from, to)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"countElements": {
			desc: "accepts css selector for html elements\n " +
				"return number of elements found on the page",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				var count int
				return b.withActionOptions(
					evaluateWithTimeout(fmt.Sprintf(`document.querySelectorAll('%s').length`, selector), &count, 5000),
					func() any { return count }, b.parseActionOpts(call, 1), pCtx,
				)
			},
		},
		"isElementPresent": {
			desc: "accepts css selector for html element\n " +
				"return boolean of whether element is present on the page",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				var count int
				return b.withActionOptions(
					evaluateWithTimeout(fmt.Sprintf(`document.querySelectorAll('%s').length`, selector), &count, 5000),
					func() any { return count > 0 }, b.parseActionOpts(call, 1), pCtx,
				)
			},
		},
		"getURL": {
			desc: "does not accept any arguments\n " +
				"returns string with value of current URL of the active browser page",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var urlstr string
				return b.withActionOptions(evaluateWithTimeout(`document.location.toString()`, &urlstr, 5000), func() any {
					return urlstr
				}, b.parseActionOpts(call, 0), pCtx)
			},
		},
		"log": {
			desc: "accepts string to print in the server log\n does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					message := call.Argument(0).String()
					b.log.Infof(ctx, message)
					pCtx.addLogEntry(message)
					return nil
				}), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"replaceInnerHtml": {
			desc: "accepts 2 arguments: css selector and full html representation of the element to put onto web page's content\n" +
				"replaces web page's content with the provided html\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					selector := call.Argument(0).String()
					htmlContent := call.Argument(1).String()
					return chromedp.PollFunction(`
							(selector, html) => {
								var element = document.querySelector(selector);
								element.innerHTML = html;
								return true;
							}
							`, nil, chromedp.WithPollingArgs(selector, htmlContent)).Do(ctx)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"waitReady": {
			desc: "accepts css selector for html element\n " +
				"waits until element is ready on the page\n " +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.WaitReady(selector), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"waitVisible": {
			desc: "accepts css selector for html element\n " +
				"waits until element is visible on the page\n " +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.WaitVisible(selector), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"waitForNetworkIdle": {
			desc: "accepts duration in the form of Go's duration, e.g. \"500ms\" or \"2s\"\n " +
				"waits until there are no active network connections for the specified duration\n " +
				"ideal for generic \"wait until page settles\" logic\n " +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				durationString := call.Argument(0).String()
				duration, err := time.ParseDuration(durationString)
				if err != nil {
					// Default to 500ms if parsing fails
					duration = 500 * time.Millisecond
				}
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.waitForNetworkIdle(ctx, duration)
				}), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"waitFileDownloadStarted": {
			desc: "accepts duration in the form of Go's duration, e.g. \"10s\"\n " +
				"waits for the provided duration until file download starts\n " +
				"returns true if file download started , returns " +
				"false if file download didn't start within the provided duration",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				durationString := call.Argument(0).String()
				duration, err := time.ParseDuration(durationString)
				if err != nil {
					return nil, noValue
				}
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					ctx, cancel := context.WithTimeout(ctx, duration)
					ticker := time.NewTicker(50 * time.Millisecond)
					defer cancel()
					for {
						select {
						case <-ticker.C:
							if pCtx.downloadFileInfo.started.Load() {
								return nil
							}
						case <-ctx.Done():
							return nil
						}
					}
				}), func() any {
					return pCtx.downloadFileInfo.started.Load()
				}, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"waitFileDownload": {
			desc: "accepts duration in the form of Go's duration, e.g. \"10s\"\n " +
				"waits for the provided duration until file is present\n " +
				"returns true if file was downloaded successfully, returns " +
				"false if file wasn't downloaded within the provided duration",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				durationString := call.Argument(0).String()
				duration, err := time.ParseDuration(durationString)
				if err != nil {
					return nil, noValue
				}
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					content, fileName, err := b.waitForFileDownload(ctx, pCtx.downloadFileInfo, duration)
					if err == nil {
						pCtx.downloadFileInfo.content = content
						pCtx.downloadFileInfo.downloadedName = fileName
					}
					return nil
				}), func() any {
					return pCtx.downloadFileInfo.content != nil
				}, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"uploadFileFromUrl": {
			desc: "accepts string url of the file to download and string css selector for the file input control\n " +
				"downloads the file to a temporary location and attaches it to the file input element\n " +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				fileURL := call.Argument(0).String()
				selector := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					filePath, err := b.downloadFileFromURL(ctx, pCtx.sessionID, fileURL, pCtx.downloadFileInfo.dir)
					if err != nil {
						return err
					}
					// we can't remove temp file right away, need to wait until session is deleted
					//defer func() {
					//	if remErr := os.Remove(filePath); remErr != nil {
					//		b.log.Warnf(b.log.WithValue(ctx, "error", remErr.Error()), "failed to remove temp upload file")
					//	}
					//}()

					if err := chromedp.SetUploadFiles(selector, []string{filePath}).Do(ctx); err != nil {
						return errors.Wrapf(err, "failed to set upload files for selector %q", selector)
					}

					pCtx.addLogEntry(fmt.Sprintf("uploaded file from %s via selector %s", fileURL, selector))
					return nil
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"sleep": {
			desc: "accepts duration in the form of Go's duration, e.g. \"10s\"\n " +
				"sleeps for the provided duration\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				durationString := call.Argument(0).String()
				duration, err := time.ParseDuration(durationString)
				if err != nil {
					return nil, noValue
				}
				return b.withActionOptions(chromedp.Sleep(duration), noValue, append(b.parseActionOpts(call, 1), opOption{WithoutTimeout: lo.ToPtr(true)}), pCtx)
			},
		},
		"waitForHtml": {
			desc: "accepts html or text to be present on the page\n " +
				"waits until either text/html to be present on the page or operation timeout to occur\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				text := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.waitForCondition(ctx, func(ctx context.Context) error {
						var pageHtml string
						if err := chromedp.InnerHTML("html", &pageHtml).Do(ctx); err != nil {
							return err
						}
						if strings.Contains(strings.ToLower(pageHtml), strings.ToLower(text)) {
							return nil
						}
						return errors.Errorf("text is still not present")
					})
				}), noValue, append(b.parseActionOpts(call, 1), opOption{WithoutTimeout: lo.ToPtr(true)}), pCtx)
			},
		},
		"isTextPresent": {
			desc: "accepts text to be present on the page\n " +
				"returns true if text is present on the page and false otherwise\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				text := call.Argument(0).String()
				isPresent := false
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					var pageText string
					if err := chromedp.Text("html", &pageText).Do(ctx); err != nil {
						return err
					}
					isPresent = strings.Contains(strings.ToLower(pageText), strings.ToLower(text))
					return nil
				}), func() any {
					return isPresent
				}, append(b.parseActionOpts(call, 1), opOption{WithoutTimeout: lo.ToPtr(true)}), pCtx)
			},
		},
		"waitForText": {
			desc: "accepts text to be present on the page\n " +
				"waits until either text to be present on the page or operation timeout to occur\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				text := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.waitForCondition(ctx, func(ctx context.Context) error {
						var pageText string
						if err := chromedp.Text("html", &pageText).Do(ctx); err != nil {
							return err
						}
						if strings.Contains(strings.ToLower(pageText), strings.ToLower(text)) {
							return nil
						}
						return errors.Errorf("text is still not present")
					})
				}), noValue, append(b.parseActionOpts(call, 1), opOption{WithoutTimeout: lo.ToPtr(true)}), pCtx)
			},
		},
		"getElementInnerTextN": {
			desc: "accepts css selector for html elements and index of the element to get inner text from\n " +
				"gets inner text from the nth found html element\n " +
				"returns inner text of the nth element\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				index, _ := call.Argument(1).ToInteger()
				var innerText string
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.doWithNthNode(ctx, selector, int(index), func(node *cdp.Node, r *runtime.RemoteObject) error {
						return chromedp.CallFunctionOn(
							embed.AttributeJS, &innerText,
							func(p *runtime.CallFunctionOnParams) *runtime.CallFunctionOnParams {
								return p.WithObjectID(r.ObjectID)
							}, "innerText",
						).Do(ctx)
					})
				}), func() any { return innerText }, b.parseActionOpts(call, 3), pCtx)
			},
		},
		"getElementValueN": {
			desc: "accepts css selector for html elements and index of the element to get value from\n " +
				"gets value from the nth found html element\n " +
				"returns value of the nth element\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				index, _ := call.Argument(1).ToInteger()
				var value string
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.doWithNthNode(ctx, selector, int(index), func(node *cdp.Node, r *runtime.RemoteObject) error {
						return chromedp.CallFunctionOn(
							embed.AttributeJS, &value,
							func(p *runtime.CallFunctionOnParams) *runtime.CallFunctionOnParams {
								return p.WithObjectID(r.ObjectID)
							}, "value",
						).Do(ctx)
					})
				}), func() any { return value }, b.parseActionOpts(call, 3), pCtx)
			},
		},
		"sendKeysToElement": {
			desc: "accepts css selector for html element and value of characters to send on that element\n " +
				"types value on found html element by sending keys\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				value := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.setValueOnControl(ctx, selector, value, pCtx, false)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"scrollIntoView": {
			desc: "accepts css selector for html element to scroll into view of\n " +
				"scrolls into view of the element\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return chromedp.ScrollIntoView(selector).Do(ctx)
				}), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"scrollIntoViewN": {
			desc: "accepts css selector for html element to scroll into view of and elemen's index on the page\n " +
				"scrolls into view of the nth element matched by selector\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				index, _ := call.Argument(1).ToInteger()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.doWithNthNode(ctx, selector, int(index), func(node *cdp.Node, r *runtime.RemoteObject) error {
						return dom.ScrollIntoViewIfNeeded().WithNodeID(node.NodeID).Do(ctx)
					})
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"setValueN": {
			desc: "accepts css selector for html elements, index of the element to set value on and value to set value on element\n " +
				"sets value on the nth found html element\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				index, _ := call.Argument(1).ToInteger()
				value := call.Argument(2).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.doWithNthNode(ctx, selector, int(index), func(node *cdp.Node, r *runtime.RemoteObject) error {
						return chromedp.CallFunctionOn(
							embed.SetAttributeJS, lo.ToPtr(""),
							func(p *runtime.CallFunctionOnParams) *runtime.CallFunctionOnParams {
								return p.WithObjectID(r.ObjectID)
							}, "value", value,
						).Do(ctx)
					})
				}), noValue, b.parseActionOpts(call, 3), pCtx)
			},
		},
		"clickN": {
			desc: "accepts css selector for html element and index of the element to click on\n " +
				"clicks on the nth found html element\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				index, _ := call.Argument(1).ToInteger()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.doWithNthNode(ctx, selector, int(index), func(node *cdp.Node, r *runtime.RemoteObject) error {
						return chromedp.MouseClickNode(node).Do(ctx)
					})
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"scrollToBottom": {
			desc: "does not accept any arguments \n" +
				"scrolls document to the bottom of the page\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return evaluateWithTimeout(`window.scrollTo(0,document.body.scrollHeight);`, nil, 5000).Do(ctx)
				}), noValue, b.parseActionOpts(call, 0), pCtx)
			},
		},
		"evaluateJS": {
			desc: "accepts JS to evaluate\n" +
				"evaluates provided JS as DevTools and returns result\n " +
				"returns result of evaluation (or nil)\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				script := call.Argument(0).String()
				var res any
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return evaluateWithTimeout(script, &res, 5000).Do(ctx)
				}), func() any { return res }, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"click": {
			desc: "accepts css selector for html element\n " +
				"clicks on the found html element\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					ctx = b.logElementBySelector(ctx, "clicking element", selector, pCtx)
					return chromedp.Click(selector, chromedp.NodeVisible).Do(ctx)
				}), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"submit": {
			desc: "accepts css selector for html element of the form\n " +
				"submits form found by the provided selector\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.Submit(selector), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"reload": {
			desc: "does not accept arguments\n" +
				"reloads current page \n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return chromedp.Reload().Do(ctx)
				}), noValue, b.parseActionOpts(call, 0), pCtx)
			},
		},
		"llmLogin": {
			sanitizeArgs: true, // always sanitize args for this function
			desc: "accepts two arguments: username and password\n" +
				"finds login and password elements on the page, " +
				"enters login/email and password to the form \n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				loginOrEmail := call.Argument(0).String()
				password := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.llmLogin(ctx, loginOrEmail, password, pCtx)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"llmClickElement": {
			desc: "accepts two arguments: first argument listing comma-separated elements to click and " +
				"second argument describing the element on the page to click\n" +
				"tries to find element which conforms the description and click it\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				elems := strings.Split(call.Argument(0).String(), ",")
				description := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.llmClick(ctx, description, elems, pCtx)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"llmClick": {
			desc: "accepts argument describing element on the page to click\n" +
				"tries to find element which conforms the description and click it\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				description := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.llmClick(ctx, description, nil, pCtx)
				}), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"llmSetValue": {
			desc: "accepts two arguments: description of the element on the page and value to set to this element\n" +
				"tries to find element which conforms the description and then set value to this element\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				description := call.Argument(0).String()
				value := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.llmSetValue(ctx, description, value, false, pCtx)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		// deprecated
		"llmSetValueSkipVerify": {
			desc: "accepts two arguments: description of the element on the page and value to set to this element\n" +
				"tries to find element which conforms the description and then set value to this element\n" +
				"skips verification of the value being set on the element\n" +
				"does not return any value",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				description := call.Argument(0).String()
				value := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.llmSetValue(ctx, description, value, false, pCtx)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"llmText": {
			desc: "accepts argument describing some text to be present\n" +
				"tries to find element which may represent this text on the page\n" +
				"returns text (if element is found on the page)",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var foundText string
				textKind := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					text, err := b.llmText(ctx, textKind, pCtx)
					if err != nil {
						return err
					}
					foundText = text
					return nil
				}), func() any { return foundText }, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"llmSendKeys": {
			desc: "accepts two arguments: description of the input element and string value of the keys to send value\n" +
				"tries to find element which may represent this input on the page and then sends provided keys to it\n" +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				desc := call.Argument(0).String()
				keys := call.Argument(1).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return b.llmSendKeys(ctx, desc, keys, pCtx)
				}), noValue, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"getInnerHtml": {
			desc: "accepts css selector for html element to take inner html from\n " +
				"returns inner html of the element under selector",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var elHtml string
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return chromedp.InnerHTML(selector, &elHtml).Do(ctx)
				}), func() any { return elHtml }, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"sessionID": {
			desc: "returns current sessionID of the browser",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return nil // nothing to do
				}), func() any { return pCtx.sessionID }, b.parseActionOpts(call, 0), pCtx)
			},
		},
		"getOuterHtml": {
			desc: "accepts css selector for html element to take outer html from\n " +
				"returns outer html of the element under selector",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var elHtml string
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					return chromedp.OuterHTML(selector, &elHtml).Do(ctx)
				}), func() any { return elHtml }, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"getElementCoords": {
			desc: "accepts css selector for html element to take coordinates from\n " +
				"returns coordinates of the element under selector in the form of a JSON object: " +
				"{topLeft:{x: number, y: number}, topRight:{x: number, y: number}, bottomRight:{x: number,y: number}, bottomLeft:{x: number, y: number}}",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var coords dto.ElementPosition
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					res, err := getElementCoords(ctx, selector)
					if err != nil {
						return err
					}
					coords = lo.FromPtr(res)
					return nil
				}), func() any { return coords }, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"findVisibleElements": {
			desc: "accepts two arguments: string containing comma separated html element names and string containing the name\n" +
				"of the attribute which will be added to every found element with a random string value\n" +
				"returns string containing html of all found html elements without any extra elements not present in the first argument",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				elements := call.Argument(0).String()
				attr := call.Argument(1).String()
				var result string
				return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) error {
					res, err := b.findHtmlElements(ctx, llmElReq{
						setAttr: &setAttr{
							random: true,
							name:   attr,
						},
						elems: strings.Split(elements, ","),
					})
					if err != nil {
						return err
					}
					result = res
					return nil
				}), func() any { return result }, b.parseActionOpts(call, 2), pCtx)
			},
		},
		"sendKeys": {
			desc: "accepts text to send to the browser page\n" +
				"sends keys to the browser page\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				keys := call.Argument(0).String()
				return b.withActionOptions(chromedp.KeyEvent(keys), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"logURL": {
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var urlString string
				return b.withActionOptions(evaluateWithTimeout(`document.location.toString()`, &urlString, 5000), func() any {
					logMsg := fmt.Sprintf("URL: %s", urlString)
					b.log.Infof(ctx, logMsg)
					pCtx.addLogEntry(logMsg)
					return urlString
				}, b.parseActionOpts(call, 0), pCtx)
			},
		},
		"text": {
			desc: "accepts css selector for html element to take text from\n " +
				"returns text within element under selector",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				var text string
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.Text(selector, &text), func() any {
					return text
				}, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"innerHtml": {
			desc: "accepts css selector for html element to take inner html from\n " +
				"keeps the inner html for the end of the program\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.InnerHTML(selector, pCtx.outHtml), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"outerHtml": {
			desc: "accepts css selector for html element to take outer html from\n " +
				"keeps the outer html for the end of the program\n " +
				"does not return any value\n",
			run: func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
				selector := call.Argument(0).String()
				return b.withActionOptions(chromedp.OuterHTML(selector, pCtx.outHtml), noValue, b.parseActionOpts(call, 1), pCtx)
			},
		},
		"waitReadabilityContent": {
			desc: "does not accept any arguments\n" +
				"waits until some content for readability appears on the page, if appeared returns article within browser response \n" +
				"does not return any value\n",
			run: b.waitReadabilityContent(pCtx),
		},
		"readability": {
			desc: "does not accept any arguments\n" +
				"parses current page's HTML with readability algorithms \n" +
				"returns readable content of the page",
			run: b.toReadableContentFunction(ctx, pCtx, nil, func(ctx context.Context, outHtml string, pageURL string, args ...any) (string, error) {
				article, err := ParseReadabilityContent(bytes.NewReader([]byte(outHtml)), pageURL)
				if err != nil {
					return "", errors.Wrapf(err, "failed to parse readability content from page")
				}
				return article.TextContent, nil
			}),
		},
		"pandoc": {
			desc: "accepts pandoc format to generate content with\n" +
				"parses current page's HTML with pandoc algorithms \n" +
				"returns readable content of the page",
			run: b.toReadableContentFunction(ctx, pCtx,
				func(call otto.FunctionCall) []any {
					return []any{call.Argument(0).String()}
				},
				func(ctx context.Context, outHtml string, pageURL string, args ...any) (string, error) {
					var format string
					if len(args) != 1 {
						return "", errors.Errorf("format argument must be provided")
					}
					if fmtString, fmtOk := args[0].(string); !fmtOk {
						return "", errors.Errorf("format argument must be string")
					} else {
						format = fmtString
					}
					return b.pandoc.HtmlToMarkdown(ctx, outHtml, format)
				}),
		},
	}
}

func (b *browser) doWithNthNode(ctx context.Context, selector any, index int, doFunc func(*cdp.Node, *runtime.RemoteObject) error) error {
	return chromedp.QueryAfter(selector, func(ctx context.Context, execCtx runtime.ExecutionContextID, nodes ...*cdp.Node) error {
		if len(nodes) < 1 {
			return fmt.Errorf("selector %q did not return any nodes", selector)
		}
		if len(nodes) < int(index)+1 || index < 0 {
			return fmt.Errorf("index (%d) is out of range (%d) of nodes list", index, len(nodes))
		}
		node := nodes[index]
		r, err := dom.ResolveNode().WithNodeID(node.NodeID).Do(ctx)
		if err != nil {
			return err
		}
		return doFunc(node, r)
	}).Do(ctx)
}

func (b *browser) waitReadabilityContent(pCtx *browserProgramCtx) func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
	return func(call otto.FunctionCall) (chromedp.Action, valuePromise) {
		return b.withActionOptions(chromedp.ActionFunc(func(ctx context.Context) (err error) {
			b.log.Infof(ctx, "[chrome]: waiting until readability is able to extract content...")
			for {
				b.log.Infof(ctx, "[chrome]: still waiting until readability is able to extract content...")
				select {
				case <-ctx.Done():
					b.log.Infof(ctx, "[chrome]: timeout waiting for readability able to extract content...")
					return err // nolint: gofumpt
				default:
					time.Sleep(1 * time.Second)
					var article readability.Article
					var parsedURL *url.URL
					var currentURL, outerHtml string

					currentURL, outerHtml, err = b.geHtmlElementsForReadability(ctx)
					if err != nil {
						b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to find visible elements on the page")
						pCtx.addLogEntry(fmt.Sprintf("Failed to find visible elements on the page: %v", err))
						continue
					}
					if parsedURL, err = url.Parse(currentURL); err != nil {
						continue
					}
					if article, err = readability.FromReader(strings.NewReader(outerHtml), parsedURL); err != nil {
						continue
					}
					if article.Title == "" {
						if err = chromedp.Title(&article.Title).Do(ctx); err != nil {
							continue
						}
					}
					if article.Content == "" {
						b.log.Warnf(ctx, "[chrome]: readability couldn't extract content from outerHtml...")
						pCtx.addLogEntry("Readability couldn't extract any content from the page")
						err = errors.Errorf("readability couldn't extract any content from the page")
						continue
					} else {
						pCtx.readabilityArticle = lo.ToPtr(dto.ToArticle(article))
						return err // nolint: gofumpt
					}
				}
			}
		}), noValue, b.parseActionOpts(call, 0), pCtx)
	}
}

func (b *browser) llmText(ctx context.Context, textKind string, pCtx *browserProgramCtx, opOptions ...opOption) (string, error) {
	ctx = b.log.WithValue(ctx, "textKind", textKind)
	textElementValue, err := retry.With(retry.Config[string]{
		MaxRetries: 3,
		AttemptErrorCallback: func(i int, err error) {
			b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to extract text value %q: attempt %d of 3", textKind, i)
		},
		Action: func() (string, error) {
			resp, err := b.llmProcessHtmlElements(ctx, llmElReq{
				kind: "text-element",
				prompt: fmt.Sprintf("Given the html elements above, find element containing text similar to %q. Extract `data-llm-id` attribute from it.\n"+
					"Respond with the 1 full value of the `data-llm-id` attribute, without any comments or explanations.", textKind),
				elems: []string{"textarea", "input", "button", "span", "div", "p", "b", "strong", "h1", "h2", "h3", "h4"},
			}, pCtx)
			if err != nil {
				b.log.Warnf(b.log.WithValue(ctx, "error", err), "failed to get element displaying text")
				pCtx.addLogEntry(fmt.Sprintf("Failed to get element displaying text for textKind %q: %v", textKind, err))
				return "", nil
			}
			selector := fmt.Sprintf(`[data-llm-id="%s"]`, llmExtractSingleSelectorFromResponse(resp))
			var textElementValue string
			if err := b.doWithTimeout(ctx, time.Second*2, func(ctx context.Context) error {
				return chromedp.Text(selector, &textElementValue).Do(ctx)
			}); err != nil {
				return "", nil
			}
			return textElementValue, nil
		},
	})
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf("%s\n---\n%s", lo.FromPtr(textElementValue),
		fmt.Sprintf("Does the text above look like a message related to %q? "+
			"Respond with a single string `yes` or `no`, without comments or explanations", textKind))

	pCtx.addLogEntry(fmt.Sprintf("LLM Text Analysis Request - textKind: %q, prompt: %q", textKind, prompt))

	llmClient, err := b.newLlmClient(pCtx.bOpts)
	if err != nil {
		return "", err
	}
	llmResp, err := llmClient.Generate(ctx, llm.GenerateRequest{
		Prompt: prompt,
	})
	if lo.FromPtr(llmResp).GenerationInfo != nil {
		pCtx.generationInfo = b.mergeGenerationInfo(pCtx.generationInfo, llmResp.GenerationInfo)
	}
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Text Analysis Error - textKind: %q, error: %v", textKind, err))
		return "", nil
	}

	pCtx.addLogEntry(fmt.Sprintf("LLM Text Analysis Response - textKind: %q, response: %q", textKind, llmResp.Response))
	if strings.TrimSpace(llmResp.Response) == "yes" {
		return lo.FromPtr(textElementValue), nil
	}
	return "", nil
}

func (b *browser) llmSetValue(ctx context.Context, description, value string, verify bool, pCtx *browserProgramCtx, opOptions ...opOption) error {
	pCtx.addLogEntry(fmt.Sprintf("LLM Set Value Request - description: %q, value: %q, verify: %t", description, value, verify))

	doSet := func() error {
		return b.doWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			_, err := b.llmFindAndSetValue(ctx, llmElReq{
				kind: description,
				prompt: fmt.Sprintf("Given the html elements above, extract `data-llm-id` attribute value "+
					`of the <input> or <textarea> element with "name", "id", "value" or "title" representing %s.\n`+
					"Respond with 1 full value of the `data-llm-id` attribute, without any comments or explanations.", description),
				elems: []string{"textarea", "input"},
			}, value, pCtx, verify)
			return err
		})
	}
	var err error
	if verify {
		err = doSet()
	} else {
		_ = doSet()
	}
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Set Value Error - description: %q, error: %v", description, err))
		return errors.Wrapf(err, "failed to enter value on %q", description)
	}
	pCtx.addLogEntry(fmt.Sprintf("LLM Set Value Success - description: %q", description))
	return nil
}

func (b *browser) llmClick(ctx context.Context, description string, elems []string, pCtx *browserProgramCtx, opOptions ...opOption) error {
	if len(elems) == 0 {
		elems = []string{"input", "span", "button", "div"}
	}
	pCtx.addLogEntry(fmt.Sprintf("LLM Click Request - description: %q, elements: %v", description, elems))

	_, err := b.llmFindAndClick(ctx, llmElReq{
		kind: description,
		prompt: fmt.Sprintf("Given the html elements above, extract `data-llm-id` attribute value for %s.\n"+
			"Respond with the full 1 value of the attribute as a string, without any comments or explanations.", description),
		elems: elems,
	}, pCtx)
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Click Error - description: %q, error: %v", description, err))
		return errors.Wrapf(err, "failed to click on %q", description)
	}
	pCtx.addLogEntry(fmt.Sprintf("LLM Click Success - description: %q", description))
	return nil
}

func (b *browser) llmLogin(ctx context.Context, emailOrLogin string, password string, pCtx *browserProgramCtx, opOptions ...opOption) error {
	pCtx.addLogEntry(fmt.Sprintf("LLM Login Request - emailOrLogin: %q", emailOrLogin))

	usernameSelector, err := b.llmFindAndSetValue(ctx, llmElReq{
		kind: "username",
		prompt: "Given the html elements above, extract `data-llm-id` attribute value " +
			`of the <input> or <textarea> element with "name", "id" or "title" representing "username" OR "email".\n` +
			"Respond with 1 full value of the `data-llm-id` attribute, without any comments or explanations.",
		elems: []string{"textarea", "input"},
	}, emailOrLogin, pCtx, true)
	if err != nil {
		return errors.Wrapf(err, "failed to enter username/email")
	}
	pwdSelector, err := b.llmFindAndSetValue(ctx, llmElReq{
		kind: "password",
		prompt: "Given the html elements above, extract `data-llm-id` attribute value of the element " +
			"with `name`, `id` or `title` representing password.\n" +
			"Respond with the 1 full value of the `data-llm-id` attribute, without any comments or explanations.",
		elems: []string{"textarea", "input"},
	}, password, pCtx, true)
	if err != nil {
		return errors.Wrapf(err, "failed to enter password")
	}

	var usernameHtml, pwdHtml string
	_ = b.doWithTimeout(ctx, 100*time.Millisecond, func(ctx context.Context) error {
		_ = chromedp.OuterHTML(usernameSelector, &usernameHtml).Do(ctx)
		_ = chromedp.OuterHTML(pwdSelector, &pwdHtml).Do(ctx)
		return nil
	})
	ctx = b.log.WithValues(ctx, map[string]any{
		"usernameSelector": usernameSelector,
		"passwordSelector": pwdSelector,
		"usernameHtml":     usernameHtml,
		"passwordHtml":     pwdHtml,
	})

	b.log.Infof(ctx, "found and set values of username and password, now trying to click login button after 1s...")

	if loginButtonSelector, err := b.llmFindAndClick(ctx, llmElReq{
		kind: "login-button",
		prompt: "Given the html elements above, extract `data-llm-id` attribute value for \"Login\" or \"Submit\" button " +
			"which should be located after username and password.\n" +
			"Respond with the full 1 value of the attribute as a string, without any comments or explanations.",
		elems: []string{"input", "span", "button"},
	}, pCtx); err == nil {
		ctx = b.log.WithValue(ctx, "loginButtonSelector", loginButtonSelector)
		b.log.Infof(ctx, "found and clicked login button")
	} else if err = b.doWithTimeout(ctx, time.Second*1, func(ctx context.Context) error {
		return chromedp.SendKeys(pwdSelector, "\n").Do(ctx)
	}); err == nil {
		b.log.Infof(ctx, "sent [ENTER] to password field and it seems to have worked")
	} else if err = b.doWithTimeout(ctx, time.Second*1, func(ctx context.Context) error {
		return chromedp.SendKeys(usernameSelector, "\n").Do(ctx)
	}); err == nil {
		b.log.Infof(ctx, "sent [ENTER] to username field and it seems to have worked")
	} else {
		pCtx.addLogEntry(fmt.Sprintf("LLM Login Error - failed to press login button or submit form: %v", err))
		return errors.Wrapf(err, "failed to press login button or submit form via ENTER")
	}

	if err := chromedp.WaitReady("body").Do(ctx); err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Login Error - failed to wait for body after login: %v", err))
		return errors.Wrapf(err, "failed to wait until body is ready after login")
	}

	pCtx.addLogEntry("LLM Login Success - login completed successfully")
	return nil
}

func (b *browser) llmSendKeys(ctx context.Context, description, keys string, pCtx *browserProgramCtx, opOptions ...opOption) error {
	pCtx.addLogEntry(fmt.Sprintf("LLM Send Keys Request - description: %q, keys: %q", description, keys))

	resp, err := b.llmProcessHtmlElements(ctx, llmElReq{
		kind: description,
		prompt: fmt.Sprintf("Given the html elements above, extract `data-llm-id` attribute value "+
			`of the <input> or <textarea> element with "name", "id" or "title" representing %q.\n`+
			"Respond with 1 full value of the `data-llm-id` attribute, without any comments or explanations.", description),
		elems: []string{"input"},
	}, pCtx)
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Send Keys Error - description: %q, error: %v", description, err))
		return errors.Wrapf(err, "failed to find css selector for element to send keys to")
	}
	selector := fmt.Sprintf(`[data-llm-id="%s"]`, llmExtractSingleSelectorFromResponse(resp))
	b.logElementBySelector(ctx, "found element to send keys to", selector, pCtx)

	err = b.doWithTimeout(ctx, time.Second*2, func(ctx context.Context) error {
		return chromedp.SendKeys(selector, keys).Do(ctx)
	})
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Send Keys Error - description: %q, selector: %q, error: %v", description, selector, err))
		return err
	}
	pCtx.addLogEntry(fmt.Sprintf("LLM Send Keys Success - description: %q, selector: %q", description, selector))
	return nil
}

func (b *browser) llmFindAndClick(ctx context.Context, req llmElReq, pCtx *browserProgramCtx) (string, error) {
	pCtx.addLogEntry(fmt.Sprintf("LLM Find And Click Request - kind: %q, elements: %v", req.kind, req.elems))

	sel, err := retry.With(retry.Config[string]{
		MaxRetries: 3,
		AttemptErrorCallback: func(i int, err error) {
			b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to click element: attempt %d of 3", i)
			pCtx.addLogEntry(fmt.Sprintf("Retry attempt %d/3 failed to click element: %v", i+1, err))
		},
		Action: func() (string, error) {
			resp, err := b.llmProcessHtmlElements(ctx, req, pCtx)
			if err != nil {
				return "", errors.Wrapf(err, "failed to find css selector for element")
			}
			selector := fmt.Sprintf(`[data-llm-id="%s"]`, llmExtractSingleSelectorFromResponse(resp))
			ctx = b.log.WithValue(ctx, "elementSelector", selector)
			var elementHtml string
			_ = b.doWithTimeout(ctx, 500*time.Millisecond, func(ctx context.Context) error {
				_ = chromedp.OuterHTML(selector, &elementHtml).Do(ctx)
				return nil
			})
			ctx = b.log.WithValue(ctx, "elementHtml", elementHtml)
			b.logElementBySelector(ctx, "found element to click on", selector, pCtx)
			if ctx.Value(includeInvisibleOption) != nil && ctx.Value(includeInvisibleOption).(bool) {
				b.logElementBySelector(ctx, "scrolling into view of element", selector, pCtx)
				if err := b.doWithTimeout(ctx, time.Second*3, func(ctx context.Context) error {
					return chromedp.ScrollIntoView(selector).Do(ctx)
				}); err != nil {
					return selector, errors.Wrapf(err, "failed to scroll into view of element")
				}
			}
			if err := chromedp.QueryAfter(selector, func(ctx context.Context, execCtx runtime.ExecutionContextID, nodes ...*cdp.Node) error {
				boxes, err := dom.GetContentQuads().WithNodeID(nodes[0].NodeID).Do(ctx)
				if err != nil {
					return err
				}
				b.log.Infof(ctx, "attempting to click element at position: %v", boxes[0])
				return nil
			}, chromedp.NodeVisible, chromedp.ByQuery).Do(ctx); err != nil {
				b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to debug node for clicking")
				pCtx.addLogEntry(fmt.Sprintf("Failed to debug node for clicking: %v", err))
			}
			if err := b.doWithTimeout(ctx, time.Second*5, func(ctx context.Context) error {
				return chromedp.Click(selector, chromedp.ByQuery).Do(ctx)
			}); err != nil {
				return selector, errors.Wrapf(err, "failed to click element")
			}
			return selector, nil
		},
	})
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Find And Click Error - kind: %q, error: %v", req.kind, err))
		return lo.FromPtr(sel), err
	}
	pCtx.addLogEntry(fmt.Sprintf("LLM Find And Click Success - kind: %q, selector: %q", req.kind, lo.FromPtr(sel)))
	return lo.FromPtr(sel), err
}

func (b *browser) llmFindAndSetValue(ctx context.Context, req llmElReq, value string, pCtx *browserProgramCtx, verify bool, opOptions ...opOption) (string, error) {
	pCtx.addLogEntry(fmt.Sprintf("LLM Find And Set Value Request - kind: %q, verify: %t", req.kind, verify))

	sel, err := retry.With(retry.Config[string]{
		MaxRetries: 3,
		AttemptErrorCallback: func(i int, err error) {
			b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to set value %q on element: attempt %d of 3", b.sanitizeSecrets(ctx, "", []string{value}, pCtx), i)
			pCtx.addLogEntry(fmt.Sprintf("Retry attempt %d/3 failed to set value on element: %v", i+1, err))
		},
		Action: func() (string, error) {
			resp, err := b.llmProcessHtmlElements(ctx, req, pCtx)
			if err != nil {
				return "", errors.Wrapf(err, "failed to find selector for element on the page")
			}
			selector := fmt.Sprintf(`[data-llm-id="%s"]`, llmExtractSingleSelectorFromResponse(resp))
			return selector, b.setValueOnControl(ctx, selector, value, pCtx, verify)
		},
	})
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM Find And Set Value Error - kind: %q, error: %v", req.kind, err))
		return lo.FromPtr(sel), err
	}
	pCtx.addLogEntry(fmt.Sprintf("LLM Find And Set Value Success - kind: %q, selector: %q", req.kind, lo.FromPtr(sel)))
	return lo.FromPtr(sel), err
}

func (b *browser) setValueOnControl(ctx context.Context, selector string, value string, pCtx *browserProgramCtx, verify bool) error {
	ctx = b.logElementBySelector(ctx, fmt.Sprintf("found element to set value %q on", b.sanitizeSecrets(ctx, "", []string{value}, pCtx)), selector, pCtx)

	if err := b.doWithTimeout(ctx, time.Second*1, func(ctx context.Context) error {
		return chromedp.WaitReady(selector).Do(ctx)
	}); err != nil {
		return errors.Wrapf(err, "element with selector %q is not ready", selector)
	}

	defer func() {
		b.logElementValueBySelector(ctx, "set value on element", selector)
		// focus "<body>" to distract focus from element
		_ = b.doWithTimeout(ctx, 1*time.Second, func(ctx context.Context) error {
			return chromedp.Focus("body").Do(ctx)
		})
	}()

	b.log.Infof(ctx, "clicking element and sending key events")
	if err := b.doWithTimeout(ctx, time.Second*2, func(ctx context.Context) error {
		return chromedp.Click(selector).Do(ctx)
	}); err == nil {
		b.log.Infof(ctx, "clicked element to focus, sending key events for %q", b.sanitizeSecrets(ctx, "", []string{value}, pCtx))
		for _, character := range value {
			if err = b.doWithTimeout(ctx, time.Second*5, func(ctx context.Context) error {
				time.Sleep(10 * time.Millisecond)
				err := chromedp.KeyEvent(fmt.Sprintf("%c", character)).Do(ctx)
				return err
			}); err != nil {
				b.log.Warnf(b.log.WithValue(ctx, "error", err.Error()), "failed to send key %c event, fallback to set value", character)
				pCtx.addLogEntry(fmt.Sprintf("Failed to send key '%c' event, fallback to set value: %v", character, err))
				break
			}
		}
	} else {
		b.log.Warnf(b.log.WithValue(ctx, "error", err.Error()), "failed to click element")
		pCtx.addLogEntry(fmt.Sprintf("Failed to click element: %v", err))
	}

	if !verify {
		b.log.Infof(ctx, "skipping verify of value for element %q", selector)
		return nil
	}

	currentValue, _ := b.getElementValueBySelector(ctx, selector)
	if currentValue != value {
		b.log.Warnf(ctx, "value on selector still doesn't match required")
		pCtx.addLogEntry(fmt.Sprintf("First value verification failed - expected: %q, actual: %q", b.sanitizeSecrets(ctx, "", []string{value}, pCtx), b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx)))
	} else {
		b.log.Warnf(ctx, "value on selector now has value of %q", b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx))
		pCtx.addLogEntry(fmt.Sprintf("First value verification successful: %q", b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx)))
		return nil
	}

	b.log.Infof(ctx, "setting value via sending keys %q to selector %q on element", value, selector)
	if err := b.doWithTimeout(ctx, time.Second*2, func(ctx context.Context) error {
		return chromedp.SendKeys(selector, value).Do(ctx)
	}); err != nil {
		b.log.Warnf(b.log.WithValue(ctx, "error", err.Error()), "failed to send keys to selector")
		pCtx.addLogEntry(fmt.Sprintf("Failed to send keys to selector: %v", err))
	}
	currentValue, _ = b.getElementValueBySelector(ctx, selector)
	if currentValue != value {
		b.log.Warnf(ctx, "value on selector still doesn't match required")
		pCtx.addLogEntry(fmt.Sprintf("Second value verification failed - expected: %q, actual: %q", b.sanitizeSecrets(ctx, "", []string{value}, pCtx), b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx)))
	} else {
		b.log.Warnf(ctx, "value on selector now has value of %q", b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx))
		pCtx.addLogEntry(fmt.Sprintf("Second value verification successful: %q", b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx)))
		return nil
	}

	b.log.Infof(ctx, "setting value directly %q on element", value)
	if err := b.doWithTimeout(ctx, time.Second*1, func(ctx context.Context) error {
		return chromedp.SetValue(selector, value).Do(ctx)
	}); err != nil {
		b.log.Warnf(b.log.WithValue(ctx, "error", err.Error()), "failed to set value to selector, fallback to click and sendKeys")
		pCtx.addLogEntry(fmt.Sprintf("Failed to set value directly on selector: %v", err))
	}

	currentValue, _ = b.getElementValueBySelector(ctx, selector)
	if currentValue != value {
		pCtx.addLogEntry(fmt.Sprintf("Final value verification failed - expected: %q, actual: %q", b.sanitizeSecrets(ctx, "", []string{value}, pCtx), b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx)))
		return errors.Errorf("value on selector still doesn't match required")
	}
	pCtx.addLogEntry(fmt.Sprintf("Final value verification successful: %q", b.sanitizeSecrets(ctx, "", []string{currentValue}, pCtx)))
	return nil
}

func (b *browser) getElementValueBySelector(ctx context.Context, selector string) (string, error) {
	var value string
	err := chromedp.Value(selector, &value).Do(ctx)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get value by selector %q", selector)
	}
	return value, nil
}

func (b *browser) logElementValueBySelector(ctx context.Context, msg, selector string) {
	var value string
	_ = chromedp.Value(selector, &value).Do(ctx)
	b.log.Infof(b.log.WithValue(ctx, "value", value), msg)
}

func (b *browser) logElementBySelector(ctx context.Context, msg, selector string, pCtx *browserProgramCtx) context.Context {
	var element string
	_ = b.doWithTimeout(ctx, time.Second*1, func(ctx context.Context) error {
		return chromedp.OuterHTML(selector, &element).Do(ctx)
	})
	ctx = b.log.WithValue(ctx, "element", element)
	b.log.Infof(ctx, msg)
	pCtx.addLogEntry(fmt.Sprintf("%q for selector %q (element %q)", msg, selector, element))
	return ctx
}

var (
	selectorRegexp          = regexp.MustCompile("`(.+)`")
	quotesRegexp            = regexp.MustCompile("\"(.+)\"")
	notAllowedIdCharsRegexp = regexp.MustCompilePOSIX("[^A-z0-9]+")
)

func generateAttributeValueWithSalt(value string) string {
	if value == "" {
		return ""
	}
	// "id" substring in element's id confuses LLM
	salt := lo.RandomString(5, lo.LowerCaseLettersCharset)
	prefix := strings.ReplaceAll(strings.ToLower(notAllowedIdCharsRegexp.ReplaceAllString(value, "_")), "id", "")
	return prefix + lo.If(prefix == "", salt).Else("-"+salt)
}

func llmExtractSingleSelectorFromResponse(resp string) string {
	selector := resp
	// The model may fence the selector in backticks or wrap it in double quotes.
	// Unwrap at most one of the two: a backticked selector may legitimately
	// contain quoted attribute values, e.g. `input[name="email"]`.
	if extracted := selectorRegexp.FindStringSubmatch(resp); len(extracted) > 1 {
		selector = strings.TrimSpace(extracted[1])
	} else if extracted := quotesRegexp.FindStringSubmatch(resp); len(extracted) > 1 {
		selector = strings.TrimSpace(extracted[1])
	}
	selector = strings.TrimSpace(strings.Split(selector, "\n")[0])
	multiSelector := strings.Split(selector, ",")
	if len(multiSelector) > 1 {
		selector = multiSelector[0]
	}
	return selector
}

func (b *browser) doWithTimeout(ctx context.Context, timeout time.Duration, doFunc func(ctx context.Context) error) error {
	withTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return doFunc(withTimeout)
}

type llmElReq struct {
	kind    string
	prompt  string
	setAttr *setAttr
	elems   []string
}
type setAttr struct {
	random bool
	name   string
}

func (b *browser) llmProcessHtmlElements(ctx context.Context, req llmElReq, pCtx *browserProgramCtx) (string, error) {
	if req.setAttr == nil {
		req.setAttr = &setAttr{
			name:   "data-llm-id",
			random: true,
		}
	}
	formElementsHtml, err := b.findHtmlElements(ctx, req)
	if err != nil {
		return "", errors.Wrapf(err, "failed to extract visible html elements")
	}
	if b.requestsDebug {
		ctx = b.log.WithValue(ctx, "formElementsHtml", formElementsHtml)
	}
	var res string
	b.log.Infof(ctx, "Waiting for LLM to generate response for %q...", req.kind)

	prompt := fmt.Sprintf("%s\n---\n%s", formElementsHtml, req.prompt)
	pCtx.addLogEntry(fmt.Sprintf("LLM HTML Elements Request - kind: %q, prompt: %q", req.kind, prompt))

	llmClient, err := b.newLlmClient(pCtx.bOpts)
	if err != nil {
		return "", err
	}
	resp, err := llmClient.Generate(ctx, llm.GenerateRequest{
		Prompt: prompt,
	})
	if lo.FromPtr(resp).GenerationInfo != nil {
		pCtx.generationInfo = b.mergeGenerationInfo(pCtx.generationInfo, resp.GenerationInfo)
	}
	if err != nil {
		pCtx.addLogEntry(fmt.Sprintf("LLM HTML Elements Error - kind: %q, error: %v", req.kind, err))
		return "", errors.Wrapf(err, "failed to generate llm response for elements")
	}
	res = resp.Response
	pCtx.addLogEntry(fmt.Sprintf("LLM HTML Elements Response - kind: %q, response: %q", req.kind, res))
	b.log.Infof(b.log.WithValue(ctx, "llmHtmlResponse", resp), "Got response for %q", req.kind)
	return res, nil
}

func (b *browser) findHtmlElements(ctx context.Context, req llmElReq) (string, error) {
	formElementsHtmlBuf := strings.Builder{}
	selector := strings.Join(req.elems, ", ")
	includeInvisible := false
	if ctx.Value(selectorOption) != nil {
		selector = ctx.Value(selectorOption).(string)
	}
	if ctx.Value(includeInvisibleOption) != nil {
		includeInvisible = ctx.Value(includeInvisibleOption).(bool)
	}
	// add allowed tags option to request
	if addAllowTagsValue := ctx.Value(allowTagsOption); addAllowTagsValue != nil {
		if addAllowTagsValueString, ok := addAllowTagsValue.(string); ok {
			req.elems = append(req.elems, strings.Split(addAllowTagsValueString, ",")...)
		}
	}
	if err := chromedp.Query(selector,
		chromedp.After(func(ctx context.Context, id runtime.ExecutionContextID, nodes ...*cdp.Node) error {
			nodes = lo.Filter(nodes, func(node *cdp.Node, _ int) bool {
				return lo.ContainsBy(req.elems, func(tagName string) bool {
					return strings.ToLower(node.LocalName) == tagName
				})
			})
			if req.setAttr != nil {
				errG := &errgroup.Group{}
				lop.ForEach(nodes, func(node *cdp.Node, _ int) {
					if err := dom.RequestChildNodes(node.NodeID).WithDepth(-1).Do(ctx); err != nil {
						b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to request child nodes")
					}
					b.setAttributeOnAllElementsRecursively(ctx, *req.setAttr, node, errG)
				})
				_ = errG.Wait()
			}
			alreadyRenderedIds := &sync.Map{}
			lop.ForEach(nodes, func(node *cdp.Node, _ int) {
				if _, rendered := alreadyRenderedIds.Load(node.NodeID); rendered {
					return
				}
				r, err := dom.ResolveNode().WithNodeID(node.NodeID).Do(ctx)
				if err != nil {
					b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to resolve node with id")
					return
				}
				if err == nil && (includeInvisible || b.isVisible(ctx, func(p *runtime.CallFunctionOnParams) *runtime.CallFunctionOnParams {
					return p.WithObjectID(r.ObjectID)
				})) {
					outerHtml, err := dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
					if err != nil {
						b.log.Errorf(b.log.WithValue(ctx, "error", err.Error()), "failed to get element's outer html")
						return
					}
					formElementsHtmlBuf.WriteString(outerHtml)
					appendRenderedNodeIDs(node, alreadyRenderedIds)
				}
			})
			return nil
		})).Do(ctx); err != nil {
		return "", err
	}
	finalHtml := formElementsHtmlBuf.String()
	finalHtml = stripAllHtmlTagsExcept(ctx, finalHtml, lo.FromPtr(req.setAttr), req.elems...)
	if !includeInvisible {
		finalHtml = b.removeInvisibleElements(ctx, finalHtml, lo.FromPtr(req.setAttr))
	}
	return finalHtml, nil
}

func (b *browser) isVisible(ctx context.Context, opts chromedp.CallOption) bool {
	var visible bool
	_ = chromedp.CallFunctionOn(embed.VisibleJS, &visible, opts).Do(ctx)
	return visible
}

func (b *browser) removeInvisibleElements(ctx context.Context, elementsHtml string, attr setAttr) string {
	q, err := htmlquery.Parse(strings.NewReader(elementsHtml))
	if err != nil {
		b.log.Errorf(b.log.WithValue(ctx, "error", err), "failed to parse final html")
		return elementsHtml
	}
	errG := &errgroup.Group{}
	q = b.removeInvisibleElementsFrom(ctx, q, attr, errG)
	_ = errG.Wait()
	return htmlquery.OutputHTML(q, true)
}

func (b *browser) removeInvisibleElementsFrom(ctx context.Context, node *html.Node, attr setAttr, errG *errgroup.Group) *html.Node {
	firstChild := node.FirstChild
	if firstChild != nil {
		errG.Go(func() error {
			if idAttr, found := lo.Find(firstChild.Attr, func(a html.Attribute) bool {
				return a.Key == attr.name
			}); found {
				if err := b.doWithTimeout(ctx, 500*time.Millisecond, func(ctx context.Context) error {
					return chromedp.WaitVisible(fmt.Sprintf("[%s=\"%s\"]", idAttr.Key, idAttr.Val)).Do(ctx)
				}); err != nil {
					node.RemoveChild(firstChild)
				}
			}
			return nil
		})
		errG.Go(func() error {
			b.removeInvisibleElementsFrom(ctx, firstChild, attr, errG)
			return nil
		})
		errG.Go(func() error {
			for sib := firstChild.NextSibling; sib != nil; sib = sib.NextSibling {
				b.removeInvisibleElementsFrom(ctx, sib, attr, errG)
			}
			return nil
		})
	}
	return node
}

func (b *browser) setAttributeOnAllElementsRecursively(ctx context.Context, setAttr setAttr, node *cdp.Node, errG *errgroup.Group) {
	lop.ForEach(node.Children, func(child *cdp.Node, _ int) {
		errG.Go(func() error {
			b.setAttributeOnAllElementsRecursively(ctx, setAttr, child, errG)
			return nil
		})
	})
	var origId string
	if setAttr.random {
		origId = lo.RandomString(10, lo.LowerCaseLettersCharset)
	} else if origId = generateAttributeValueWithSalt(node.AttributeValue("id")); origId == "" {
		origId = generateAttributeValueWithSalt(node.AttributeValue("name"))
	}
	if origId != "" {
		_ = dom.SetAttributeValue(node.NodeID, setAttr.name, origId).Do(ctx)
	}
}

func (b *browser) withIframeBySelector(ctx context.Context, selector string, action func(ctx context.Context, opts ...chromedp.QueryOption) error) error {
	var iframeUrl string
	var found bool
	err := chromedp.Run(ctx, chromedp.AttributeValue(selector, "src", &iframeUrl, &found))
	if err != nil {
		return err
	}
	if !found {
		return errors.Errorf("iframe not found with selector %q", selector)
	}

	targets, _ := chromedp.Targets(ctx)
	var tgt *target.Info
	for _, t := range targets {
		if t.Type == "iframe" && strings.Contains(t.URL, iframeUrl) {
			tgt = t
		}
	}
	var iframe *cdp.Node
	var iframes []*cdp.Node
	if err := chromedp.Run(ctx, chromedp.Nodes(`iframe`, &iframes)); err != nil {
		return errors.Errorf("iframe not found with selector %q", selector)
	}
	for _, f := range iframes {
		if val, _ := f.Attribute("src"); val == iframeUrl {
			iframe = f
		}
	}
	if tgt != nil {
		iCtx, _ := chromedp.NewContext(ctx, chromedp.WithTargetID(tgt.TargetID))
		var innerBody string
		_ = chromedp.InnerHTML("body", &innerBody, chromedp.FromNode(iframe)).Do(iCtx)
		fmt.Println(innerBody)

		return action(iCtx, chromedp.FromNode(iframe))
	}
	return errors.Errorf("failed to switch to iframe target by selector %q", selector)
}

func (b *browser) takeScreenshot(ctx context.Context, name string, pCtx *browserProgramCtx) error {
	screenshot, err := retry.With(retry.Config[[]byte]{
		Action: func() ([]byte, error) {
			var screenshot []byte
			err := chromedp.FullScreenshot(&screenshot, 90).Do(ctx)
			return screenshot, errors.Wrapf(err, "failed to take screenshot of the page")
		},
		MaxRetries: 2,
		AttemptErrorCallback: func(i int, err error) {
			b.log.Infof(b.log.WithValue(ctx, "error", err.Error()), "Failed to take screenshot with name %q (attempt %d of 2)", name, i)
			time.Sleep(1 * time.Second)
		},
		NoMoreAttemptsCallback: nil,
	})
	if err != nil {
		return errors.Wrapf(err, "failed to take screenshot %q", name)
	}
	b.log.Infof(ctx, "Took screenshot of size %d with name %q", len(lo.FromPtr(screenshot)), name)
	pCtx.screenshots[name] = lo.FromPtr(screenshot)
	return nil
}

func (b *browser) sanitizeSecrets(ctx context.Context, actionName string, vals []string, pCtx *browserProgramCtx) []string {
	return lo.Map(vals, func(val string, _ int) string {
		secretArgs := ctx.Value(secretArgsOption) != nil && ctx.Value(secretArgsOption).(bool)
		if lo.ContainsBy(lo.Values(pCtx.secrets), func(secret string) bool {
			return secret == val
		}) || secretArgs {
			return "<secret>"
		}
		return val
	})
}

func (b *browser) mergeGenerationInfo(info map[string]any, info2 map[string]any) map[string]any {
	if info2 == nil {
		return info
	}
	if info == nil {
		info = make(map[string]any)
	}
	info2 = lo.Assign(info2) // clone
	info = lo.Assign(info)   // clone
	for k, v := range info2 {
		if current, exists := info[k]; exists {
			if curVal, ok := current.(int); !ok {
				info[k] = v
			} else if updVal, ok := info[k].(int); ok {
				info[k] = curVal + updVal
			}
		} else {
			info[k] = v
		}
	}
	return info
}

func appendRenderedNodeIDs(node *cdp.Node, ids *sync.Map) {
	for _, child := range node.Children {
		appendRenderedNodeIDs(child, ids)
		ids.Store(child.NodeID, true)
	}
}

func stripAllHtmlTagsExcept(ctx context.Context, html string, setAttr setAttr, allowedTags ...string) string {
	p := bluemonday.NewPolicy()
	p.AllowStandardAttributes()
	p.AllowStandardURLs()
	p.AllowTables()
	p.AllowDataAttributes()
	p.AllowStandardAttributes()
	if addAllowTagsValue := ctx.Value(allowTagsOption); addAllowTagsValue != nil {
		if addAllowTagsValueString, ok := addAllowTagsValue.(string); ok {
			allowedTags = append(allowedTags, strings.Split(addAllowTagsValueString, ",")...)
		}
	}
	p = p.AllowElementsContent(allowedTags...)
	p = p.AllowElements(allowedTags...)
	p.AllowLists()
	p.AllowStyling()
	p = p.AllowUnsafe(true)
	allowedAttrs := []string{setAttr.name, "type", "title", "name", "id", "class", "value", "aria-label"}
	if addAllowAttrsValue := ctx.Value(allowAttributesOption); addAllowAttrsValue != nil {
		if addAllowAttributesValueString, ok := addAllowAttrsValue.(string); ok {
			allowedAttrs = append(allowedAttrs, strings.Split(addAllowAttributesValueString, ",")...)
		}
	}
	p = p.AllowAttrs(allowedAttrs...).Globally()
	return p.Sanitize(html)
}

func (b *browser) disablePdfViewer() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		err := chromedp.Navigate("chrome://settings/content/pdfDocuments").Do(ctx)
		if err != nil {
			return err
		}
		// old approach
		err = b.doWithTimeout(ctx, 2*time.Second, func(ctx context.Context) error {
			return chromedp.Click(`
document.querySelector("body > settings-ui").shadowRoot.querySelector("#main").shadowRoot.querySelector("settings-basic-page").shadowRoot.querySelector('settings-section[section=privacy] > settings-privacy-page').shadowRoot.querySelector('settings-animated-pages > settings-subpage[route-path="/content/pdfDocuments"] settings-radio-group settings-collapse-radio-button').shadowRoot.querySelector('#radioCollapse .disc-wrapper[tabindex="-1"]')
`, chromedp.ByJSPath).Do(ctx)
		})
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			err = b.doWithTimeout(ctx, 2*time.Second, func(ctx context.Context) error {
				return chromedp.Click(`
		document.querySelector("body > settings-ui").shadowRoot.querySelector("#main").shadowRoot.querySelector("#privacy > settings-privacy-page-index").shadowRoot.querySelector("#siteSettingsPdfDocuments").shadowRoot.querySelector("settings-subpage > div > settings-radio-group > settings-collapse-radio-button:nth-child(1)").shadowRoot.querySelector("#labelWrapper")
`, chromedp.ByJSPath).Do(ctx)
			})
		}
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			// if timeout, trying out new approach
			err = b.doWithTimeout(ctx, 2*time.Second, func(ctx context.Context) error {
				return chromedp.Click(`
document.querySelector("body > settings-ui").shadowRoot.querySelector("#main").shadowRoot.querySelector("#privacy > settings-privacy-page-index").shadowRoot.querySelector("#old").shadowRoot.querySelector("#basicPage > settings-section > settings-privacy-page").shadowRoot.querySelector("#pages > settings-subpage > div > settings-radio-group > settings-collapse-radio-button:nth-child(1)").shadowRoot.querySelector("#label")
`, chromedp.ByJSPath).Do(ctx)
			})
		}

		if err != nil {
			return err
		}

		return chromedp.Navigate("about:blank").Do(ctx)
	})
}

func setCookies(cookies []dto.BrowserCookie) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		expr := cdp.TimeSinceEpoch(time.Now().Add(180 * 24 * time.Hour))
		_, err := maps.MapErr(cookies, func(cookie dto.BrowserCookie, _ int) (any, error) {
			err := network.SetCookie(cookie.Name, cookie.Value).
				WithExpires(&expr).
				WithDomain(cookie.Domain).
				WithPath(cookie.Path).
				WithHTTPOnly(cookie.HTTPOnly).
				WithSecure(cookie.Secure).
				Do(ctx)
			if err != nil {
				return nil, err
			}
			return nil, nil
		})
		return err
	})
}

func outCookies(outCookie *[]dto.BrowserCookie) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := network.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		converted := lo.Map(cookies, func(cookie *network.Cookie, _ int) dto.BrowserCookie {
			return dto.BrowserCookie{
				Name:     cookie.Name,
				Value:    cookie.Value,
				Domain:   cookie.Domain,
				Path:     cookie.Path,
				HTTPOnly: cookie.HTTPOnly,
				Secure:   cookie.Secure,
			}
		})
		*outCookie = converted
		return nil
	})
}

func getElementCoords(ctx context.Context, selector string) (*dto.ElementPosition, error) {
	var res dto.ElementPosition
	err := chromedp.QueryAfter(selector, func(ctx context.Context, execCtx runtime.ExecutionContextID, nodes ...*cdp.Node) error {
		if len(nodes) < 1 {
			return errors.Errorf("no nodes found by selector")
		}
		node := nodes[0]
		boxes, err := dom.GetContentQuads().WithNodeID(node.NodeID).Do(ctx)
		if err != nil {
			return err
		}

		if len(boxes) == 0 {
			return chromedp.ErrInvalidDimensions
		}

		content := boxes[0]

		c := len(content)
		if c%2 != 0 || c < 1 {
			return chromedp.ErrInvalidDimensions
		}

		if c < 8 {
			return chromedp.ErrInvalidDimensions
		}
		res.TopLeft = dto.XYCoords{
			X: content[0],
			Y: content[1],
		}
		res.TopRight = dto.XYCoords{
			X: content[2],
			Y: content[3],
		}
		res.BottomRight = dto.XYCoords{
			X: content[4],
			Y: content[5],
		}
		res.BottomLeft = dto.XYCoords{
			X: content[6],
			Y: content[7],
		}

		return nil
	}, chromedp.NodeVisible, chromedp.ByQuery).Do(ctx)
	return &res, err
}

func withTimeoutOfMs(millis int) chromedp.EvaluateOption {
	return func(params *runtime.EvaluateParams) *runtime.EvaluateParams {
		params.Timeout = runtime.TimeDelta(millis)
		return params
	}
}

func (b *browser) waitForCondition(ctx context.Context, cond func(ctx context.Context) error) error {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return errors.Wrapf(ctx.Err(), "operation timeout has occurred")
		case <-ticker.C:
			if err := cond(ctx); err != nil && !errors.Is(err, context.Canceled) {
				continue
			}
			return nil
		}
	}
}

func (b *browser) chromeLogFunction(ctx context.Context, pCtx *browserProgramCtx, prefix string) func(msg string, i ...any) {
	return func(msg string, i ...any) {
		args := []any{prefix, msg}
		args = append(args, lo.Map(i, func(val any, _ int) any {
			if s, ok := val.(string); ok {
				return s
			}
			return val
		})...)
		fullArgs := strings.Join(lo.Map(args, func(item any, _ int) string {
			return fmt.Sprintf("%q", item)
		}), " ")

		if !b.chromeDebug && strings.Contains(fullArgs, "net::ERR_ABORTED") {
			ctx = b.log.WithValue(ctx, "chromeLog", fullArgs)
			b.log.Infof(ctx, "[chrome][%s]", prefix)
			pCtx.addLogEntry(fullArgs)
		}
		if prefix == "debug" && !b.chromeDebug {
			return
		}
		if len(i) == 1 {
			var object map[string]any
			s, ok := i[0].([]byte)
			if ok {
				err := json.Unmarshal(s, &object)
				if err == nil {
					ctx = b.log.WithValue(ctx, "chromeLog", object)
					b.log.Infof(ctx, "[chrome][%s]", prefix)
					pCtx.addLogEntry(string(s))
					return
				}
			}
		}
		msg = fmt.Sprintf("[chrome][%s]: %s", args...)
		b.log.Infof(ctx, msg)
		pCtx.addLogEntry(msg)
	}
}

func evaluateWithTimeout(script string, res any, timeoutMs int) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return chromedp.EvaluateAsDevTools(script, res, withTimeoutOfMs(timeoutMs)).Do(ctx)
	})
}

func (b *browser) newLlmClient(bOpts dto.BrowserOpts) (llm.Client, error) {
	llmClientType, err := fromPtrOrEnv(bOpts.LlmClient, "LLM_CLIENT")
	if err != nil {
		return nil, err
	}
	if llmClientType == "" {
		llmClientType = "ollama"
	}

	switch llmClientType {
	case "ollama":
		// if custom Ollama URL is set, don't use Ollama credentials from env variables to prevent leakage
		fromPtrOrEnvOllama := guardedFromPtrOrEnv(lo.FromPtr(bOpts.OllamaUrl) != "")

		ollamaUrl, err := fromPtrOrEnv(bOpts.OllamaUrl, "OLLAMA_URL")
		if err != nil {
			return nil, err
		}

		ollamaApiKey, err := fromPtrOrEnvOllama(bOpts.OllamaApiKey, "OLLAMA_API_KEY")
		if err != nil {
			return nil, err
		}
		return llm.NewOllama(b.log, ollamaUrl, ollamaApiKey), nil
	case "openai":
		openaiToken, err := fromPtrOrEnv(bOpts.OpenaiToken, "OPENAI_TOKEN")
		if err != nil {
			return nil, err
		}
		openaiOrg, err := fromPtrOrEnv(bOpts.OpenaiOrganization, "OPENAI_ORGANIZATION")
		if err != nil {
			return nil, err
		}
		openaiModel, err := fromPtrOrEnv(bOpts.OpenaiModel, "OPENAI_MODEL")
		if err != nil {
			return nil, err
		}
		llmClient, err := llm.NewOpenAI(b.log, openaiToken, openaiOrg, openaiModel)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to init openai client")
		}
		return llmClient, nil
	case "azure-openai":
		// if custom Azure endpoint is set, don't use Azure credentials from env variables to prevent leakage
		fromPtrOrEnvAzure := guardedFromPtrOrEnv(lo.FromPtr(bOpts.AzureOpenaiEndpoint) != "")

		azureEndpoint, err := fromPtrOrEnv(bOpts.AzureOpenaiEndpoint, "AZURE_OPENAI_ENDPOINT")
		if err != nil {
			return nil, err
		}

		azureApiKey, err := fromPtrOrEnvAzure(bOpts.AzureOpenaiKey, "AZURE_OPENAI_API_KEY")
		if err != nil {
			return nil, err
		}
		azureModel, err := fromPtrOrEnvAzure(bOpts.AzureOpenaiDeploymentName, "AZURE_OPENAI_DEPLOYMENT_NAME")
		if err != nil {
			return nil, err
		}
		azureApiVersion, err := fromPtrOrEnvAzure(bOpts.AzureOpenaiApiVersion, "AZURE_OPENAI_API_VERSION")
		if err != nil {
			return nil, err
		}
		llmClient, err := llm.NewAzureOpenAI(b.log, azureEndpoint, azureApiKey, azureModel, azureApiVersion)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to init azure openai client")
		}
		return llmClient, nil
	default:
		return nil, errors.Errorf("unsupported llm client: %q", llmClientType)
	}
}

func fromPtrOrEnv(ptr *string, envVar string) (string, error) {
	if lo.FromPtr(ptr) != "" {
		return *ptr, nil
	}
	value, err := awsutil.GetEnvOrSecret(envVar)
	if err != nil {
		return "", errors.Errorf("failed to get %s env", envVar)
	}
	return value, nil
}

func guardedFromPtrOrEnv(condition bool) func(ptr *string, envVar string) (string, error) {
	return func(ptr *string, envVar string) (string, error) {
		if condition {
			return lo.FromPtr(ptr), nil
		}
		return fromPtrOrEnv(ptr, envVar)
	}
}
