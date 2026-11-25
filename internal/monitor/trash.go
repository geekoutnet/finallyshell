package monitor

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type TrashRunner func(context.Context, string) (string, string, error)

func TrashSSHRunner(client *ssh.Client) TrashRunner {
	return func(ctx context.Context, script string) (string, string, error) {
		if client == nil {
			return "", "", errors.New("not connected")
		}
		ready := "trash-" + rand.Text()
		command := "printf '%s\\n' " + privilegeQuote(ready) + "; exec sh '-s'"
		request := PrivilegeRequest{Command: "sh '-c' " + privilegeQuote(command), ready: ready, script: strings.NewReader(script), interrupt: true}
		result, err := runPrivilegeSSH(ctx, client, request)
		return result.Stdout, result.Stderr, err
	}
}

type TrashItem struct {
	Store, Mount, Name, Original, Deleted, Mode, Owner, Note string
	Size                                                     int64
	Directory, Root                                          bool
	stamp                                                    string
	sizeKnown                                                bool
}

type TrashInventory struct {
	Items    []TrashItem
	Orphans  []string
	Warnings []string
}

var (
	ErrTrashPath       = errors.New("unsafe trash path")
	ErrTrashPermission = errors.New("permission denied moving to trash")
	ErrTrashChanged    = errors.New("trash item or destination changed")
)

type TrashConflict struct{ Stamp string }

func (*TrashConflict) Error() string { return "restore destination already exists" }

type TrashRestoreMode string

const (
	TrashCheck    TrashRestoreMode = "check"
	TrashReplace  TrashRestoreMode = "replace"
	TrashKeepBoth TrashRestoreMode = "keep"
)

func ValidTrashPath(name string) bool {
	if !trashAbsolute(name) || name == "/" {
		return false
	}
	parts := strings.Split(name[1:], "/")
	if parts[0] == "proc" || parts[0] == "sys" || parts[0] == "dev" {
		return false
	}
	for _, part := range parts {
		if part == "Trash" || part == ".Trash" || strings.HasPrefix(part, ".Trash-") {
			return false
		}
	}
	return true
}

func trashAbsolute(name string) bool {
	return path.IsAbs(name) && path.Clean(name) == name && !strings.ContainsAny(name, "\x00\r\n")
}

func trashName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00\r\n")
}

func trashEncode(text string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, char := range []byte(text) {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~/", rune(char)) {
			out.WriteByte(char)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[char>>4])
			out.WriteByte(hex[char&15])
		}
	}
	return out.String()
}

func parseTrashInfo(text, mount, base string) (item TrashItem) {
	inGroup := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "[") {
			inGroup = strings.TrimSpace(line) == "[Trash Info]"
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !inGroup || !found {
			continue
		}
		switch key {
		case "Path":
			original, err := url.PathUnescape(value)
			if err != nil || original == "" {
				continue
			}
			if !path.IsAbs(original) && base != "" && trashAbsolute("/"+original) {
				original = strings.TrimSuffix(base, "/") + "/" + original
			}
			if ValidTrashPath(original) && (mount == "" || strings.HasPrefix(original, strings.TrimSuffix(mount, "/")+"/")) {
				item.Original = original
			}
		case "DeletionDate":
			if _, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
				item.Deleted = value
			}
		case "X-Finallyshell-Mode":
			item.Mode = value
		case "X-Finallyshell-Owner":
			item.Owner = value
		case "X-Finallyshell-Size":
			item.Size, item.sizeKnown = parseTrashBytes(value)
		case "X-Finallyshell-Note":
			item.Note, _ = url.PathUnescape(value)
		}
	}
	return item
}

func parseTrashBytes(text string) (int64, bool) {
	count, err := strconv.ParseInt(text, 10, 64)
	if err != nil || count < 0 {
		return 0, false
	}
	return count, true
}

