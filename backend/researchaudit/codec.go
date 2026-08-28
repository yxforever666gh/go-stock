package researchaudit

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

func encodeGZIP(value string) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, "", err
	}
	writer.Header.ModTime = time.Unix(0, 0).UTC()
	writer.Header.OS = 255
	if _, err = writer.Write([]byte(value)); err != nil {
		return nil, "", err
	}
	if err = writer.Close(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256([]byte(value))
	return buffer.Bytes(), hex.EncodeToString(sum[:]), nil
}

func decodeGZIP(blob []byte, expected string) (string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return "", err
	}
	value, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	sum := sha256.Sum256(value)
	actual := hex.EncodeToString(sum[:])
	if expected != "" && actual != expected {
		return "", fmt.Errorf("audit payload hash mismatch: expected %s, got %s", expected, actual)
	}
	return string(value), nil
}
