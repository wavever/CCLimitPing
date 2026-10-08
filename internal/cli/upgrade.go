package cli

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/update"
)

const releaseDownloadBase = "https://github.com/wavever/CCLimitPing/releases/latest/download"

func newUpgradeCmd() *cobra.Command {
	var force bool
	text := localizedText()
	cmd := &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"up", "update"},
		Short:   text.upgradeShort,
		Long:    text.upgradeLong,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			out := cmd.OutOrStdout()
			// Say so instead of re-downloading and reinstalling the same build.
			// A failed or empty lookup falls through and upgrades anyway, which
			// is the behaviour this command has always had.
			if latest := update.Latest(ctx, updateHTTPClient); latest != "" && !force && isReleaseVersion() {
				if update.Available(version(), latest, "") == "" {
					fmt.Fprintf(out, text.upgradeCurrentFmt, update.Normalize(version()))
					// Still repair the alias. A binary installed before the
					// alias existed, or upgraded by a version that did not
					// create it, is already current and would otherwise never
					// get one — there is no upgrade left to hang it off.
					if exe, err := currentExecutable(); err == nil {
						ensureAlias(exe, out)
					}
					return nil
				}
			}
			return runUpgrade(ctx, out, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, text.upgradeForceFlag)
	return cmd
}

func runUpgrade(ctx context.Context, out, errOut io.Writer) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("self-upgrade is not supported on Windows; download the latest zip from GitHub releases")
	}

	exe, err := currentExecutable()
	if err != nil {
		return err
	}

	asset, err := releaseAssetName()
	if err != nil {
		return err
	}
	url := releaseDownloadBase + "/" + asset
	fmt.Fprintf(out, "Downloading %s\n", url)

	tmp, err := os.MkdirTemp("", "limitping-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, asset)
	if err := downloadFile(ctx, url, archivePath); err != nil {
		return err
	}
	nextBin := filepath.Join(tmp, "limitping")
	if err := extractLimitping(archivePath, nextBin); err != nil {
		return err
	}
	if err := os.Chmod(nextBin, 0o755); err != nil {
		return err
	}

	if err := replaceExecutable(nextBin, exe, out, errOut); err != nil {
		return err
	}
	fmt.Fprintf(out, "Upgraded limitping -> %s\n", exe)
	ensureAlias(exe, out)
	// The new binary, not this one, knows which hook events it relies on, so
	// it re-registers the hooks of whichever providers already have them.
	// Best-effort: the upgrade itself has already succeeded.
	refresh := exec.Command(exe, "hooks", "install", "--refresh")
	refresh.Stdout = out
	refresh.Stderr = errOut
	_ = refresh.Run()
	cmd := exec.Command(exe, "version")
	cmd.Stdout = out
	cmd.Stderr = errOut
	_ = cmd.Run()
	return nil
}

// currentExecutable is the running binary with symlinks resolved, so an upgrade
// invoked through the alias replaces the binary rather than the link.
func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating current executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolving current executable: %w", err)
	}
	return exe, nil
}

func releaseAssetName() (string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	switch goos {
	case "darwin", "linux":
		return fmt.Sprintf("limitping_%s_%s.tar.gz", goos, goarch), nil
	case "windows":
		return fmt.Sprintf("limitping_%s_%s.zip", goos, goarch), nil
	default:
		return "", fmt.Errorf("unsupported OS %q (build from source: go build ./cmd/limitping)", goos)
	}
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("download returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func extractLimitping(archivePath, dest string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"):
		return extractLimitpingTarGz(archivePath, dest)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractLimitpingZip(archivePath, dest)
	default:
		return fmt.Errorf("unsupported release archive: %s", archivePath)
	}
}

func extractLimitpingTarGz(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.FileInfo().IsDir() || filepath.Base(h.Name) != "limitping" {
			continue
		}
		return writeExtractedFile(dest, tr)
	}
	return fmt.Errorf("release archive does not contain limitping")
}

func extractLimitpingZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != "limitping.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeExtractedFile(dest, rc)
		_ = rc.Close()
		return err
	}
	return fmt.Errorf("release archive does not contain limitping.exe")
}

func writeExtractedFile(dest string, src io.Reader) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func replaceExecutable(src, dest string, out, errOut io.Writer) error {
	replaceErr := atomicReplaceExecutable(src, dest)
	if replaceErr == nil {
		return nil
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("replacing %s: %w", dest, replaceErr)
	}
	fmt.Fprintf(out, "Cannot replace %s directly (%v); retrying with sudo.\n", dest, replaceErr)
	cmd := exec.Command("sudo", "install", "-m", "0755", src, dest)
	cmd.Stdin = os.Stdin
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo install %s: %w", dest, err)
	}
	return nil
}

func atomicReplaceExecutable(src, dest string) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".limitping-upgrade-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	in, err := os.Open(src)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	defer in.Close()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	cleanup = false
	return nil
}
