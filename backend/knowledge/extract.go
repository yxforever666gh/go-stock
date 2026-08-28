package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	pdf "github.com/ledongthuc/pdf"
)

const MaxDocumentBytes = 10 * 1024 * 1024

var (
	ErrInvalidInput      = errors.New("invalid knowledge input")
	ErrNotFound          = errors.New("knowledge item not found")
	ErrConflict          = errors.New("knowledge state conflict")
	ErrApprovalForbidden = errors.New("knowledge approval requires a user")
)

type ExtractedText struct {
	Text     string
	MimeType string
}

func ExtractText(filename, mimeType string, data []byte) (ExtractedText, error) {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return ExtractedText{}, fmt.Errorf("%w: decoded file must be between 1 byte and 10 MiB", ErrInvalidInput)
	}
	filename = strings.TrimSpace(filename)
	mimeType = normalizeMimeType(filename, mimeType)
	var text string
	switch mimeType {
	case "text/plain", "text/markdown":
		if !utf8.Valid(data) {
			return ExtractedText{}, fmt.Errorf("%w: text document is not valid UTF-8", ErrInvalidInput)
		}
		text = string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	case "application/pdf":
		reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return ExtractedText{}, fmt.Errorf("%w: parse PDF: %v", ErrInvalidInput, err)
		}
		plain, err := reader.GetPlainText()
		if err != nil {
			return ExtractedText{}, fmt.Errorf("%w: extract PDF text: %v", ErrInvalidInput, err)
		}
		content, err := io.ReadAll(io.LimitReader(plain, MaxDocumentBytes+1))
		if err != nil || len(content) > MaxDocumentBytes {
			return ExtractedText{}, fmt.Errorf("%w: extracted PDF text is too large", ErrInvalidInput)
		}
		text = string(content)
	default:
		return ExtractedText{}, fmt.Errorf("%w: unsupported mime type %q", ErrInvalidInput, mimeType)
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\x00", ""))
	if text == "" {
		return ExtractedText{}, fmt.Errorf("%w: document contains no extractable text", ErrInvalidInput)
	}
	return ExtractedText{Text: text, MimeType: mimeType}, nil
}

func normalizeMimeType(filename, value string) string {
	if parsed, _, err := mime.ParseMediaType(strings.TrimSpace(value)); err == nil {
		value = strings.ToLower(parsed)
	} else {
		value = strings.ToLower(strings.TrimSpace(value))
	}
	switch value {
	case "text/plain", "text/markdown", "application/pdf":
		return value
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt":
		return "text/plain"
	case ".md", ".markdown":
		return "text/markdown"
	case ".pdf":
		return "application/pdf"
	}
	return value
}
