package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

func downloadURLToTempFile(ctx context.Context, log logger.Logger, fileURL, destDir, prefix string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create request to download file")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.Wrapf(err, "failed to download file from url %q", fileURL)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Warnf(log.WithValue(ctx, "error", cerr.Error()), "failed to close response body while downloading file")
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", errors.Errorf("unexpected status %d while downloading file from %q", resp.StatusCode, fileURL)
	}

	parsedURL, err := url.Parse(fileURL)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse url %q", fileURL)
	}

	fileName := path.Base(parsedURL.Path)
	if fileName == "" || fileName == "." || fileName == "/" {
		fileName = fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}

	ext := filepath.Ext(fileName)
	pattern := prefix + "*"
	if ext != "" {
		pattern = fmt.Sprintf("%s*%s", prefix, ext)
	}

	tempFile, err := os.CreateTemp(destDir, pattern)
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp file for download")
	}
	defer func() {
		if cerr := tempFile.Close(); cerr != nil {
			log.Warnf(log.WithValue(ctx, "error", cerr.Error()), "failed to close temp file for downloaded file")
		}
	}()

	if _, err = io.Copy(tempFile, resp.Body); err != nil {
		return "", errors.Wrap(err, "failed to write downloaded file to disk")
	}

	return tempFile.Name(), nil
}
