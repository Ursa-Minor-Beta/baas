# Parse to Markdown KV - Usage Examples

This document provides comprehensive usage examples for the `/api/parse-to-markdown-kv` endpoint.

## Table of Contents

- [Basic Usage](#basic-usage)
- [Excel Files](#excel-files)
- [PowerPoint Files](#powerpoint-files)
- [Word Documents](#word-documents)
- [CSV Files](#csv-files)
- [Plain Text Files](#plain-text-files)
- [Advanced Options](#advanced-options)
- [Error Handling](#error-handling)
- [Code Examples](#code-examples)

---

## Basic Usage

### Endpoint

```
POST /api/parse-to-markdown-kv
Content-Type: application/json
Authorization: Bearer <your-api-token>
```

### Minimal Request

```json
{
  "URL": "https://example.com/document.xlsx",
  "timeout": "30s"
}
```

### Minimal Response

```json
{
  "markdown": "| Column1 | Column2 |\n| --- | --- |\n| Value1 | Value2 |\n",
  "usedBrowser": false,
  "usedProxy": "",
  "meta": {
    "version": "1.0.0",
    "executionTime": "2.3s"
  }
}
```

---

## Excel Files

### Simple Excel Conversion

**Request:**
```json
{
  "URL": "https://example.com/sales-data.xlsx",
  "timeout": "30s"
}
```

**Expected Markdown Output:**
```markdown
| Product | Q1 | Q2 | Q3 | Q4 |
| --- | --- | --- | --- | --- |
| Widget | 100 | 120 | 110 | 130 |
| Gadget | 80 | 90 | 95 | 100 |
```

### Excel with Multiple Sheets

The new aipandoc XLSX reader parses all sheets. Each sheet will be included in the markdown output.

**Request:**
```json
{
  "URL": "https://example.com/annual-report.xlsx",
  "timeout": "60s",
  "maxAttempts": 3
}
```

**Expected Output:**
All sheets will be converted and included in a single markdown string.

---

## PowerPoint Files

### PPTX with SmartArt

The updated aipandoc supports SmartArt diagrams.

**Request:**
```json
{
  "URL": "https://example.com/presentation.pptx",
  "timeout": "45s"
}
```

**Expected Markdown Output:**
```markdown
# Slide 1 Title

Slide content with bullet points

# Slide 2 Title

More content with tables and text
```

---

## Word Documents

### DOCX Conversion

**Request:**
```json
{
  "URL": "https://example.com/report.docx",
  "timeout": "30s"
}
```

**Expected Markdown Output:**
```markdown
# Document Title

Paragraph content with **bold** and *italic* text.

## Section Heading

- Bullet point 1
- Bullet point 2

| Table | Header |
| --- | --- |
| Cell 1 | Cell 2 |
```

---

## CSV Files

### Basic CSV Table

**Request:**
```json
{
  "URL": "https://example.com/data.csv",
  "timeout": "10s"
}
```

**Sample CSV Input:**
```csv
Name,Email,Role
John Doe,john@example.com,Developer
Jane Smith,jane@example.com,Designer
```

**Markdown Output:**
```markdown
| Name | Email | Role |
| --- | --- | --- |
| John Doe | john@example.com | Developer |
| Jane Smith | jane@example.com | Designer |
```

### CSV with Special Characters

Pipes and newlines in cells are automatically escaped.

**Request:**
```json
{
  "URL": "https://example.com/data-with-pipes.csv",
  "timeout": "10s"
}
```

**Sample CSV Input:**
```csv
Name,Description
Product A,"Contains | pipes"
Product B,"Multi
line
description"
```

**Markdown Output:**
```markdown
| Name | Description |
| --- | --- |
| Product A | Contains \| pipes |
| Product B | Multi line description |
```

---

## Plain Text Files

Plain text files with .txt extension are returned as-is.

**Request:**
```json
{
  "URL": "https://example.com/notes.txt",
  "timeout": "10s"
}
```

---

## Advanced Options

### Using Proxy

**Request:**
```json
{
  "URL": "https://protected-site.com/document.xlsx",
  "timeout": "30s",
  "useProxy": "proxy.example.com:8080"
}
```

### Using Random Proxy from Pool

**Request:**
```json
{
  "URL": "https://example.com/document.xlsx",
  "timeout": "30s",
  "useRandomProxy": true
}
```

### Browser Fallback

If regular HTTP fetch fails, fallback to browser automation.

**Request:**
```json
{
  "URL": "https://dynamic-site.com/document.xlsx",
  "timeout": "60s",
  "fallbackToBrowser": true
}
```

### Force Browser

Always use browser automation (useful for JavaScript-rendered content).

**Request:**
```json
{
  "URL": "https://spa-app.com/document.xlsx",
  "timeout": "60s",
  "forceUseBrowser": true
}
```

### Retry with Multiple Attempts

**Request:**
```json
{
  "URL": "https://unreliable-server.com/document.xlsx",
  "timeout": "30s",
  "maxAttempts": 5
}
```

### Custom Headers

**Request:**
```json
{
  "URL": "https://api.example.com/document.xlsx",
  "timeout": "30s",
  "headers": {
    "X-API-Key": "your-api-key",
    "X-Custom-Header": "value"
  }
}
```

### Disable Cache

Force fresh fetch without using cache.

**Request:**
```json
{
  "URL": "https://example.com/document.xlsx",
  "timeout": "30s",
  "doNotUseCache": true
}
```

### Custom User-Agent

**Request:**
```json
{
  "URL": "https://example.com/document.xlsx",
  "timeout": "30s",
  "userAgent": "Custom-Bot/1.0"
}
```

---

## Error Handling

### Invalid URL

**Request:**
```json
{
  "URL": "https://invalid-domain-12345.com/file.xlsx",
  "timeout": "10s"
}
```

**Response (HTTP 500):**
```json
{
  "error": "failed to determine document format: failed to fetch URL"
}
```

### Unsupported Format

**Request:**
```json
{
  "URL": "https://example.com/video.mp4",
  "timeout": "10s"
}
```

**Response (HTTP 500):**
```json
{
  "error": "unknown document format for content-type: \"video/mp4\""
}
```

### Timeout

**Request:**
```json
{
  "URL": "https://slow-server.com/large-file.xlsx",
  "timeout": "5s"
}
```

**Response (HTTP 500):**
```json
{
  "error": "context deadline exceeded"
}
```

---

## Code Examples

### cURL

#### Basic Request
```bash
curl -X POST https://api.example.com/api/parse-to-markdown-kv \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "URL": "https://example.com/data.xlsx",
    "timeout": "30s"
  }'
```

#### With All Options
```bash
curl -X POST https://api.example.com/api/parse-to-markdown-kv \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "URL": "https://example.com/document.xlsx",
    "timeout": "60s",
    "maxAttempts": 3,
    "fallbackToBrowser": true,
    "useRandomProxy": false,
    "headers": {
      "X-Custom": "value"
    }
  }'
```

### JavaScript (fetch)

```javascript
async function parseToMarkdown(url) {
  const response = await fetch('https://api.example.com/api/parse-to-markdown-kv', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer YOUR_TOKEN'
    },
    body: JSON.stringify({
      URL: url,
      timeout: '30s',
      maxAttempts: 3
    })
  });

  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`);
  }

  const data = await response.json();
  return data.markdown;
}

// Usage
parseToMarkdown('https://example.com/sales-data.xlsx')
  .then(markdown => console.log(markdown))
  .catch(error => console.error('Error:', error));
```

### Python (requests)

```python
import requests

def parse_to_markdown(url, timeout='30s', max_attempts=3):
    api_url = 'https://api.example.com/api/parse-to-markdown-kv'
    headers = {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer YOUR_TOKEN'
    }
    payload = {
        'URL': url,
        'timeout': timeout,
        'maxAttempts': max_attempts
    }

    response = requests.post(api_url, json=payload, headers=headers)
    response.raise_for_status()

    return response.json()['markdown']

# Usage
try:
    markdown = parse_to_markdown('https://example.com/data.xlsx')
    print(markdown)
except requests.exceptions.HTTPError as e:
    print(f'HTTP error occurred: {e}')
except Exception as e:
    print(f'Error occurred: {e}')
```

### Go

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ReadabilityConfig struct {
	URL         string `json:"URL"`
	Timeout     string `json:"timeout"`
	MaxAttempts *int   `json:"maxAttempts,omitempty"`
}

type ParseToMarkdownResult struct {
	Markdown    string `json:"markdown"`
	UsedBrowser bool   `json:"usedBrowser"`
	UsedProxy   string `json:"usedProxy,omitempty"`
}

func parseToMarkdown(url, token string) (string, error) {
	apiURL := "https://api.example.com/api/parse-to-markdown-kv"

	maxAttempts := 3
	config := ReadabilityConfig{
		URL:         url,
		Timeout:     "30s",
		MaxAttempts: &maxAttempts,
	}

	jsonData, err := json.Marshal(config)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result ParseToMarkdownResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Markdown, nil
}

func main() {
	markdown, err := parseToMarkdown(
		"https://example.com/data.xlsx",
		"YOUR_TOKEN",
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(markdown)
}
```

---

## Best Practices

### 1. Set Appropriate Timeouts

- **Small files (< 1MB)**: `10-30s`
- **Medium files (1-10MB)**: `30-60s`
- **Large files (> 10MB)**: `60-120s`

### 2. Use Retry Mechanism for Unreliable Sources

```json
{
  "URL": "https://unreliable-server.com/file.xlsx",
  "timeout": "30s",
  "maxAttempts": 5
}
```

### 3. Enable Browser Fallback for Dynamic Sites

```json
{
  "URL": "https://dynamic-site.com/file.xlsx",
  "timeout": "60s",
  "fallbackToBrowser": true
}
```

### 4. Cache for Repeated Requests

By default, caching is enabled. To force fresh fetch:

```json
{
  "URL": "https://example.com/file.xlsx",
  "timeout": "30s",
  "doNotUseCache": true
}
```

---

## Supported Formats

| Format | Extension | Content-Type | Notes |
|--------|-----------|--------------|-------|
| Excel | `.xlsx` | `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` | All sheets parsed |
| PowerPoint | `.pptx` | `application/vnd.openxmlformats-officedocument.presentationml.presentation` | SmartArt supported |
| Word | `.docx` | `application/vnd.openxmlformats-officedocument.wordprocessingml.document` | Full formatting |
| CSV | `.csv` | `text/csv` | Generates Markdown tables |
| Plain Text | `.txt` | `text/plain` | Returned as-is |

---

## Rate Limiting

Please respect rate limits:
- **Standard tier**: 100 requests/minute
- **Premium tier**: 1000 requests/minute

---

## Support

For issues or questions:
- GitHub Issues: https://github.com/Ursa-Minor-Beta/baas/issues
- Documentation: https://docs.example.com/parse-to-markdown-kv
