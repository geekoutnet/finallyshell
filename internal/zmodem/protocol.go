package zmodem

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxRetries = 10

type Progress struct {
	Name        string
	Done, Total int64
	Path        string
}

type Transfer struct {
	reader *bufio.Reader
	writer io.Writer
	start  header
	ctx    context.Context
}

type uploadFile struct {
	file *os.File
	info os.FileInfo
	name string
}

func (transfer *Transfer) Send(paths []string, report func(Progress)) error {
	if transfer.start.kind != zrinit || len(paths) == 0 {
		return errFrame
	}
	files := make([]uploadFile, 0, len(paths))
	defer func() {
		for _, source := range files {
			source.file.Close()
		}
	}()
	for _, path := range paths {
		info, err := os.Lstat(path)
		name := filepath.Base(path)
		if err != nil || !safeName(name) || !info.Mode().IsRegular() || info.Size() > math.MaxUint32 {
			return errors.New("ZMODEM requires regular files under 4 GiB with safe names")
		}
		file, err := os.Open(path)
		if err != nil {
			return errors.New("cannot open ZMODEM source file")
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || info.Size() != opened.Size() {
			file.Close()
			return errors.New("ZMODEM source file changed")
		}
		files = append(files, uploadFile{file, info, name})
	}
	ready := transfer.start
	var skipped bool
	for _, source := range files {
		var err error
		ready, err = transfer.sendFile(source, ready, report)
		if err != nil {
			return err
		}
		skipped = skipped || ready.kind == zskip
	}
	if err := transfer.writeHeader(zfin, 0, hexHeader); err != nil {
		return err
	}
	if _, err := transfer.expect(zfin); err != nil {
		return err
	}
	if err := transfer.write([]byte("OO")); err != nil {
		return err
	}
	if skipped {
		return errSkipped
	}
	return nil
}

var errSkipped = errors.New("the remote receiver skipped a file")

func (transfer *Transfer) sendFile(source uploadFile, ready header, report func(Progress)) (header, error) {
	format := byte(bin16)
	if ready.value>>24&0x20 != 0 {
		format = bin32
	}
	metadata := fmt.Appendf(nil, "%s\x00%d %o 0 0 0 0\x00", source.name, source.info.Size(), max(0, source.info.ModTime().Unix()))
	if err := transfer.writeHeader(zfile, 1<<24|7<<16, format); err != nil {
		return header{}, err
	}
	if err := transfer.write(encodeData(metadata, zcrcw, format)); err != nil {
		return header{}, err
	}
	response, err := transfer.expect(zrpos, zskip)
	if err != nil || response.kind == zskip {
		return response, err
	}
	return transfer.sendContents(source, response.value, format, report)
}

func (transfer *Transfer) sendContents(source uploadFile, offset uint32, format byte, report func(Progress)) (header, error) {
	size := uint32(source.info.Size())
	buf := make([]byte, 1024)
	var retries int
	for {
		if offset > size || retries >= maxRetries {
			return header{}, errFrame
		}
		if report != nil {
			report(Progress{Name: source.name, Done: int64(offset), Total: int64(size)})
		}
		if offset == size {
			if err := transfer.writeHeader(zeof, offset, format); err != nil {
				return header{}, err
			}
			response, err := transfer.expect(zrinit, zrpos, zskip)
			if err != nil || response.kind != zrpos {
				return response, err
			}
			offset, retries = response.value, retries+1
			continue
		}
		length := min(uint32(len(buf)), size-offset)
		if _, err := source.file.ReadAt(buf[:length], int64(offset)); err != nil {
			return header{}, err
		}
		if err := transfer.writeHeader(zdata, offset, format); err != nil {
			return header{}, err
		}
		if err := transfer.write(encodeData(buf[:length], zcrcw, format)); err != nil {
			return header{}, err
		}
		response, err := transfer.expect(zack, zrpos, zskip, znak)
		if err != nil || response.kind == zskip {
			return response, err
		}
		switch {
		case response.kind == zack && response.value == offset+length:
			offset, retries = response.value, 0
		case response.kind == zrpos:
			offset, retries = response.value, retries+1
		default:
			retries++
		}
	}
}

func (transfer *Transfer) Receive(dir string, accept func(string, int64) bool, report func(Progress)) error {
	if transfer.start.kind != zrqinit {
		return errFrame
	}
	if err := transfer.writeHeader(zrinit, 0x23000000, hexHeader); err != nil {
		return err
	}
	for {
		frame, err := transfer.expect(zfile, zsinit, zchallenge, zrqinit, zfin)
		if err != nil {
			return err
		}
		switch frame.kind {
		case zrqinit:
			err = transfer.writeHeader(zrinit, 0x23000000, hexHeader)
		case zchallenge:
			err = transfer.writeHeader(zack, frame.value, hexHeader)
		case zsinit:
			if _, _, err = readData(transfer.reader, frame.format); err == nil {
				err = transfer.writeHeader(zack, 0, hexHeader)
			}
		case zfile:
			if err = transfer.receiveFile(dir, frame.format, accept, report); err == nil {
				err = transfer.writeHeader(zrinit, 0x23000000, hexHeader)
			}
		case zfin:
			if err = transfer.writeHeader(zfin, 0, hexHeader); err != nil {
				return err
			}
			for range 2 {
				value, err := readNonFlow(transfer.reader)
				if err != nil {
					return err
				}
				if value != 'O' {
					transfer.reader.UnreadByte()
					return errFrame
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (transfer *Transfer) receiveFile(dir string, format byte, accept func(string, int64) bool, report func(Progress)) error {
	metadata, _, err := readData(transfer.reader, format)
	if err != nil {
		return err
	}
	name, size, modified, err := parseMetadata(metadata)
	if err != nil {
		return err
	}
	if accept != nil && !accept(name, int64(size)) {
		return context.Canceled
	}
	if transfer.ctx != nil && transfer.ctx.Err() != nil {
		return transfer.ctx.Err()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".zmodem-*.part")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	defer file.Close()
	progress := Progress{Name: name, Total: int64(size)}
	if report != nil {
		report(progress)
	}
	if err := transfer.receiveContents(file, size, func(offset uint32) {
		progress.Done = int64(offset)
		if report != nil {
			report(progress)
		}
	}); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if transfer.ctx != nil && transfer.ctx.Err() != nil {
		return transfer.ctx.Err()
	}
	if !modified.IsZero() {
		_ = os.Chtimes(temp, modified, modified)
	}
	progress.Path, err = publish(temp, dir, name)
	if err == nil && report != nil {
		report(progress)
	}
	return err
}

func (transfer *Transfer) receiveContents(file *os.File, size uint32, report func(uint32)) error {
	var offset uint32
	if err := transfer.writeHeader(zrpos, offset, hexHeader); err != nil {
		return err
	}
	for retries := 0; retries < maxRetries; {
		frame, err := transfer.expect(zdata, zeof)
		if err != nil {
			return err
		}
		if frame.kind == zeof && frame.value == size && offset == size {
			return nil
		}
		if frame.kind == zdata && frame.value == offset {
			before := offset
			offset, err = transfer.receivePackets(file, size, offset, frame.format, report)
			if err == nil {
				retries = 0
				continue
			}
			if !recoverable(err) {
				return err
			}
			if offset > before {
				retries = 0
			}
		}
		retries++
		if err := transfer.writeHeader(zrpos, offset, hexHeader); err != nil {
			return err
		}
	}
	return errChecksum
}

func (transfer *Transfer) receivePackets(file *os.File, size, offset uint32, format byte, report func(uint32)) (uint32, error) {
	for {
		packet, end, err := readData(transfer.reader, format)
		if err != nil {
			return offset, err
		}
		if uint32(len(packet)) > size-offset {
			return offset, errors.New("ZMODEM exceeded the announced file size")
		}
		if _, err := file.Write(packet); err != nil {
			return offset, err
		}
		offset += uint32(len(packet))
		report(offset)
		if end == zcrcq || end == zcrcw {
			if err := transfer.writeHeader(zack, offset, hexHeader); err != nil {
				return offset, err
			}
		}
		if end == zcrce || end == zcrcw {
			return offset, nil
		}
	}
}

func parseMetadata(payload []byte) (string, uint32, time.Time, error) {
	name, info, found := bytes.Cut(payload, []byte{0})
	if !found || !safeName(string(name)) {
		return "", 0, time.Time{}, errors.New("unsafe ZMODEM file name")
	}
	info, _, _ = bytes.Cut(info, []byte{0})
	fields := strings.Fields(string(info))
	if len(fields) == 0 {
		return "", 0, time.Time{}, errFrame
	}
	size, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return "", 0, time.Time{}, errors.New("invalid ZMODEM file size")
	}
	var modified time.Time
	if len(fields) > 1 {
		if seconds, err := strconv.ParseInt(fields[1], 8, 64); err == nil && seconds > 0 && seconds <= 253402300799 {
			modified = time.Unix(seconds, 0)
		}
	}
	return string(name), uint32(size), modified, nil
}

func safeName(name string) bool {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || len(name) > 240 ||
		strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") || strings.ContainsAny(name, `/\:<>"|?*`) ||
		strings.IndexFunc(name, func(char rune) bool { return unicode.IsControl(char) || unicode.In(char, unicode.Cf) }) >= 0 {
		return false
	}
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	return !((strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
		strings.Contains("\x001\x002\x003\x004\x005\x006\x007\x008\x009\x00¹\x00²\x00³\x00", "\x00"+base[3:]+"\x00"))
}

func publish(temp, dir, name string) (string, error) {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for index := 0; index < 10000; index++ {
		chosen := name
		if index > 0 {
			chosen = fmt.Sprintf("%s (%d)%s", stem, index, extension)
		}
		path := filepath.Join(dir, chosen)
		if err := moveNoReplace(temp, path); !errors.Is(err, os.ErrExist) {
			return path, err
		}
	}
	return "", os.ErrExist
}

func recoverable(err error) bool { return errors.Is(err, errChecksum) || errors.Is(err, errFrame) }

func (transfer *Transfer) expect(kinds ...byte) (header, error) {
	for range maxRetries {
		frame, err := readHeader(transfer.reader)
		if recoverable(err) {
			continue
		}
		if err != nil {
			return header{}, err
		}
		if frame.kind == zabort || frame.kind == zferr {
			return header{}, ErrCanceled
		}
		for _, kind := range kinds {
			if frame.kind == kind {
				return frame, nil
			}
		}
	}
	return header{}, errFrame
}

func (transfer *Transfer) writeHeader(kind byte, value uint32, format byte) error {
	return transfer.write(encodeHeader(header{kind: kind, value: value, format: format}))
}

func (transfer *Transfer) write(payload []byte) error {
	if transfer.ctx != nil && transfer.ctx.Err() != nil {
		return transfer.ctx.Err()
	}
	count, err := transfer.writer.Write(payload)
	if err == nil && count != len(payload) {
		err = io.ErrShortWrite
	}
	return err
}

func SourceName(path string) (string, bool) {
	name := filepath.Base(path)
	return name, safeName(name)
}

func startValue(frame []byte) (header, bool) {
	if len(frame) != 20 || frame[18]&0x7f != '\r' || frame[19]&0x7f != '\n' {
		return header{}, false
	}
	var payload [7]byte
	for index := range payload {
		hi, low := hexDigit(frame[4+index*2]), hexDigit(frame[5+index*2])
		if hi > 15 || low > 15 {
			return header{}, false
		}
		payload[index] = hi<<4 | low
	}
	if payload[0] > zrinit || !validChecksum(payload[:], 5, hexHeader) {
		return header{}, false
	}
	return header{kind: payload[0], value: binary.LittleEndian.Uint32(payload[1:5]), format: hexHeader}, true
}

func hexDigit(value byte) byte {
	switch {
	case value >= '0' && value <= '9':
		return value - '0'
	case value|32 >= 'a' && value|32 <= 'f':
		return (value | 32) - 'a' + 10
	default:
		return 255
	}
}
