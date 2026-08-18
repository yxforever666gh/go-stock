package cli

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cliports "go-stock/internal/cli/ports"
)

const (
	databaseArchiveFormatVersion    = 1
	legacyStrategyArchiveTableCount = 17
)

type databaseArchiveOptions struct {
	Output              string
	SourceAppVersion    string
	SourceCommit        string
	MainSchemaVersion   int
	MinuteSchemaVersion int
}

type databaseArchiveResult struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type databaseArchiveManifest struct {
	FormatVersion       int              `json:"formatVersion"`
	CreatedAt           string           `json:"createdAt"`
	SourceAppVersion    string           `json:"sourceAppVersion"`
	SourceCommit        string           `json:"sourceCommit"`
	MainSchemaVersion   int              `json:"mainSchemaVersion"`
	MinuteSchemaVersion int              `json:"minuteSchemaVersion"`
	Files               []archiveFile    `json:"files"`
	LegacyTableRows     map[string]int64 `json:"legacyTableRows"`
}

type archiveFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

func createDatabaseArchive(ctx context.Context, admin cliports.StorageAdmin, options databaseArchiveOptions) (*databaseArchiveResult, error) {
	if admin == nil {
		return nil, fmt.Errorf("database storage admin is required")
	}
	output, err := filepath.Abs(strings.TrimSpace(options.Output))
	if err != nil || strings.TrimSpace(options.Output) == "" {
		return nil, fmt.Errorf("archive output path is required")
	}
	if !strings.EqualFold(filepath.Ext(output), ".zip") {
		return nil, fmt.Errorf("archive output must use .zip extension")
	}
	if strings.TrimSpace(options.SourceAppVersion) == "" || strings.TrimSpace(options.SourceCommit) == "" {
		return nil, fmt.Errorf("archive source application version and commit are required")
	}
	if options.MainSchemaVersion <= 0 || options.MinuteSchemaVersion <= 0 {
		return nil, fmt.Errorf("archive source schema versions are required")
	}
	if _, err := os.Stat(output); err == nil {
		return nil, fmt.Errorf("archive output already exists: %s", output)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(output), ".archive-staging-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	if err := admin.QuickCheck(ctx); err != nil {
		return nil, fmt.Errorf("verify source databases before archive: %w", err)
	}
	mainPath := filepath.Join(staging, "stock.db")
	minutePath := filepath.Join(staging, "minute.db")
	if err := admin.Backup(ctx, mainPath, minutePath); err != nil {
		return nil, err
	}
	counts, err := admin.LegacyStrategyRowCounts(ctx)
	if err != nil {
		return nil, err
	}
	if len(counts) != legacyStrategyArchiveTableCount {
		return nil, fmt.Errorf("archive legacy table inventory has %d entries, expected %d", len(counts), legacyStrategyArchiveTableCount)
	}
	files := make([]archiveFile, 0, 2)
	for _, item := range []struct{ name, path string }{{"stock.db", mainPath}, {"minute.db", minutePath}} {
		info, err := os.Stat(item.path)
		if err != nil {
			return nil, err
		}
		hash, err := fileSHA256(item.path)
		if err != nil {
			return nil, err
		}
		files = append(files, archiveFile{Name: item.name, SizeBytes: info.Size(), SHA256: hash})
	}
	manifest := databaseArchiveManifest{
		FormatVersion: databaseArchiveFormatVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		SourceAppVersion: strings.TrimSpace(options.SourceAppVersion), SourceCommit: strings.TrimSpace(options.SourceCommit),
		MainSchemaVersion: options.MainSchemaVersion, MinuteSchemaVersion: options.MinuteSchemaVersion,
		Files: files, LegacyTableRows: counts,
	}
	manifestPath := filepath.Join(staging, "manifest.json")
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, append(payload, '\n'), 0o600); err != nil {
		return nil, err
	}
	if err := writeDatabaseArchive(output, staging, []string{"stock.db", "minute.db", "manifest.json"}); err != nil {
		_ = os.Remove(output)
		return nil, err
	}
	if err := verifyDatabaseArchive(output); err != nil {
		_ = os.Remove(output)
		return nil, err
	}
	archiveHash, err := fileSHA256(output)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(output)
	if err != nil {
		return nil, err
	}
	return &databaseArchiveResult{Path: output, SHA256: archiveHash, SizeBytes: info.Size()}, nil
}

func writeDatabaseArchive(output, staging string, names []string) error {
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := zip.NewWriter(file)
	failed := true
	defer func() {
		if failed {
			_ = w.Close()
			_ = file.Close()
		}
	}()
	for _, name := range names {
		if err := addZipFile(w, name, filepath.Join(staging, name)); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

func addZipFile(w *zip.Writer, name, path string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = io.Copy(destination, source)
	return err
}

func verifyDatabaseArchive(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	if len(reader.File) != 3 {
		return fmt.Errorf("archive has %d entries, expected 3", len(reader.File))
	}
	entries := make(map[string]*zip.File, len(reader.File))
	for _, entry := range reader.File {
		if entry.Name != "stock.db" && entry.Name != "minute.db" && entry.Name != "manifest.json" {
			return fmt.Errorf("archive contains unexpected entry %s", entry.Name)
		}
		if entries[entry.Name] != nil {
			return fmt.Errorf("archive contains duplicate entry %s", entry.Name)
		}
		entries[entry.Name] = entry
	}
	manifestEntry := entries["manifest.json"]
	if manifestEntry == nil {
		return fmt.Errorf("archive manifest.json is missing")
	}
	manifestBytes, err := readZipEntry(manifestEntry)
	if err != nil {
		return err
	}
	var manifest databaseArchiveManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("parse archive manifest: %w", err)
	}
	if manifest.FormatVersion != databaseArchiveFormatVersion {
		return fmt.Errorf("archive format version %d is unsupported", manifest.FormatVersion)
	}
	if strings.TrimSpace(manifest.SourceAppVersion) == "" || strings.TrimSpace(manifest.SourceCommit) == "" {
		return fmt.Errorf("archive source release identity is missing")
	}
	if manifest.MainSchemaVersion <= 0 || manifest.MinuteSchemaVersion <= 0 {
		return fmt.Errorf("archive source schema identity is missing")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return fmt.Errorf("archive creation time is invalid: %w", err)
	}
	if len(manifest.LegacyTableRows) != legacyStrategyArchiveTableCount {
		return fmt.Errorf("archive legacy table manifest has %d entries, expected %d", len(manifest.LegacyTableRows), legacyStrategyArchiveTableCount)
	}
	seen := make([]string, 0, len(manifest.Files))
	seenFiles := make(map[string]struct{}, len(manifest.Files))
	for _, expected := range manifest.Files {
		if _, duplicate := seenFiles[expected.Name]; duplicate {
			return fmt.Errorf("archive manifest contains duplicate file %s", expected.Name)
		}
		seenFiles[expected.Name] = struct{}{}
		entry := entries[expected.Name]
		if entry == nil {
			return fmt.Errorf("archive entry %s is missing", expected.Name)
		}
		if int64(entry.UncompressedSize64) != expected.SizeBytes {
			return fmt.Errorf("archive entry %s failed size verification", expected.Name)
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, reader)
		closeErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("archive entry %s failed hash verification", expected.Name)
		}
		seen = append(seen, expected.Name)
	}
	sort.Strings(seen)
	if strings.Join(seen, ",") != "minute.db,stock.db" {
		return fmt.Errorf("archive database entries are %v", seen)
	}
	return nil
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
