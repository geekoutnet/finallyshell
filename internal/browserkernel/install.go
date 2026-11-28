package browserkernel

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

// maxUnpacked bounds what an archive may unpack to.
const maxUnpacked = 16 << 30

// stallTimeout is how long a download may go without a byte.
var stallTimeout = time.Minute

// Install downloads rel over c (nil for http.DefaultClient), checks it,
// unpacks it and puts it in place of the kernel's current version, if
// any. progress, if not nil, is called from Install's goroutine, at most
// a few times a second.
func (m *Manager) Install(ctx context.Context, c *http.Client, rel Release, progress func(Progress)) (Installed, error) {
	end, err := m.begin(rel.Kind)
	if err != nil {
		return Installed{}, err
	}
	defer end()
	if err := httpsURL(rel.URL); err != nil {
		return Installed{}, err
	}
	if !versionPattern.MatchString(rel.Version) {
		return Installed{}, fmt.Errorf("invalid version %q", rel.Version)
	}
	if progress == nil {
		progress = func(Progress) {}
	}
	if err := os.MkdirAll(m.Root, 0o700); err != nil {
		return Installed{}, err
	}
	work, err := os.MkdirTemp(m.Root, ".tmp-"+string(rel.Kind)+"-")
	if err != nil {
		return Installed{}, err
	}
	defer removeAll(work)
	if err := grantSandbox(work); err != nil {
		return Installed{}, err
	}
	archive := filepath.Join(work, "download."+rel.Format)
	if err := download(ctx, c, rel, archive, progress); err != nil {
		return Installed{}, err
	}
	app := filepath.Join(work, "app")
	progress(Progress{Phase: Extracting})
	if err := m.unpack(ctx, rel, archive, work, app, progress); err != nil {
		return Installed{}, err
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	if err := os.Remove(archive); err != nil {
		return Installed{}, err
	}
	exe, err := m.locate(rel.Kind, app)
	if err != nil {
		return Installed{}, err
	}
	installed := Installed{Kind: rel.Kind, Version: rel.Version, Platform: m.GOOS + "/" + m.GOARCH, Exe: exe, Size: dirSize(app), InstalledAt: time.Now().UTC().Truncate(time.Second)}
	data, err := json.MarshalIndent(installed, "", "  ")
	if err != nil {
		return Installed{}, err
	}
	if err := os.WriteFile(filepath.Join(work, manifestName), append(data, '\n'), 0o600); err != nil {
		return Installed{}, err
	}
	if err := m.swap(rel.Kind, work); err != nil {
		return Installed{}, err
	}
	installed.Dir = m.Dir(rel.Kind)
	return installed, nil
}

// swap puts work in place of the kernel's folder, moving the old one
// aside first and back if work cannot take its place.
func (m *Manager) swap(k Kind, work string) error {
	dir := m.Dir(k)
	trash := ""
	if _, err := os.Lstat(dir); err == nil {
		if trash, err = m.trashName(k); err != nil {
			return err
		}
		if err := rename(dir, trash); err != nil {
			return err
		}
	}
	if err := rename(work, dir); err != nil {
		if trash != "" {
			_ = rename(trash, dir)
		}
		return err
	}
	if trash != "" {
		_ = removeAll(trash)
	}
	return nil
}

// downloadAttempts is how many times a download is tried: a dropped or
// stalled connection resumes from the byte reached.
const downloadAttempts = 4

// retryDelay is the wait before the second attempt, doubled for each
// next one.
var retryDelay = time.Second

// transient is a failure another attempt may not have.
type transient struct{ err error }

func (t transient) Error() string { return t.err.Error() }
func (t transient) Unwrap() error { return t.err }

type downloader struct {
	client      *http.Client
	rel         Release
	file        *os.File
	hash        hash.Hash
	done, total int64
	last        time.Time
	progress    func(Progress)
}

func download(ctx context.Context, c *http.Client, rel Release, path string, progress func(Progress)) error {
	progress(Progress{Phase: Resolving})
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	d := &downloader{client: client(c), rel: rel, file: file, hash: sha256.New(), progress: progress}
	delay := retryDelay
	for attempt := 1; ; attempt++ {
		err = d.attempt(ctx)
		var again transient
		if err == nil || ctx.Err() != nil || !errors.As(err, &again) || attempt == downloadAttempts {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		delay *= 2
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	progress(Progress{Phase: Downloading, Done: d.done, Total: d.total})
	if d.total > 0 && d.done != d.total {
		return fmt.Errorf("download: got %d of %d bytes", d.done, d.total)
	}
	if rel.SHA256 != "" && hex.EncodeToString(d.hash.Sum(nil)) != strings.ToLower(rel.SHA256) {
		return ErrChecksum
	}
	return nil
}

// attempt downloads the rest of the file: all of it, or from d.done with
// a range request.
func (d *downloader) attempt(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(stallTimeout, func() { cancel(ErrStalled) })
	defer stall.Stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.rel.URL, nil)
	if err != nil {
		return err
	}
	if d.done > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", d.done))
	}
	response, err := d.client.Do(request)
	if err != nil {
		return transient{causeOf(ctx, err)}
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
		// The whole file: the first attempt, or a server that ignored
		// the range.
		if d.done > 0 {
			if err := d.restart(); err != nil {
				return err
			}
		}
		total := response.ContentLength
		if d.rel.Size > 0 {
			if total > 0 && total != d.rel.Size {
				return fmt.Errorf("download: size %d, expected %d", total, d.rel.Size)
			}
			total = d.rel.Size
		}
		d.total = max(total, 0)
	case response.StatusCode == http.StatusPartialContent && d.done > 0:
		var start, end, size int64
		if _, err := fmt.Sscanf(response.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &size); err != nil || start != d.done || d.total > 0 && size != d.total {
			return fmt.Errorf("download: unexpected range %q", response.Header.Get("Content-Range"))
		}
	case response.StatusCode >= 500:
		return transient{fmt.Errorf("download: %s", response.Status)}
	default:
		return fmt.Errorf("download: %s", response.Status)
	}
	buffer := make([]byte, 256<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			stall.Reset(stallTimeout)
			if d.total > 0 && d.done+int64(n) > d.total {
				return fmt.Errorf("download: more than %d bytes", d.total)
			}
			if _, err := d.file.Write(buffer[:n]); err != nil {
				return err
			}
			d.hash.Write(buffer[:n])
			d.done += int64(n)
			if now := time.Now(); now.Sub(d.last) > 200*time.Millisecond {
				d.last = now
				d.progress(Progress{Phase: Downloading, Done: d.done, Total: d.total})
			}
		}
		if readErr == io.EOF {
			if d.total > 0 && d.done < d.total {
				return transient{io.ErrUnexpectedEOF}
			}
			return nil
		}
		if readErr != nil {
			return transient{causeOf(ctx, readErr)}
		}
	}
}

