package cli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-stock/internal/releaseinfo"
)

type releaseInspectResult struct {
	Manifest releaseinfo.ReleaseManifest `json:"manifest"`
	Build    releaseinfo.BuildInfo       `json:"build"`
}

func runRelease(args []string, _ GlobalOptions, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: release inspect|verify-zoneinfo")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "inspect":
		if len(args) != 1 {
			return fmt.Errorf("usage: release inspect")
		}
		payload, err := json.MarshalIndent(releaseInspectResult{Manifest: releaseinfo.Manifest(), Build: releaseinfo.Build()}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(payload))
		return err
	case "verify-zoneinfo":
		return runVerifyZoneinfo(args[1:], stdout)
	default:
		return fmt.Errorf("usage: release inspect|verify-zoneinfo")
	}
}

func runVerifyZoneinfo(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release verify-zoneinfo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", "", "path to zoneinfo ZIP")
	expected := fs.String("expect-sha256", "", "expected lowercase SHA256")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" || len(*expected) != sha256.Size*2 || *expected != strings.ToLower(*expected) {
		return fmt.Errorf("usage: release verify-zoneinfo --path <zoneinfo.zip> --expect-sha256 <lowercase-sha256>")
	}
	absolute, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if actual != *expected {
		return fmt.Errorf("zoneinfo ZIP SHA256 mismatch: got %s want %s", actual, *expected)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var zone []byte
	for _, entry := range archive.File {
		if entry.Name != "Asia/Shanghai" {
			continue
		}
		reader, openErr := entry.Open()
		if openErr != nil {
			return openErr
		}
		zone, err = io.ReadAll(io.LimitReader(reader, 1<<20))
		_ = reader.Close()
		if err != nil {
			return err
		}
		break
	}
	if len(zone) == 0 {
		return fmt.Errorf("zoneinfo ZIP is missing Asia/Shanghai")
	}
	location, err := time.LoadLocationFromTZData("Asia/Shanghai", zone)
	if err != nil {
		return err
	}
	_, offset := time.Date(2024, time.January, 1, 12, 0, 0, 0, location).Zone()
	if offset != 8*60*60 {
		return fmt.Errorf("Asia/Shanghai offset mismatch")
	}
	_, err = fmt.Fprintf(stdout, "Zoneinfo ZIP verified: path=%s sha256=%s Asia/Shanghai=+08:00\n", absolute, actual)
	return err
}
