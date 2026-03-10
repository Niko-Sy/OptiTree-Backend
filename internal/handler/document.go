package handler

import (
	"fmt"
	"mime"

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
			"id":       doc.ID,
			"fileName": doc.FileName,
			"status":   doc.Status,
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
