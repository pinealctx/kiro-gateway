package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestFindReleaseAsset(t *testing.T) {
	release := &githubRelease{
		TagName: "v1.2.3",
		Assets: []githubAsset{
			{Name: "kiro-gateway_v1.2.3_linux_arm64.tar.gz", DownloadURL: "arm64"},
			{Name: "kiro-gateway_v1.2.3_linux_amd64.tar.gz", DownloadURL: "amd64"},
		},
	}
	asset, err := findReleaseAsset(release, "linux", "amd64")
	if err != nil {
		t.Fatalf("findReleaseAsset() error = %v", err)
	}
	if asset.DownloadURL != "amd64" {
		t.Fatalf("DownloadURL = %q, want amd64", asset.DownloadURL)
	}
}

func TestFindReleaseAssetMissing(t *testing.T) {
	release := &githubRelease{TagName: "v1.2.3"}
	if _, err := findReleaseAsset(release, "linux", "amd64"); err == nil {
		t.Fatal("findReleaseAsset() expected error for missing asset")
	}
}

func TestUpdateCommandSilencesUsageOnRuntimeErrors(t *testing.T) {
	if !updateCmd.SilenceUsage {
		t.Fatal("update command should not print usage for runtime update errors")
	}
}

func TestExtractTarGzBinary(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "kiro-gateway_v1.2.3_linux_amd64.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	payload := []byte("binary")
	if err := tw.WriteHeader(&tar.Header{
		Name: "kiro-gateway_v1.2.3_linux_amd64/kiro-gateway",
		Mode: 0755,
		Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "new-kiro-gateway")
	if err := extractReleaseBinary(archivePath, dest); err != nil {
		t.Fatalf("extractReleaseBinary() error = %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("extracted binary = %q, want binary", got)
	}
}

func TestExtractZipBinary(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "kiro-gateway_v1.2.3_windows_amd64.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	writer, err := zw.Create("kiro-gateway_v1.2.3_windows_amd64/kiro-gateway.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("binary.exe")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "new-kiro-gateway.exe")
	if err := extractReleaseBinary(archivePath, dest); err != nil {
		t.Fatalf("extractReleaseBinary() error = %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary.exe" {
		t.Fatalf("extracted binary = %q, want binary.exe", got)
	}
}
