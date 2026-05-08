package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pinealctx/kiro-gateway/version"
	"github.com/spf13/cobra"
)

const (
	updateRepo = "pinealctx/kiro-gateway"
	updateApp  = "kiro-gateway"
)

func init() {
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:          "update",
	Short:        "Update kiro-gateway to the latest GitHub release",
	SilenceUsage: true,
	RunE:         runUpdate,
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	if runtime.GOOS == "windows" {
		return errors.New("self-update on Windows is not supported yet; run the PowerShell installer again")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()

	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return err
	}
	current := version.Get()
	if release.TagName == "" {
		return errors.New("latest release does not include a tag name")
	}
	if current == release.TagName {
		fmt.Fprintf(cmd.OutOrStdout(), "kiro-gateway is already up to date (%s)\n", current)
		return nil
	}

	asset, err := findReleaseAsset(release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	target, err := filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", updateApp+"-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, asset.Name)
	if err := downloadFile(ctx, asset.DownloadURL, archivePath); err != nil {
		return err
	}
	newBinary := filepath.Join(tmpDir, updateApp)
	if err := extractReleaseBinary(archivePath, newBinary); err != nil {
		return err
	}
	if err := replaceExecutable(target, newBinary); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated kiro-gateway from %s to %s\n", current, release.TagName)
	return nil
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("create release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", updateApp+"/"+version.Get())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("fetch latest release returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode latest release: %w", err)
	}
	return &release, nil
}

func findReleaseAsset(release *githubRelease, goos, goarch string) (*githubAsset, error) {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	want := fmt.Sprintf("%s_%s_%s_%s%s", updateApp, release.TagName, goos, goarch, ext)
	for i := range release.Assets {
		if release.Assets[i].Name == want {
			return &release.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("no release asset found for %s/%s (%s)", goos, goarch, want)
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", updateApp+"/"+version.Get())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download release asset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download release asset returned %d", resp.StatusCode)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write archive file: %w", err)
	}
	return nil
}

func extractReleaseBinary(archivePath, dest string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZipBinary(archivePath, dest)
	}
	return extractTarGzBinary(archivePath, dest)
}

func extractTarGzBinary(archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read gzip archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != updateApp {
			continue
		}
		return writeExtractedBinary(dest, tr, 0755)
	}
	return errors.New("kiro-gateway binary not found in release archive")
}

func extractZipBinary(archivePath, dest string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer func() { _ = reader.Close() }()
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != updateApp+".exe" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("open binary in zip archive: %w", err)
		}
		err = writeExtractedBinary(dest, rc, 0755)
		_ = rc.Close()
		return err
	}
	return errors.New("kiro-gateway.exe binary not found in release archive")
}

func writeExtractedBinary(dest string, src io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create extracted binary: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, src); err != nil {
		return fmt.Errorf("write extracted binary: %w", err)
	}
	return nil
}

func replaceExecutable(target, newBinary string) error {
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("stat current executable: %w", err)
	}
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return replaceExecutableWithSudo(target, newBinary, info.Mode().Perm())
		}
		return fmt.Errorf("backup current executable: %w", err)
	}
	if err := copyFile(newBinary, target, info.Mode().Perm()); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("install new executable: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

func replaceExecutableWithSudo(target, newBinary string, mode os.FileMode) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("backup current executable: permission denied")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("backup current executable: permission denied; rerun with sudo or reinstall to $HOME/.kiro-gateway/bin")
	}

	script := `set -eu
target=$1
src=$2
mode=$3
backup="${target}.old"
restore_on_error() {
  status=$?
  if [ "$status" -ne 0 ] && [ -f "$backup" ]; then
    rm -f "$target" 2>/dev/null || true
    mv -f "$backup" "$target" 2>/dev/null || true
  fi
  exit "$status"
}
trap restore_on_error EXIT
rm -f "$backup"
mv "$target" "$backup"
cp "$src" "$target"
chmod "$mode" "$target"
rm -f "$backup"
trap - EXIT
`
	cmd := exec.Command("sudo", "sh", "-c", script, "kiro-gateway-update", target, newBinary, fmt.Sprintf("%#o", mode.Perm()))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("replace executable with sudo: %w", err)
	}
	return nil
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