func (d *downloader) restart() error {
	if err := d.file.Truncate(0); err != nil {
		return err
	}
	if _, err := d.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	d.hash.Reset()
	d.done = 0
	return nil
}

// causeOf is ErrStalled or the context's error when the context ended
// the request, err otherwise.
func causeOf(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return err
}

func (m *Manager) unpack(ctx context.Context, rel Release, archive, work, app string, progress func(Progress)) error {
	switch rel.Format {
	case "zip":
		return unzip(ctx, archive, app, progress)
	case "tar.xz":
		return untarXZ(ctx, archive, app, progress)
	case "exe":
		// Firefox's full installer unpacks itself without installing
		// with /ExtractDir; the browser is its core folder.
		out := filepath.Join(work, "extract")
		if err := m.run(ctx, archive, "/ExtractDir="+out); err != nil {
			return err
		}
		return rename(filepath.Join(out, "core"), app)
	case "dmg":
		mount := filepath.Join(work, "mount")
		if err := os.Mkdir(mount, 0o700); err != nil {
			return err
		}
		if err := m.run(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mount, archive); err != nil {
			return err
		}
		defer m.run(context.Background(), "hdiutil", "detach", mount, "-force")
		bundles, _ := filepath.Glob(filepath.Join(mount, "*.app"))
		if len(bundles) != 1 {
			return errors.New("dmg: no application in the image")
		}
		if err := os.Mkdir(app, 0o755); err != nil {
			return err
		}
		return m.run(ctx, "ditto", bundles[0], filepath.Join(app, filepath.Base(bundles[0])))
	}
	return fmt.Errorf("unknown format %q", rel.Format)
}

