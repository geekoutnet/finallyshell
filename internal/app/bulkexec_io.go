package app

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"finallyshell/internal/config"
	"finallyshell/locales"
)

const bulkHistoryLimit = 20

type bulkHistoryEntry struct {
	Time    time.Time          `json:"time"`
	IDs     []string           `json:"targets"`
	Script  string             `json:"script,omitempty"`
	Summary map[bulkStatus]int `json:"summary"`
	Timeout int                `json:"timeout"`
	Workers int                `json:"concurrency"`
	Root    bool               `json:"root,omitempty"`
	Stop    bool               `json:"stop,omitempty"`
}

func bulkHistoryDir() string { return filepath.Join(config.Dir(), "bulk-runs") }

func bulkHistoryNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if _, _, ok := strings.Cut(entry.Name(), "-"); ok {
			names = append(names, entry.Name())
		}
	}
	slices.Reverse(names)
	return names, nil
}

func bulkLoadHistory(dir string) ([]bulkHistoryEntry, error) {
	names, err := bulkHistoryNames(dir)
	if err != nil {
		return nil, err
	}
	var history []bulkHistoryEntry
	var readErr error
	for _, name := range names[:min(bulkHistoryLimit, len(names))] {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			readErr = err
			continue
		}
		var entry bulkHistoryEntry
		err = json.NewDecoder(io.LimitReader(file, 4<<20)).Decode(&entry)
		file.Close()
		if err != nil {
			readErr = err
			continue
		}
		history = append(history, entry)
	}
	return history, readErr
}

func bulkSaveHistory(dir string, entry bulkHistoryEntry) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, entry.Time.UTC().Format("20060102150405.000000000")+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(entry)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), strings.TrimSuffix(file.Name(), ".tmp")+".json"); err != nil {
		return err
	}
	names, err := bulkHistoryNames(dir)
	if err != nil {
		return err
	}
	for _, name := range names[min(bulkHistoryLimit, len(names)):] {
		err = errors.Join(err, os.Remove(filepath.Join(dir, name)))
	}
	return err
}

func bulkExit(result bulkResult) string {
	if result.exit < 0 {
		return ""
	}
	return strconv.Itoa(result.exit)
}

func bulkDuration(result bulkResult) string {
	if result.duration <= 0 {
		return ""
	}
	return max(time.Millisecond, result.duration.Round(time.Millisecond)).String()
}

func bulkOutput(result bulkResult) string {
	var parts []string
	if result.stdout != "" {
		parts = append(parts, locales.BulkStdout()+"\n"+result.stdout)
	}
	if result.stderr != "" {
		parts = append(parts, locales.BulkStderr()+"\n"+result.stderr)
	}
	if detail := bulkErrorText(result.err); detail != "" {
		parts = append(parts, detail)
	}
	if result.truncated {
		parts = append(parts, locales.BulkTruncated())
	}
	return strings.Join(parts, "\n\n")
}

func bulkCSV(rows []bulkResult) (string, error) {
	var out strings.Builder
	writer := csv.NewWriter(&out)
	if err := writer.Write([]string{"host", "status", "exit", "duration", "output"}); err != nil {
		return "", err
	}
	for _, result := range rows {
		output := result.stdout
		for _, part := range []string{result.stderr, bulkErrorText(result.err)} {
			if part != "" {
				if output != "" {
					output += "\n"
				}
				output += part
			}
		}
		if result.truncated {
			output += "\n" + locales.BulkTruncated()
		}
		if err := writer.Write([]string{bulkSafeCell(result.target.name), string(result.status), bulkExit(result), bulkDuration(result), bulkSafeCell(output)}); err != nil {
			return "", err
		}
	}
	writer.Flush()
	return out.String(), writer.Error()
}

func bulkSafeCell(text string) string {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	first, _ := utf8.DecodeRuneInString(trimmed)
	if strings.HasPrefix(text, "\t") || strings.HasPrefix(text, "\r") || strings.HasPrefix(text, "\n") ||
		strings.ContainsRune("=+-@＝＋－＠", first) {
		return "'" + text
	}
	return text
}

func bulkReport(rows []bulkResult) string {
	var out strings.Builder
	out.WriteString(bulkSummary(bulkCounts(rows)) + "\n")
	for _, result := range rows {
		fmt.Fprintf(&out, "\n=== %s (%s) · %s", result.target.name, result.target.endpoint, bulkStatusText(result.status))
		if exit := bulkExit(result); exit != "" {
			fmt.Fprintf(&out, " · %s %s", locales.BulkExit(), exit)
		}
		if duration := bulkDuration(result); duration != "" {
			fmt.Fprintf(&out, " · %s", duration)
		}
		out.WriteByte('\n')
		if output := bulkOutput(result); output != "" {
			out.WriteString(output + "\n")
		}
	}
	return out.String()
}

func bulkWriteExport(path, data string, expected os.FileInfo) error {
	if expected == nil {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = io.WriteString(file, data)
		if err = errors.Join(err, file.Close()); err != nil {
			os.Remove(path)
		}
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !bulkSameFile(expected, current) {
		return errors.New(locales.BulkExportChanged())
	}
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	if info, err := source.Stat(); err != nil || !bulkSameFile(expected, info) {
		source.Close()
		return errors.New(locales.BulkExportChanged())
	}
	backupPath := path + ".bak-" + time.Now().Format("20060102150405")
	backup, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		source.Close()
		return err
	}
	_, err = io.Copy(backup, source)
	if err = errors.Join(err, source.Close(), backup.Close()); err != nil {
		os.Remove(backupPath)
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".bulk-export-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = io.WriteString(file, data)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil || !bulkSameFile(expected, info) {
		return errors.New(locales.BulkExportChanged())
	}
	return os.Rename(file.Name(), path)
}

func bulkSameFile(expected, current os.FileInfo) bool {
	return current.Mode().IsRegular() && os.SameFile(expected, current) && expected.ModTime().Equal(current.ModTime()) && expected.Size() == current.Size()
}
