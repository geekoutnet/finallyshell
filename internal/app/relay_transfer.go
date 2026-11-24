package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

var (
	errRelaySize    = errors.New("file size changed or did not match")
	errRelayType    = errors.New("symbolic links and special files are not supported")
	errRelayChanged = errors.New("destination changed since the confirmation")
	errRelayDirect  = errors.New("direct SSH public-key authentication or host-key verification failed")
	errRelayTar     = errors.New("the source tar command failed")
	errRelayOverlap = errors.New("the destination overlaps the source on the same server")
)

type relayFS interface {
	Lstat(string) (fs.FileInfo, error)
	ReadDirContext(context.Context, string) ([]fs.FileInfo, error)
	Mkdir(string) error
	Chmod(string, fs.FileMode) error
	Chtimes(string, time.Time, time.Time) error
	Rename(string, string) error
	RealPath(string) (string, error)
	read(string) (io.ReadCloser, error)
	write(string) (io.WriteCloser, error)
}

type relaySFTP struct{ *sftp.Client }

func (client relaySFTP) read(name string) (io.ReadCloser, error) { return client.Open(name) }

func (client relaySFTP) write(name string) (io.WriteCloser, error) {
	return client.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
}

type relayEntry struct {
	name string
	info fs.FileInfo
}

type relayManifest struct {
	entries []relayEntry
	bytes   int64
	files   int
}

