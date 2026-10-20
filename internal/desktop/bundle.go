package desktop

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// GuacdVersion is the guacamole-server release the bundle is built from.
const GuacdVersion = "1.6.0"

// guacdManifest holds the SHA-256 of each published bundle, in sha256sum
// format. The daemon refuses a bundle that is not listed here, so a hash in
// the source tree vouches for an asset that is downloaded later (D5). The
// release job rebuilds each bundle and checks it against this file, which
// works because the build is reproducible: see scripts/build-guacd.sh.
//
//go:embed guacd.sha256
var guacdManifest string

// BundleEnv names a local bundle to install instead of downloading one. It is
// for development and the container e2e, where the bundle was just built and
// no release carries it yet.
const BundleEnv = "HELIOS_GUACD_BUNDLE"

// releaseBase is where published bundles live.
const releaseBase = "https://github.com/kamrul1157024/helios/releases"

// bundleAsset is the release asset name for an arch.
func bundleAsset(arch string) string {
	return fmt.Sprintf("helios-guacd-%s-linux-%s.tar.zst", GuacdVersion, arch)
}

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// bundleURL is the download URL for the running build. A release build takes
// the asset from its own release; a dev build, which belongs to no release,
// takes it from the newest one. The asset name carries the guacd version, not
// the Helios one, so the same name is right in both places — and the hash
// check makes the source irrelevant to trust.
func bundleURL(heliosVersion, arch string) string {
	asset := bundleAsset(arch)
	if releaseVersion.MatchString(heliosVersion) {
		return fmt.Sprintf("%s/download/v%s/%s", releaseBase, heliosVersion, asset)
	}
	return fmt.Sprintf("%s/latest/download/%s", releaseBase, asset)
}

// manifestHash looks an asset up in the embedded manifest.
func manifestHash(manifest, asset string) (string, bool) {
	for _, line := range strings.Split(manifest, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// bundleSource is where step 5 gets the bundle and what it must hash to. An
// empty want means "any": only the local override may carry that.
type bundleSource struct {
	path string // local file, when the override is set
	url  string
	want string
}

// resolveBundle decides the bundle for this machine.
func resolveBundle(heliosVersion string) (bundleSource, error) {
	asset := bundleAsset(runtime.GOARCH)
	want, listed := manifestHash(guacdManifest, asset)
	if override := os.Getenv(BundleEnv); override != "" {
		return bundleSource{path: override}, nil
	}
	if !listed {
		return bundleSource{}, fmt.Errorf("this build of Helios has no desktop service for linux/%s", runtime.GOARCH)
	}
	return bundleSource{url: bundleURL(heliosVersion, runtime.GOARCH), want: want}, nil
}

// bundleMarker records which bundle a directory holds, so the next start can
// tell a current bundle from one an older daemon installed.
const bundleMarker = ".bundle-sha256"

// installedBundleHash reads the marker of the installed bundle.
func installedBundleHash(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, bundleMarker))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, "sbin", "guacd")); err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// fileHash returns the hex SHA-256 of a file.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// expectedBundleHash is the hash the installed bundle must carry to count as
// current: the manifest's, or the override file's own.
func expectedBundleHash(src bundleSource) (string, error) {
	if src.path != "" {
		return fileHash(src.path)
	}
	return src.want, nil
}

// errBundleUnavailable is the failure the person sees for any download or
// hash problem: "Could not get the desktop service", and Retry.
var errBundleUnavailable = errors.New("could not get the desktop service")

// installBundle fetches, checks and unpacks the bundle into dir. The new
// bundle is unpacked beside the old one and swapped in by rename, so a failed
// update leaves the working bundle in place.
func installBundle(ctx context.Context, src bundleSource, dir string, progress func(string)) error {
	archive := src.path
	if archive == "" {
		tmp, err := download(ctx, src.url, filepath.Dir(dir), progress)
		if err != nil {
			return fmt.Errorf("%w: %v", errBundleUnavailable, err)
		}
		defer os.Remove(tmp)
		archive = tmp
	}
	got, err := fileHash(archive)
	if err != nil {
		return fmt.Errorf("%w: %v", errBundleUnavailable, err)
	}
	if src.want != "" && got != src.want {
		return fmt.Errorf("%w: the download's SHA-256 is %s, expected %s", errBundleUnavailable, got, src.want)
	}
	progress("Unpacking the desktop service")
	staging := dir + ".new"
	os.RemoveAll(staging)
	if err := extractTarZst(archive, staging); err != nil {
		os.RemoveAll(staging)
		return fmt.Errorf("unpack desktop service: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, bundleMarker), []byte(got+"\n"), 0o644); err != nil {
		os.RemoveAll(staging)
		return fmt.Errorf("mark desktop service: %w", err)
	}
	old := dir + ".old"
	os.RemoveAll(old)
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move old desktop service: %w", err)
	}
	if err := os.Rename(staging, dir); err != nil {
		return fmt.Errorf("install desktop service: %w", err)
	}
	os.RemoveAll(old)
	return nil
}

// downloadTimeout bounds the fetch. The bundle is about 25 MB.
const downloadTimeout = 5 * time.Minute

func download(ctx context.Context, url, dir string, progress func(string)) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	progress("Downloading " + url)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp(dir, "guacd-*.tar.zst")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// extractTarZst unpacks the bundle into dest, dropping the archive's single
// top-level folder. Only regular files, folders and symlinks that stay inside
// dest are accepted; the hash already vouches for the archive, and this keeps
// a bad override from writing anywhere else.
func extractTarZst(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("zstd: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if err := extractEntry(tr, hdr, dest); err != nil {
			return err
		}
	}
}

func extractEntry(tr *tar.Reader, hdr *tar.Header, dest string) error {
	_, rel, _ := strings.Cut(strings.TrimPrefix(hdr.Name, "./"), "/")
	if rel == "" {
		return os.MkdirAll(dest, 0o755)
	}
	target := filepath.Join(dest, rel)
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
		return fmt.Errorf("archive entry %q leaves the bundle", hdr.Name)
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode).Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return fmt.Errorf("write %s: %w", target, err)
		}
		return out.Close()
	case tar.TypeSymlink:
		if filepath.IsAbs(hdr.Linkname) || strings.Contains(hdr.Linkname, "..") {
			return fmt.Errorf("archive link %q leaves the bundle", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.Symlink(hdr.Linkname, target)
	}
	return fmt.Errorf("archive entry %q has unsupported type %c", hdr.Name, hdr.Typeflag)
}
