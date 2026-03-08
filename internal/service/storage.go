package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"optitree-backend/internal/util"
)

var (
	ErrFileTypeForbidden = errors.New("文件类型不支持")
	ErrFileTooLarge      = errors.New("文件过大")
)

type StorageService struct {
	localPath     string
	baseURL       string
	maxFileSize   int64
	allowedImages map[string]bool
	allowedDocs   map[string]bool
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