func relayInventory(ctx context.Context, source relayFS, dir string, names []string) (relayManifest, error) {
	var manifest relayManifest
	var walk func(string) error
	walk = func(name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		full := path.Join(dir, name)
		info, err := source.Lstat(full)
		if err != nil {
			return relayFileError(name, err)
		}
		manifest.entries = append(manifest.entries, relayEntry{name, info})
		if info.Mode().IsRegular() {
			if info.Size() < 0 || manifest.bytes > (1<<63-1)-info.Size() {
				return relayFileError(name, errRelaySize)
			}
			manifest.bytes += info.Size()
			manifest.files++
			return nil
		}
		if !info.IsDir() {
			return relayFileError(name, errRelayType)
		}
		children, err := source.ReadDirContext(ctx, full)
		if err != nil {
			return relayFileError(name, err)
		}
		for _, child := range children {
			if !relayBasename(child.Name()) {
				return errRelayPath
			}
			if err := walk(path.Join(name, child.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !relayDirectory(dir) {
		return manifest, errRelayPath
	}
	for _, name := range names {
		if !relayBasename(name) {
			return manifest, errRelayPath
		}
		if err := walk(name); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func relayFileError(name string, err error) error { return fmt.Errorf("%s: %w", name, err) }

func relayCopyFile(ctx context.Context, src, dst relayFS, from, to string, size int64, progress func(int64)) error {
	reader, err := src.read(from)
	if err != nil {
		return err
	}
	defer reader.Close()
	writer, err := dst.write(to)
	if err != nil {
		return err
	}
	err = relayStream(ctx, writer, reader, size, progress)
	return errors.Join(err, writer.Close())
}

type relayProgress struct {
	bytes, total      int64
	files, totalFiles int
	current           string
	mode              relayMode
}

type relayTask struct {
	from, to string
	items    []relayItem
	existing map[string]fs.FileInfo
	mode     relayMode
	endpoint relayEndpoint
	run      relayRunner
	samePeer bool
}

func (task relayTask) move(ctx context.Context, src, dst relayFS, remove func(string) error, report func(relayProgress)) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !relayDirectory(task.from) || !relayDirectory(task.to) || len(task.items) == 0 {
		return errRelayPath
	}
	if real, err := dst.RealPath(task.to); err != nil || real != task.to {
		return errors.Join(errRelayChanged, err)
	}
	names := make([]string, len(task.items))
	for index, item := range task.items {
		if !relayBasename(item.source) || !relayBasename(item.dest) {
			return errRelayPath
		}
		names[index] = item.source
	}
	manifest, err := relayInventory(ctx, src, task.from, names)
	if err != nil {
		return err
	}
	if task.samePeer {
		canonical, err := src.RealPath(task.from)
		if err != nil {
			return err
		}
		if relayOverlaps(canonical, task.to, task.items) {
			return errRelayOverlap
		}
	}
	p := relayProgress{total: manifest.bytes, totalFiles: manifest.files, mode: relayViaApp}
	if task.mode != relayViaApp {
		if relayCanDirect(ctx, task.run, task.endpoint) {
			p.mode = relayDirect
		} else if task.mode == relayDirect {
			return errRelayDirect
		}
	}
	report(p)
	if err := ctx.Err(); err != nil {
		return err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	staging := path.Join(task.to, fmt.Sprintf("%s%x", relayStagePrefix, token))
	if err := dst.Mkdir(staging); err != nil {
		return err
	}
	defer func() {
		if cleanupErr := remove(staging); cleanupErr != nil {
			err = errors.Join(err, relayFileError(staging, cleanupErr))
		}
	}()
	if err := dst.Chmod(staging, 0o700); err != nil {
		return err
	}
	if p.mode == relayDirect {
		script, err := relayPipeline(task.from, names, staging, task.endpoint)
		if err != nil {
			return err
		}
		if err := relayRunDirect(ctx, task.run, script, dst, staging, manifest, p, report); err != nil {
			return err
		}
	} else {
		if err := relayCopyTree(ctx, src, dst, task.from, staging, manifest, &p, report); err != nil {
			return err
		}
	}
	if err := relayVerify(ctx, dst, staging, names, manifest); err != nil {
		return err
	}
	for index := len(manifest.entries) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := manifest.entries[index]
		name := path.Join(staging, entry.name)
		if err := dst.Chtimes(name, entry.info.ModTime(), entry.info.ModTime()); err != nil {
			return relayFileError(entry.name, err)
		}
		if err := dst.Chmod(name, entry.info.Mode().Perm()); err != nil {
			return relayFileError(entry.name, err)
		}
	}
	p.bytes, p.files, p.current = p.total, p.totalFiles, ""
	report(p)
	if real, err := dst.RealPath(task.to); err != nil || real != task.to {
		return errors.Join(errRelayChanged, err)
	}
	for _, item := range task.items {
		if err := task.checkTarget(dst, item); err != nil {
			return relayFileError(item.dest, err)
		}
	}
	for _, item := range task.items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := task.publish(dst, staging, item); err != nil {
			return relayFileError(item.dest, err)
		}
	}
	return nil
}

func relayOverlaps(from, to string, items []relayItem) bool {
	within := func(parent, child string) bool {
		return child == parent || strings.HasPrefix(child, strings.TrimSuffix(parent, "/")+"/")
	}
	for _, input := range items {
		source := path.Join(from, input.source)
		if within(source, to) {
			return true
		}
		for _, output := range items {
			if within(path.Join(to, output.dest), source) {
				return true
			}
		}
	}
	return false
}

func relayCopyTree(ctx context.Context, src, dst relayFS, from, staging string, manifest relayManifest, p *relayProgress, report func(relayProgress)) error {
	for _, entry := range manifest.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := path.Join(staging, entry.name)
		if entry.info.IsDir() {
			if err := dst.Mkdir(target); err != nil {
				return relayFileError(entry.name, err)
			}
			continue
		}
		p.current = entry.name
		err := relayCopyFile(ctx, src, dst, path.Join(from, entry.name), target, entry.info.Size(), func(count int64) {
			p.bytes += count
			report(*p)
		})
		if err != nil {
			return relayFileError(entry.name, err)
		}
		p.files++
		report(*p)
	}
	return nil
}

func relayVerify(ctx context.Context, dst relayFS, staging string, names []string, source relayManifest) error {
	verified, err := relayInventory(ctx, dst, staging, names)
	if err != nil {
		return err
	}
	if len(verified.entries) != len(source.entries) {
		return errRelaySize
	}
	byName := make(map[string]fs.FileInfo, len(verified.entries))
	for _, actual := range verified.entries {
		byName[actual.name] = actual.info
	}
	for _, expected := range source.entries {
		info := byName[expected.name]
		if info == nil || info.IsDir() != expected.info.IsDir() || !info.IsDir() && info.Size() != expected.info.Size() {
			return relayFileError(expected.name, errRelaySize)
		}
	}
	return nil
}

func relayRunDirect(ctx context.Context, run relayRunner, script string, dst relayFS, stage string, manifest relayManifest, p relayProgress, report func(relayProgress)) error {
	result := make(chan error, 1)
	go func() {
		_, stderr, err := run(ctx, script)
		if strings.Contains(stderr, relayTarFailed) {
			err = errors.Join(err, errRelayTar)
		}
		if err != nil && stderr != "" {
			stderr = strings.TrimSpace(strings.ReplaceAll(stderr, relayTarFailed, ""))
			if stderr != "" && ctx.Err() == nil {
				err = fmt.Errorf("%w: %s", err, stderr)
			}
		}
		result <- err
	}()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	seen := make(map[string]int64)
	var next int
	for {
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			select {
			case <-result:
			case <-time.After(time.Second):
			}
			return ctx.Err()
		case <-tick.C:
			for range min(32, len(manifest.entries)) {
				if ctx.Err() != nil {
					break
				}
				entry := manifest.entries[next]
				next = (next + 1) % len(manifest.entries)
				if entry.info.IsDir() {
					continue
				}
				if info, err := dst.Lstat(path.Join(stage, entry.name)); err == nil && info.Mode().IsRegular() {
					previous, known := seen[entry.name]
					current := min(info.Size(), entry.info.Size())
					p.bytes += current - previous
					if current == entry.info.Size() && (!known || previous < current) {
						p.files++
					}
					seen[entry.name] = current
				}
			}
			report(p)
		}
	}
}

func (task relayTask) checkTarget(dst relayFS, item relayItem) error {
	target := path.Join(task.to, item.dest)
	info, err := dst.Lstat(target)
	if !item.backup {
		if !errors.Is(err, fs.ErrNotExist) {
			return errors.Join(errRelayChanged, err)
		}
		return nil
	}
	expected := task.existing[item.dest]
	if err != nil || expected == nil || info.Mode() != expected.Mode() || info.Size() != expected.Size() || !info.ModTime().Equal(expected.ModTime()) {
		return errors.Join(errRelayChanged, err)
	}
	return nil
}

func (task relayTask) publish(dst relayFS, stage string, item relayItem) error {
	target := path.Join(task.to, item.dest)
	if err := task.checkTarget(dst, item); err != nil {
		return err
	}
	if !item.backup {
		return dst.Rename(path.Join(stage, item.source), target)
	}
	backup := target + ".bak-" + time.Now().UTC().Format("20060102150405")
	if _, err := dst.Lstat(backup); !errors.Is(err, fs.ErrNotExist) {
		return errors.Join(errRelayChanged, err)
	}
	if err := dst.Rename(target, backup); err != nil {
		return err
	}
	if err := dst.Rename(path.Join(stage, item.source), target); err != nil {
		return errors.Join(err, dst.Rename(backup, target))
	}
	return nil
}
