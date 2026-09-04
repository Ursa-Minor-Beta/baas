package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

type Uploader interface {
	Upload(ctx context.Context, data []byte, mimeType string) (*UploadResult, error)
}

type uploader struct {
	storageServiceURL    string
	storageServiceAPIKey string
	log                  logger.Logger
}

type PrepareResponse struct {
	UploadURL string `json:"uploadURL"`
	SessionID string `json:"sessionID"`
	FileName  string `json:"fileName"`
	Secret    string `json:"secret"`
	Link      string `json:"link"`
}

type UploadResult struct {
	SessionID string `json:"sessionID"`
	Link      string `json:"link"`
}

func NewUploader(storageServiceURL, storageServiceAPIKey string, log logger.Logger) Uploader {
	return &uploader{
		storageServiceURL:    storageServiceURL,
		storageServiceAPIKey: storageServiceAPIKey,
		log:                  log,
	}
}

func (u *uploader) Upload(ctx context.Context, data []byte, mimeType string) (*UploadResult, error) {
	ext, err := getExtensionFromMimeType(mimeType)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to determine file extension")
	}

	// Generate a unique file name
	fileName := fmt.Sprintf("%s%s", uuid.New().String(), ext)

	// Prepare query parameters
	queryParams := url.Values{}
	queryParams.Set("directory", "default") // You might want to make this configurable
	queryParams.Set("ttl_minutes", "1440")  // 24 hours, adjust as needed
	queryParams.Set("file", fileName)

	// Prepare the URL for the storage service
	prepareURL := fmt.Sprintf("%s/api/prepare?%s", u.storageServiceURL, queryParams.Encode())

	// Create the request to prepare the upload
	req, err := http.NewRequestWithContext(ctx, "POST", prepareURL, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create request")
	}

	// Set the authorization header
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", u.storageServiceAPIKey))

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to send request")
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			u.log.Errorf(u.log.WithValue(ctx, "error", err.Error()), "failed to close buffer")
		}
	}(resp.Body)

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("storage service returned non-OK status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var prepareResponse PrepareResponse
	if err := json.Unmarshal(body, &prepareResponse); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal response")
	}

	// Upload the file
	uploadReq, err := http.NewRequestWithContext(ctx, "PUT", prepareResponse.UploadURL, bytes.NewReader(data))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create upload request")
	}

	// Set the Content-Type header
	uploadReq.Header.Set("Content-Type", mimeType)

	uploadResp, err := client.Do(uploadReq)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to upload file")
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			u.log.Errorf(u.log.WithValue(ctx, "error", err.Error()), "failed to close buffer")
		}
	}(uploadResp.Body)

	if uploadResp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("file upload failed with status: %d", uploadResp.StatusCode)
	}

	u.log.Infof(ctx, "File uploaded successfully: %s", fileName)
	return &UploadResult{
		SessionID: prepareResponse.SessionID,
		Link:      prepareResponse.Link,
	}, nil
}

func getExtensionFromMimeType(mimeType string) (string, error) {
	extensions, err := mime.ExtensionsByType(mimeType)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get extensions for MIME type %s", mimeType)
	}
	if len(extensions) == 0 {
		return "", errors.Errorf("no known extension for MIME type %s", mimeType)
	}
	return extensions[0], nil
}
