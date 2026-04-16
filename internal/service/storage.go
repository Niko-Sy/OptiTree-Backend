package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"optitree-backend/internal/util"
)

var (
	ErrFileTypeForbidden = errors.New("文件类型不支持")
	ErrFileTooLarge      = errors.New("文件过大")
	ErrStorageNotFound   = errors.New("存储文件不存在")
)

type StorageService struct {
	localPath     string
	baseURL       string
	maxFileSize   int64
	allowedImages map[string]bool
	allowedDocs   map[string]bool
	remoteClient  *http.Client
}

func NewStorageService(localPath, baseURL string, maxFileSize int64, allowedImages, allowedDocs []string) *StorageService {
	imgMap := make(map[string]bool)
	for _, t := range allowedImages {
		imgMap[t] = true
	}
	docMap := make(map[string]bool)
	for _, t := range allowedDocs {
		docMap[t] = true
	}
	return &StorageService{
		localPath:     localPath,
		baseURL:       baseURL,
		maxFileSize:   maxFileSize,
		allowedImages: imgMap,
		allowedDocs:   docMap,
		remoteClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *StorageService) SaveImage(file multipart.File, header *multipart.FileHeader) (string, error) {
	if !s.allowedImages[header.Header.Get("Content-Type")] {
		return "", ErrFileTypeForbidden
	}
	if header.Size > s.maxFileSize {
		return "", ErrFileTooLarge
	}
	return s.save(file, header, "avatars")
}

func (s *StorageService) SaveDocument(file multipart.File, header *multipart.FileHeader, mimeType string) (string, error) {
	if !s.allowedDocs[mimeType] {
		return "", ErrFileTypeForbidden
	}
	if header.Size > s.maxFileSize {
		return "", ErrFileTooLarge
	}
	return s.save(file, header, "documents")
}

func (s *StorageService) SaveGeneratedDocument(fileName, mimeType string, data []byte) (string, error) {
	if !s.allowedDocs[mimeType] {
		return "", ErrFileTypeForbidden
	}
	if int64(len(data)) > s.maxFileSize {
		return "", ErrFileTooLarge
	}
	return s.saveBytes(fileName, data, "documents")
}

// LocalPath converts a storage URL back to its absolute file system path.
// Returns an empty string if the URL does not match this storage's base URL.
func (s *StorageService) LocalPath(url string) string {
	prefix := s.baseURL + "/"
	if !strings.HasPrefix(url, prefix) {
		return ""
	}
	rel := strings.TrimPrefix(url, prefix)
	return filepath.Join(s.localPath, filepath.FromSlash(rel))
}

func (s *StorageService) save(file multipart.File, header *multipart.FileHeader, subDir string) (string, error) {
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	dateStr := time.Now().Format("2006/01/02")
	relDir := filepath.Join(subDir, dateStr)
	absDir := filepath.Join(s.localPath, relDir)

	if err := os.MkdirAll(absDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	token, err := util.RandomToken(8)
	if err != nil {
		return "", err
	}
	filename := token + ext
	absPath := filepath.Join(absDir, filename)

	dst, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	relPath := filepath.ToSlash(filepath.Join(relDir, filename))
	return s.baseURL + "/" + relPath, nil
}

func (s *StorageService) saveBytes(fileName string, data []byte, subDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		ext = ".bin"
	}
	dateStr := time.Now().Format("2006/01/02")
	relDir := filepath.Join(subDir, dateStr)
	absDir := filepath.Join(s.localPath, relDir)

	if err := os.MkdirAll(absDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	token, err := util.RandomToken(8)
	if err != nil {
		return "", err
	}
	filename := token + ext
	absPath := filepath.Join(absDir, filename)

	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	relPath := filepath.ToSlash(filepath.Join(relDir, filename))
	return s.baseURL + "/" + relPath, nil
}

func (s *StorageService) DeleteFile(ctx context.Context, urlPath string) error {
	// 从 URL 路径提取相对路径
	relPath := strings.TrimPrefix(urlPath, s.baseURL+"/")
	absPath := filepath.Join(s.localPath, relPath)
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *StorageService) GetAllowedDocMIMETypes() []string {
	types := make([]string, 0, len(s.allowedDocs))
	for t := range s.allowedDocs {
		types = append(types, t)
	}
	return types
}

// OpenDocument opens a document by source URL and returns stream, size and content type.
func (s *StorageService) OpenDocument(ctx context.Context, sourceURL, fallbackContentType string) (io.ReadCloser, int64, string, error) {
	trimmedURL := strings.TrimSpace(sourceURL)
	if trimmedURL == "" {
		return nil, 0, "", ErrStorageNotFound
	}

	if localPath := s.LocalPath(trimmedURL); localPath != "" {
		f, err := os.Open(localPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, 0, "", ErrStorageNotFound
			}
			return nil, 0, "", err
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, 0, "", err
		}
		contentType := detectContentType(localPath, fallbackContentType)
		return f, st.Size(), contentType, nil
	}

	parsed, err := url.Parse(trimmedURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, 0, "", ErrStorageNotFound
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmedURL, nil)
	if err != nil {
		return nil, 0, "", err
	}
	resp, err := s.remoteClient.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, 0, "", ErrStorageNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, 0, "", fmt.Errorf("远端文件读取失败，status=%d", resp.StatusCode)
	}

	contentType, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if parseErr != nil || strings.TrimSpace(contentType) == "" {
		contentType = fallbackContentType
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}

	if resp.ContentLength > 0 && s.maxFileSize > 0 && resp.ContentLength > s.maxFileSize {
		_ = resp.Body.Close()
		return nil, 0, "", ErrFileTooLarge
	}

	return resp.Body, resp.ContentLength, contentType, nil
}

func detectContentType(path string, fallbackContentType string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		if contentType := mime.TypeByExtension(ext); contentType != "" {
			mainType, _, err := mime.ParseMediaType(contentType)
			if err == nil && strings.TrimSpace(mainType) != "" {
				return mainType
			}
		}
	}
	if strings.TrimSpace(fallbackContentType) != "" {
		return fallbackContentType
	}
	return "application/octet-stream"
}