func runTool(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// entryPath is where an archive's entry goes under dest: an error for
// names that would leave it.
func entryPath(dest, name string) (string, error) {
	name = strings.TrimSuffix(name, "/")
	local := filepath.FromSlash(name)
	if name == "" || strings.Contains(name, `\`) || !filepath.IsLocal(local) {
		return "", fmt.Errorf("archive entry %q outside the folder", name)
	}
	path := filepath.Join(dest, local)
	if err := noLinkParents(dest, path); err != nil {
		return "", err
	}
	return path, nil
}

// noLinkParents refuses paths under dest that go through a symbolic link
// the archive made: links are only checked where they are, so writing
// through one, or linking from inside one, could leave dest.
func noLinkParents(dest, path string) error {
	rel, err := filepath.Rel(dest, filepath.Dir(path))
	if err != nil || rel == "." {
		return err
	}
	current := dest
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("archive entry %q goes through a link", path)
		}
	}
	return nil
}

// linkInside reports whether a symbolic link at path to target stays in
// dest.
func linkInside(dest, path, target string) bool {
	if target == "" || filepath.IsAbs(target) || strings.HasPrefix(target, "/") || strings.Contains(target, `\`) {
		return false
	}
	rel, err := filepath.Rel(dest, filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
	return err == nil && filepath.IsLocal(rel)
}

func writeEntry(ctx context.Context, path string, mode fs.FileMode, r io.Reader, written *int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	n, err := io.Copy(file, contextReader{ctx, io.LimitReader(r, maxUnpacked-*written+1)})
	*written += n
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && *written > maxUnpacked {
		err = errors.New("archive unpacks too large")
	}
	return err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func unzip(ctx context.Context, archive, dest string, progress func(Progress)) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	var total uint64
	for _, file := range reader.File {
		total += file.UncompressedSize64
	}
	if total > maxUnpacked {
		return errors.New("archive unpacks too large")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var written int64
	last := time.Time{}
	for _, file := range reader.File {
		path, err := entryPath(dest, file.Name)
		if err != nil {
			return err
		}
		mode := file.Mode()
		switch {
		case mode.IsDir():
			err = os.MkdirAll(path, 0o755)
		case mode&fs.ModeSymlink != 0:
			err = zipLink(dest, path, file)
		case mode.IsRegular():
			var body io.ReadCloser
			if body, err = file.Open(); err == nil {
				err = writeEntry(ctx, path, mode, body, &written)
				body.Close()
			}
		default:
			err = fmt.Errorf("archive entry %q: unsupported type", file.Name)
		}
		if err != nil {
			return err
		}
		if now := time.Now(); now.Sub(last) > 200*time.Millisecond {
			last = now
			progress(Progress{Phase: Extracting, Done: written, Total: int64(total)})
		}
	}
	return nil
}

func zipLink(dest, path string, file *zip.File) error {
	body, err := file.Open()
	if err != nil {
		return err
	}
	defer body.Close()
	target, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return err
	}
	return makeLink(dest, path, string(target))
}

func makeLink(dest, path, target string) error {
	if !linkInside(dest, path, target) {
		return fmt.Errorf("archive link %q leaves the folder", target)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Symlink(filepath.FromSlash(target), path)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	return n, err
}

func untarXZ(ctx context.Context, archive, dest string, progress func(Progress)) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	counter := &countingReader{r: file}
	decompressed, err := xz.NewReader(bufio.NewReaderSize(counter, 1<<20))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	reader := tar.NewReader(decompressed)
	var written int64
	last := time.Time{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		path, err := entryPath(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path, 0o755)
		case tar.TypeReg:
			err = writeEntry(ctx, path, header.FileInfo().Mode(), reader, &written)
		case tar.TypeSymlink:
			err = makeLink(dest, path, header.Linkname)
		default:
			err = fmt.Errorf("archive entry %q: unsupported type", header.Name)
		}
		if err != nil {
			return err
		}
		if now := time.Now(); now.Sub(last) > 200*time.Millisecond {
			last = now
			progress(Progress{Phase: Extracting, Done: counter.n, Total: info.Size()})
		}
	}
}

// exeCandidates are where each kernel's executable may be in app/, as
// glob patterns, first match wins.
func exeCandidates(k Kind, goos string) []string {
	switch k {
	case Chrome:
		switch goos {
		case "windows":
			return []string{"chrome-*/chrome.exe"}
		case "darwin":
			return []string{"chrome-*/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"}
		}
		return []string{"chrome-*/chrome"}
	case Firefox:
		switch goos {
		case "windows":
			return []string{"firefox.exe"}
		case "darwin":
			return []string{"Firefox.app/Contents/MacOS/firefox"}
		}
		return []string{"firefox/firefox"}
	case Camoufox:
		switch goos {
		case "windows":
			return []string{"camoufox.exe", "*/camoufox.exe"}
		case "darwin":
			return []string{"Camoufox.app/Contents/MacOS/camoufox", "*/Camoufox.app/Contents/MacOS/camoufox"}
		}
		return []string{"camoufox-bin", "camoufox", "*/camoufox-bin", "*/camoufox"}
	}
	return nil
}

// locate finds the kernel's executable in app, as a slash path relative
// to it.
func (m *Manager) locate(k Kind, app string) (string, error) {
	for _, pattern := range exeCandidates(k, m.GOOS) {
		matches, _ := filepath.Glob(filepath.Join(app, filepath.FromSlash(pattern)))
		for _, match := range matches {
			if executable(match) {
				rel, err := filepath.Rel(app, match)
				if err == nil && filepath.IsLocal(rel) {
					return filepath.ToSlash(rel), nil
				}
			}
		}
	}
	return "", fmt.Errorf("%s: browser executable not found in the download", k.Name())
}

func dirSize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}
