package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"finallyshell/internal/config"
)

const recordingDefaultMiB = 64

type recording struct {
	dir, title      string
	width, height   int
	limit, size     int
	start, partTime time.Time
	text            bool
	cast, plain     *os.File
	nextPart        int
	styles          ansiState
	plainStyles     ansiState
	pending         []byte
	esc             []byte
}

func newRecording(dir, title string, width, height, limit int, text bool, now time.Time) (*recording, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	rec := &recording{dir: dir, title: title, width: width, height: height, limit: limit, text: text, start: now}
	return rec, rec.rotate(now)
}

func (rec *recording) rotate(now time.Time) error {
	if err := rec.closeFiles(); err != nil {
		return err
	}
	header, err := json.Marshal(struct {
		Version   int    `json:"version"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Timestamp int64  `json:"timestamp"`
		Title     string `json:"title"`
	}{2, rec.width, rec.height, now.Unix(), rec.title})
	if err != nil || len(header)+64 > rec.limit {
		return errors.New("recording header exceeds size limit")
	}
	base := filepath.Join(rec.dir, rec.start.Format("2006-01-02_15-04-05"))
	for range 10000 {
		index := rec.nextPart
		rec.nextPart++
		path := base
		if index != 0 {
			path = fmt.Sprintf("%s-%03d", base, index)
		}
		cast, err := os.OpenFile(path+".cast", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		var plain *os.File
		if rec.text {
			plain, err = os.OpenFile(path+".txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				cast.Close()
				os.Remove(cast.Name())
				if errors.Is(err, os.ErrExist) {
					continue
				}
				return err
			}
		}
		rec.cast, rec.plain, rec.partTime = cast, plain, now
		rec.size, err = cast.Write(append(header, '\n'))
		if err != nil {
			_ = rec.closeFiles()
		}
		return err
	}
	return os.ErrExist
}

func (rec *recording) output(data []byte, now time.Time) error {
	data = append(rec.pending, data...)
	end := utf8Boundary(data)
	clean := make([]byte, 0, end)
	for _, value := range data[:end] {
		before := rec.styles.state
		kind := rec.styles.next(value)
		switch kind {
		case ansiEscape:
			if before == ansiGround || value == 0x1b {
				rec.esc = rec.esc[:0]
			}
			if len(rec.esc) < 512 {
				if before == ansiGround && value == 0x9b {
					rec.esc = append(rec.esc, '\x1b', '[')
				} else {
					rec.esc = append(rec.esc, value)
				}
			}
			if rec.styles.state == ansiGround {
				if len(rec.esc) < 512 {
					clean = append(clean, rec.esc...)
				}
				rec.esc = rec.esc[:0]
			}
		case ansiOpaque:
			rec.esc = rec.esc[:0]
		case ansiControl:
			if value < 0x7f {
				clean = append(clean, value)
			}
		default:
			clean = append(clean, value)
		}
	}
	rec.pending = append(rec.pending[:0], data[end:]...)
	return rec.write("o", strings.ToValidUTF8(string(clean), "�"), now)
}

func (rec *recording) write(kind, data string, now time.Time) error {
	if data == "" {
		return nil
	}
	for data != "" {
		cut := min(len(data), 16<<10)
		for cut < len(data) && !utf8.RuneStart(data[cut]) && cut > 1 {
			cut--
		}
		for {
			elapsed := max(0, now.Sub(rec.partTime).Seconds())
			line, err := json.Marshal([3]any{elapsed, kind, data[:cut]})
			if err != nil {
				return err
			}
			if rec.size+len(line)+1 <= rec.limit {
				amount, err := rec.cast.Write(append(line, '\n'))
				rec.size += amount
				if err != nil {
					return err
				}
				if rec.plain != nil && kind == "o" {
					if _, err := rec.plain.WriteString(plainTerminalText(&rec.plainStyles, data[:cut])); err != nil {
						return err
					}
				}
				break
			}
			if cut <= 8 || rec.limit-rec.size < 96 {
				if err := rec.rotate(now); err != nil {
					return err
				}
				continue
			}
			cut = utf8Boundary([]byte(data[:cut/2]))
		}
		data = data[cut:]
	}
	return nil
}

func plainTerminalText(parser *ansiState, data string) string {
	var out strings.Builder
	for index := range len(data) {
		value := data[index]
		if kind := parser.next(value); kind == ansiText || kind == ansiControl && (value == '\n' || value == '\t') {
			out.WriteByte(value)
		}
	}
	return out.String()
}

func (rec *recording) closeFiles() error {
	var cast, plain error
	if rec.cast != nil {
		cast = rec.cast.Close()
		rec.cast = nil
	}
	if rec.plain != nil {
		plain = rec.plain.Close()
		rec.plain = nil
	}
	return errors.Join(cast, plain)
}

func (rec *recording) Close() error { return rec.closeFiles() }

func recordingDir(cfg config.TerminalRecording) string {
	if cfg.Folder != "" {
		return cfg.Folder
	}
	return filepath.Join(config.Dir(), "recordings")
}

func recordingHost(title, id string) string {
	safe := func(value string) string {
		value = strings.Map(func(char rune) rune {
			if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' {
				return char
			}
			return '_'
		}, value)
		return value[:utf8Boundary([]byte(value[:min(len(value), 80)]))]
	}
	return "host-" + safe(title) + "-" + safe(id)
}
