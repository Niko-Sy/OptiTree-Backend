package service

import (
	"testing"

	"optitree-backend/internal/constant"
)

func TestInferReaderKind(t *testing.T) {
	cases := []struct {
		name     string
		fileType string
		mimeType string
		fileName string
		want     string
	}{
		{name: "pdf", fileType: "pdf", fileName: "a.pdf", want: constant.DocumentReaderKindPDF},
		{name: "docx_to_pdf_reader", fileType: "docx", fileName: "a.docx", want: constant.DocumentReaderKindPDF},
		{name: "xlsx_tabular", fileType: "xlsx", fileName: "a.xlsx", want: constant.DocumentReaderKindTabular},
		{name: "csv_tabular", fileType: "csv", fileName: "a.csv", want: constant.DocumentReaderKindTabular},
		{name: "text_plain", fileType: "txt", fileName: "a.txt", want: constant.DocumentReaderKindText},
		{name: "text_mime_fallback", fileType: "", mimeType: "text/plain", fileName: "a.unknown", want: constant.DocumentReaderKindText},
		{name: "unsupported", fileType: "bin", fileName: "a.bin", want: constant.DocumentReaderKindUnsupported},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferReaderKind(tc.fileType, tc.mimeType, tc.fileName)
			if got != tc.want {
				t.Fatalf("inferReaderKind(%q,%q,%q)=%q, want %q", tc.fileType, tc.mimeType, tc.fileName, got, tc.want)
			}
		})
	}
}

func TestInferPreviewStatus(t *testing.T) {
	cases := []struct {
		name     string
		fileType string
		reader   string
		want     string
	}{
		{name: "docx_processing", fileType: "docx", reader: constant.DocumentReaderKindPDF, want: constant.DocumentPreviewProcessing},
		{name: "unsupported_failed", fileType: "bin", reader: constant.DocumentReaderKindUnsupported, want: constant.DocumentPreviewFailed},
		{name: "normal_ready", fileType: "pdf", reader: constant.DocumentReaderKindPDF, want: constant.DocumentPreviewReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferPreviewStatus(tc.fileType, tc.reader)
			if got != tc.want {
				t.Fatalf("inferPreviewStatus(%q,%q)=%q, want %q", tc.fileType, tc.reader, got, tc.want)
			}
		})
	}
}

func TestSearchTextContent(t *testing.T) {
	content := "第一行\n第二行包含故障树关键字\n第三行"
	keyword := "故障树"

	locator, snippet, ok := searchTextContent(content, keyword)
	if !ok {
		t.Fatal("expected text search hit")
	}
	if locator.Type != constant.DocumentReaderKindText {
		t.Fatalf("locator type=%q, want %q", locator.Type, constant.DocumentReaderKindText)
	}
	if locator.LineStart != 1 || locator.LineEnd != 1 {
		t.Fatalf("line range=(%d,%d), want (1,1)", locator.LineStart, locator.LineEnd)
	}
	if locator.StartOffset != 5 || locator.EndOffset != 8 {
		t.Fatalf("offset=(%d,%d), want (5,8)", locator.StartOffset, locator.EndOffset)
	}
	if snippet == "" {
		t.Fatal("snippet should not be empty")
	}
}

func TestSearchTabularContentCSV(t *testing.T) {
	data := []byte("col1,col2\nfoo,液压泵故障")
	keyword := "液压泵"

	locator, snippet, ok := searchTabularContent(data, keyword, "csv")
	if !ok {
		t.Fatal("expected tabular search hit")
	}
	if locator.Type != constant.DocumentReaderKindTabular {
		t.Fatalf("locator type=%q, want %q", locator.Type, constant.DocumentReaderKindTabular)
	}
	if locator.RowIndex != 1 || locator.ColIndex != 1 {
		t.Fatalf("row/col=(%d,%d), want (1,1)", locator.RowIndex, locator.ColIndex)
	}
	if locator.StartOffset != 0 || locator.EndOffset != 3 {
		t.Fatalf("offset=(%d,%d), want (0,3)", locator.StartOffset, locator.EndOffset)
	}
	if snippet == "" {
		t.Fatal("snippet should not be empty")
	}
}