func trashRun(ctx context.Context, run TrashRunner, root bool, script string) (string, error) {
	if run == nil {
		return "", errors.New("trash runner is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	rootFlag := "0"
	if root {
		rootFlag = "1"
	}
	out, stderr, err := run(ctx, "root_trash="+privilegeQuote(rootFlag)+"\n"+trashShell+"\n"+script+"\n")
	out = strings.ReplaceAll(out, "\r\n", "\n")
	if err == nil {
		return out, nil
	}
	if detail := strings.TrimSpace(stderr); detail != "" {
		return out, fmt.Errorf("%s: %w", detail, err)
	}
	return out, err
}

func TrashPut(ctx context.Context, run TrashRunner, original, note string, root bool) (TrashItem, error) {
	if !ValidTrashPath(original) {
		return TrashItem{}, ErrTrashPath
	}
	if len(note) > 16<<10 {
		return TrashItem{}, errors.New("trash note is too long")
	}
	out, err := trashRun(ctx, run, root, "put "+privilegeQuote(original)+" "+privilegeQuote(trashEncode(original))+" "+privilegeQuote(trashEncode(note)))
	if out == "permission\n" && err != nil {
		return TrashItem{}, fmt.Errorf("%w: %w", ErrTrashPermission, err)
	}
	if out == "path\n" && err != nil {
		return TrashItem{}, fmt.Errorf("%w: %w", ErrTrashPath, err)
	}
	if out == "changed\n" && err != nil {
		return TrashItem{}, fmt.Errorf("%w: %w", ErrTrashChanged, err)
	}
	fields := strings.Split(out, "\x00")
	if len(fields) != 4 || !trashAbsolute(fields[0]) || (fields[1] != "" && !trashAbsolute(fields[1])) || !trashName(fields[2]) || fields[3] != "" {
		if err == nil {
			err = errors.New("invalid trash move response")
		}
		return TrashItem{}, err
	}
	return TrashItem{Store: fields[0], Mount: fields[1], Name: fields[2], Original: original, Note: note, Root: root}, err
}

func TrashList(ctx context.Context, run TrashRunner, root bool) (TrashInventory, error) {
	out, err := trashRun(ctx, run, root, trashListShell)
	return parseTrashList(out, root, err)
}

func parseTrashList(out string, root bool, err error) (inventory TrashInventory, result error) {
	if out == "" && err != nil {
		return inventory, err
	}
	seen := make(map[[2]string]bool)
	fields := strings.Split(out, "\x00")
	for len(fields) >= 2 {
		switch fields[0] {
		case "end":
			if len(fields) == 2 && fields[1] == "" {
				return inventory, err
			}
			return inventory, errors.Join(err, errors.New("invalid trash listing terminator"))
		case "orphan":
			inventory.Orphans = append(inventory.Orphans, fields[1])
			fields = fields[2:]
		case "warning":
			inventory.Warnings = append(inventory.Warnings, fields[1])
			fields = fields[2:]
		case "item":
			if len(fields) < 11 || !trashAbsolute(fields[1]) || (fields[2] != "" && !trashAbsolute(fields[2])) || !trashName(fields[3]) || fields[9] == "" {
				return inventory, errors.Join(err, errors.New("invalid trash item response"))
			}
			key := [2]string{fields[1], fields[3]}
			if seen[key] {
				fields = fields[10:]
				continue
			}
			seen[key] = true
			base := fields[2]
			if base == "" {
				base = path.Dir(fields[1])
			}
			item := parseTrashInfo(fields[4], fields[2], base)
			item.Store, item.Mount, item.Name, item.Root = fields[1], fields[2], fields[3], root
			item.Directory, item.stamp = fields[5] == "d", fields[9]
			if item.Mode == "" {
				item.Mode = fields[6]
			}
			if item.Owner == "" {
				item.Owner = fields[7]
			}
			if size, known := parseTrashBytes(fields[8]); known {
				item.Size, item.sizeKnown = size, true
			}
			inventory.Items = append(inventory.Items, item)
			fields = fields[10:]
		default:
			return inventory, errors.Join(err, errors.New("invalid trash listing response"))
		}
	}
	return inventory, errors.Join(err, errors.New("incomplete trash listing"))
}

func trashItemCommand(item TrashItem) (string, error) {
	if !trashAbsolute(item.Store) || !trashName(item.Name) || item.stamp == "" {
		return "", ErrTrashPath
	}
	return privilegeQuote(item.Store) + " " + privilegeQuote(item.Name) + " " + privilegeQuote(item.stamp), nil
}

func TrashRestore(ctx context.Context, run TrashRunner, item TrashItem, mode TrashRestoreMode, conflictStamp string) (string, error) {
	result, err := TrashRestoreWithNote(ctx, run, item, mode, conflictStamp, "")
	return result.Destination, err
}

type TrashRestoreResult struct{ Destination, Displaced string }

func TrashRestoreWithNote(ctx context.Context, run TrashRunner, item TrashItem, mode TrashRestoreMode, conflictStamp, replacementNote string) (result TrashRestoreResult, err error) {
	args, err := trashItemCommand(item)
	if err != nil || !ValidTrashPath(item.Original) || mode != TrashCheck && mode != TrashReplace && mode != TrashKeepBoth {
		return result, ErrTrashPath
	}
	script := "restore " + args + " " + privilegeQuote(item.Original) + " " + privilegeQuote(string(mode)) + " " + privilegeQuote(conflictStamp)
	if mode == TrashReplace && replacementNote != "" {
		if len(replacementNote) > 16<<10 {
			return result, errors.New("trash note is too long")
		}
		script += " " + privilegeQuote(trashEncode(replacementNote))
	}
	out, err := trashRun(ctx, run, item.Root, script)
	if displaced, found := strings.CutPrefix(out, "displaced\x00"); found {
		var terminated bool
		result.Displaced, out, terminated = strings.Cut(displaced, "\x00")
		if !terminated || !trashAbsolute(result.Displaced) {
			return TrashRestoreResult{}, errors.Join(err, errors.New("invalid trash replacement response"))
		}
	}
	if stamp, found := strings.CutPrefix(out, "conflict\x00"); found && err == nil {
		stamp, terminated := strings.CutSuffix(stamp, "\x00")
		if !terminated || stamp == "" || strings.ContainsAny(stamp, "\x00\r\n") {
			return result, errors.New("invalid trash conflict response")
		}
		return result, &TrashConflict{Stamp: stamp}
	}
	if out == "changed\n" && err != nil {
		return result, fmt.Errorf("%w: %w", ErrTrashChanged, err)
	}
	if destination, terminated := strings.CutSuffix(out, "\x00"); terminated && ValidTrashPath(destination) {
		result.Destination = destination
	} else if err == nil {
		err = errors.New("invalid trash restore response")
	}
	return result, err
}

func TrashDelete(ctx context.Context, run TrashRunner, item TrashItem) error {
	args, err := trashItemCommand(item)
	if err != nil {
		return err
	}
	out, err := trashRun(ctx, run, item.Root, "purge "+args)
	if out == "changed\n" && err != nil {
		return fmt.Errorf("%w: %w", ErrTrashChanged, err)
	}
	return err
}

func TrashEmpty(ctx context.Context, run TrashRunner, items []TrashItem) error {
	var failures []error
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := TrashDelete(ctx, run, item); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.Name, err))
		}
	}
	return errors.Join(failures...)
}

func TrashSize(items []TrashItem) (total int64) {
	for _, item := range items {
		if item.Size > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += item.Size
	}
	return total
}
