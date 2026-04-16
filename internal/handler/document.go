package handler

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type DocumentHandler struct {
	docService *service.DocumentService
}

func NewDocumentHandler(docService *service.DocumentService) *DocumentHandler {
	return &DocumentHandler{docService: docService}
}

func (h *DocumentHandler) Upload(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		util.Fail(c, constant.CodeInvalidParam, "请上传文件")
		return
	}

	// Support both files and files[] for frontend compatibility.
	files := append(form.File["files"], form.File["files[]"]...)
	if len(files) == 0 {
		util.Fail(c, constant.CodeInvalidParam, "请至少上传一个文件")
		return
	}

	quality := c.PostForm("quality")
	if quality == "" {
		quality = "balanced"
	}
	modelName := c.PostForm("model")
	if modelName == "" {
		modelName = "qwen3.5-flash"
	}
	projectType := c.PostForm("projectType")

	projectIDStr := c.PostForm("projectId")
	var projectID *string
	if projectIDStr != "" {
		s := projectIDStr
		projectID = &s
	}

	userID := middleware.GetUserID(c)
	var docIDs []string
	var documents []gin.H

	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			util.FailServerError(c)
			return
		}

		mimeType := fh.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		// 只取 MIME 类型主体，去掉参数（charset 等）
		mimeType, _, _ = mime.ParseMediaType(mimeType)

		doc, err := h.docService.Upload(c.Request.Context(), service.UploadDocumentInput{
			File:       f,
			Header:     fh,
			MimeType:   mimeType,
			UploadedBy: userID,
			ProjectID:  projectID,
		})
		_ = f.Close()
		if err != nil {
			switch err {
			case service.ErrFileTypeForbidden:
				util.Fail(c, constant.CodeFileType, fh.Filename+": "+constant.MsgFileType)
			case service.ErrFileTooLarge:
				util.Fail(c, constant.CodeFileTooLarge, fh.Filename+": "+constant.MsgFileTooLarge)
			default:
				util.FailServerError(c)
			}
			return
		}
		docIDs = append(docIDs, doc.ID)
		documents = append(documents, gin.H{
			"id":            doc.ID,
			"fileName":      doc.FileName,
			"status":        doc.Status,
			"readerKind":    doc.ReaderKind,
			"previewStatus": doc.PreviewStatus,
		})
	}

	summary := fmt.Sprintf(
		"已上传 %d 份文档，quality=%s，model=%s",
		len(documents),
		quality,
		modelName,
	)
	if projectType != "" {
		summary += ", projectType=" + projectType
	}

	util.Success(c, gin.H{
		"docIds":    docIDs,
		"summary":   summary,
		"documents": documents,
	})
}

func (h *DocumentHandler) GetByID(c *gin.Context) {
	docID := c.Param("docId")
	doc, err := h.docService.GetByID(c.Request.Context(), docID)
	if err != nil {
		switch err {
		case service.ErrDocumentNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"document": doc})
}

func (h *DocumentHandler) ListByProject(c *gin.Context) {
	projectID := strings.TrimSpace(c.Param("projectId"))
	if projectID == "" {
		util.Fail(c, constant.CodeInvalidParam, "projectId 不能为空")
		return
	}

	list, err := h.docService.ListByProject(c.Request.Context(), projectID, middleware.GetUserID(c))
	if err != nil {
		switch err {
		case service.ErrDocumentPermissionDenied:
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	util.Success(c, gin.H{"list": list})
}

func (h *DocumentHandler) SearchByProject(c *gin.Context) {
	projectID := strings.TrimSpace(c.Param("projectId"))
	if projectID == "" {
		util.Fail(c, constant.CodeInvalidParam, "projectId 不能为空")
		return
	}

	results, err := h.docService.Search(
		c.Request.Context(),
		projectID,
		c.Query("q"),
		middleware.GetUserID(c),
	)
	if err != nil {
		switch err {
		case service.ErrDocumentPermissionDenied:
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	util.Success(c, gin.H{"list": results})
}

func (h *DocumentHandler) Preview(c *gin.Context) {
	docID := strings.TrimSpace(c.Param("docId"))
	if docID == "" {
		util.Fail(c, constant.CodeInvalidParam, "docId 不能为空")
		return
	}

	binary, err := h.docService.Preview(c.Request.Context(), docID, middleware.GetUserID(c))
	if err != nil {
		switch err {
		case service.ErrDocumentNotFound:
			c.JSON(http.StatusNotFound, gin.H{"code": constant.CodeNotFound, "message": constant.MsgNotFound})
		case service.ErrDocumentPermissionDenied:
			util.FailForbidden(c)
		case service.ErrDocumentPreviewProcessing:
			c.JSON(http.StatusConflict, gin.H{"code": constant.CodeConflict, "message": "文档预览生成中"})
		case service.ErrDocumentPreviewFailed:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"code": constant.CodeAIFailed, "message": "文档预览不可用"})
		default:
			util.FailServerError(c)
		}
		return
	}
	defer binary.Reader.Close()

	filename := binary.FileName
	if strings.TrimSpace(filename) == "" {
		filename = "document"
	}

	contentType := strings.TrimSpace(binary.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(filename))
	if binary.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(binary.Size, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, binary.Reader)
}

func (h *DocumentHandler) Download(c *gin.Context) {
	docID := strings.TrimSpace(c.Param("docId"))
	if docID == "" {
		util.Fail(c, constant.CodeInvalidParam, "docId 不能为空")
		return
	}

	binary, err := h.docService.Download(c.Request.Context(), docID, middleware.GetUserID(c))
	if err != nil {
		switch err {
		case service.ErrDocumentNotFound:
			c.JSON(http.StatusNotFound, gin.H{"code": constant.CodeNotFound, "message": constant.MsgNotFound})
		case service.ErrDocumentPermissionDenied:
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	defer binary.Reader.Close()

	filename := binary.FileName
	if strings.TrimSpace(filename) == "" {
		filename = "document"
	}

	contentType := strings.TrimSpace(binary.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
	if binary.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(binary.Size, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, binary.Reader)
}
