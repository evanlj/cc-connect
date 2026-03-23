package feishu

import "testing"

func TestDetectFeishuUploadFileType(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{name: "opus", filePath: "D:/tmp/voice.opus", want: "opus"},
		{name: "mp4", filePath: "D:/tmp/demo.mp4", want: "mp4"},
		{name: "pdf", filePath: "D:/tmp/spec.pdf", want: "pdf"},
		{name: "docx", filePath: "D:/tmp/spec.docx", want: "doc"},
		{name: "xlsx", filePath: "D:/tmp/data.xlsx", want: "xls"},
		{name: "pptx", filePath: "D:/tmp/slides.pptx", want: "ppt"},
		{name: "markdown as stream", filePath: "D:/tmp/plan.md", want: "stream"},
		{name: "unknown ext as stream", filePath: "D:/tmp/archive.zip", want: "stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectFeishuUploadFileType(tt.filePath); got != tt.want {
				t.Fatalf("detectFeishuUploadFileType(%q)=%q, want %q", tt.filePath, got, tt.want)
			}
		})
	}
}
