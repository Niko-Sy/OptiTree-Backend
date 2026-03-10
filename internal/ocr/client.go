// Package ocr wraps the PaddleOCR layout-parsing API.
// It mirrors the logic in the reference Python script (ocr_to_markdown.py),
// translating it into idiomatic Go so it can be called directly from the backend.
package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// FileTypePDF is the fileType constant for PDF documents (matches PaddleOCR API).
const FileTypePDF = 0

// FileTypeImage is the fileType constant for image files (matches PaddleOCR API).
const FileTypeImage = 1

// Client calls the PaddleOCR layout-parsing API to extract structured Markdown from documents.
type Client struct {
	url        string
	token      string
	httpClient *http.Client
}

// NewClient creates a new OCR client.
// url is the full API endpoint (e.g. https://…/layout-parsing).
// token is the Authorization token.
// timeout sets the HTTP round-trip timeout; use 0 for the default of 300s.
func NewClient(url, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &Client{
		url:   url,
		token: token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// ─── API wire types ───────────────────────────────────────────────────────────

type ocrRequest struct {
	File                      string `json:"file"`
	FileType                  int    `json:"fileType"`
	UseDocOrientationClassify bool   `json:"useDocOrientationClassify"`
	UseDocUnwarping           bool   `json:"useDocUnwarping"`
	UseChartRecognition       bool   `json:"useChartRecognition"`
}

type ocrResponse struct {
	Result *ocrResult `json:"result"`
}

type ocrResult struct {
	LayoutParsingResults []ocrPageResult `json:"layoutParsingResults"`
}

type ocrPageResult struct {
	Markdown struct {
		Text   string            `json:"text"`
		Images map[string]string `json:"images"`
	} `json:"markdown"`
}

// ─── Public API ───────────────────────────────────────────────────────────────

// ParseToMarkdown reads the file at filePath, sends it to the PaddleOCR API,
// and returns all pages merged into a single Markdown string (pages separated by
// "\n\n---\n\n", matching the Python reference implementation).
//
// fileType must be FileTypePDF (0) or FileTypeImage (1).
func (c *Client) ParseToMarkdown(ctx context.Context, filePath string, fileType int) (string, error) {
	data, err := encodeFile(filePath)
	if err != nil {
		return "", fmt.Errorf("ocr: encode file: %w", err)
	}

	payload := ocrRequest{
		File:                      data,
		FileType:                  fileType,
		UseDocOrientationClassify: false,
		UseDocUnwarping:           false,
		UseChartRecognition:       false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("ocr: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ocr: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ocr: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ocr: API returned HTTP %d: %s", resp.StatusCode, string(preview))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ocr: read response body: %w", err)
	}

	var ocrResp ocrResponse
	if err := json.Unmarshal(raw, &ocrResp); err != nil {
		return "", fmt.Errorf("ocr: parse response JSON: %w", err)
	}
	if ocrResp.Result == nil {
		return "", fmt.Errorf("ocr: response missing 'result' field")
	}

	return mergeMarkdown(ocrResp.Result.LayoutParsingResults), nil
}

// IsBinaryDoc reports whether the given fileType extension requires OCR
// (i.e. is a binary format rather than plain text).
// txt files do NOT require OCR; PDF, docx, xlsx, doc do.
func IsBinaryDoc(fileType string) bool {
	switch strings.ToLower(fileType) {
	case "pdf", "docx", "doc", "xlsx", "xls":
		return true
	}
	return false
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// encodeFile reads the file at path and returns its Base64-encoded content (ASCII).
func encodeFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// mergeMarkdown concatenates page-level Markdown text, separated by horizontal rules.
func mergeMarkdown(pages []ocrPageResult) string {
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		text := strings.TrimSpace(p.Markdown.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}
