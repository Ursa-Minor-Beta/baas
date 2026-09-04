package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const (
	DefaultUserAgentForReadability = "*"
)

func (s *Server) readabilityWithChrome(ctx context.Context, cfg *dto.ReadabilityConfig) (*dto.ReadabilityResult, error) {
	s.Logger().Infof(ctx, "Trying to fetch with Chrome...")
	opts := dto.BrowserOpts{}
	if cfg.UserAgent != nil {
		opts.UserAgent = lo.FromPtr(cfg.UserAgent)
	}
	if cfg.UseProxy != nil {
		opts.UseProxy = s.proxy.Host()
	}
	opts.EnableExtensions = []dto.ChromeExtensionMode{dto.EnableExtensionsReadability, dto.EnableExtensionsBaas}
	fetchCode := fmt.Sprintf(`
var resp = navigateResponse('%s'); 
waitReady('body');
`, cfg.URL)
	if strings.Contains(cfg.URL, "linkedin.com") {
		fetchRetries := 6
		fetchCode = fmt.Sprintf(`
var resp;
for (var i = 1; i < %d +1; i++) {
	resp = navigateResponse('%s'); 
	if (resp.Status == 200) {
		break;
	}
	waitReady('body');
	if (i >= %d) {
		throw 'readabilty page returned non-200 status code: ' + resp.Status;
	};
	sleep('300ms');
}
`, fetchRetries, cfg.URL, fetchRetries)
	}
	opts.Program = fmt.Sprintf(`
%s
if (resp === undefined) {
   reload();
   waitReady('body');
}
if (!resp.MimeTypeReadability) {
    throw 'readability does not support mime type: ' + resp.MimeType;
}
waitReady('body'); 
reload();
sleep('5s');
waitReadabilityContent('timeout:10s');

`, fetchCode)
	res, err := s.browser.Run(ctx, opts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch url %q with chrome", cfg.URL)
	}
	if len(res.Log) > 0 {
		s.Logger().Warnf(ctx, "Readability issue when fetching url %q with chrome: %v", cfg.URL, res.Log)
	}
	if res.Error != nil {
		return nil, errors.Errorf("failed to fetch url %q with chrome: error occurred: %v", cfg.URL, *res.Error)
	}
	if res.ReadabilityArticle == nil {
		return nil, errors.Errorf("failed to read url %q with readability in chrome: no content returned", cfg.URL)
	}
	return s.articleToResult(ctx, cfg, []byte(res.ReadabilityArticle.Content), *res.ReadabilityArticle, true), nil
}

// FetchFromURL fetch the web page from specified url then parses the response to find
// the readable content.
func FetchFromURL(ctx context.Context, cfg *dto.ReadabilityConfig) (*http.Response, error) {
	log := logger.NewLogger()
	pageURL, timeoutString, userAgent := cfg.URL, cfg.Timeout, cfg.UserAgent
	if timeoutString == "" {
		timeoutString = DefaultTimeout
	}
	timeout, err := time.ParseDuration(timeoutString)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse timeout string %q", timeoutString)
	}

	if cfg.Timeout == "" {
		cfg.Timeout = timeoutString
	}

	// Make sure URL is valid
	_, err = url.ParseRequestURI(pageURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Fetch page from URL
	client := &http.Client{
		Timeout: timeout,
	}

	transport := &http.Transport{
		TLSHandshakeTimeout:   time.Second * 10,
		IdleConnTimeout:       time.Second * 10,
		ResponseHeaderTimeout: time.Second * 10,
		ExpectContinueTimeout: time.Second * 10,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	if cfg.UseProxy != nil {
		proxyURLString := fmt.Sprintf("http://%s", lo.FromPtr(cfg.UseProxy))
		proxyURL, err := url.Parse(proxyURLString)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse proxy URL %q", proxyURLString)
		}
		log.Infof(ctx, "using proxy server %q to fetch %q...", proxyURLString, cfg.URL)

		transport.Proxy = http.ProxyURL(proxyURL)
	}
	client.Transport = transport

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init request for page: %w", err)
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", lo.If(userAgent == nil, DefaultUserAgentForReadability).Else(lo.FromPtr(userAgent)))
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the page: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("failed to fetch the page: status code %d", resp.StatusCode)
	}

	return resp, nil
}

// ReadFromURL fetch the web page from specified url then parses the response to find
// the readable content.
func ReadFromURL(ctx context.Context, cfg *dto.ReadabilityConfig) ([]byte, *readability.Article, error) {
	resp, err := FetchFromURL(ctx, cfg)
	if resp != nil {
		defer func(Body io.ReadCloser) {
			_ = Body.Close()
		}(resp.Body)
	}
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to fetch URL %q", cfg.URL)
	}

	// Make sure content type is HTML
	cp := resp.Header.Get("Content-Type")

	if !lo.ContainsBy(supportedTextMimeTypes, func(mime string) bool {
		return strings.Contains(cp, mime)
	}) && !lo.FromPtr(cfg.IgnoreMimeType) {
		return nil, nil, fmt.Errorf("URL is not a HTML/text document")
	}

	fullResponseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to read response body")
	}

	bufReader := bytes.NewReader(fullResponseBody)

	article, err := ParseReadabilityContent(bufReader, cfg.URL)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to parse content with readabilty")
	}
	return fullResponseBody, article, nil
}

func ParseReadabilityContent(reader io.Reader, pageURL string) (*readability.Article, error) {
	// Make sure URL is valid
	parsedURL, err := url.ParseRequestURI(pageURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Parse content
	parser := readability.NewParser()
	parsed, err := parser.Parse(reader, parsedURL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse readability content from %q", pageURL)
	}
	return &parsed, err
}
