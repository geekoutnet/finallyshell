package monitor

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

var caddyBackupName = regexp.MustCompile(`^[0-9]{14}(?:\.[0-9]{3})?$`)

func caddyBackupRoot(inst CaddyInstall) string { return path.Join(path.Dir(inst.Config), ".backups") }

func caddyBackupAllowed(inst CaddyInstall, backup string) bool {
	return !inst.ContainerConfigUnmapped && caddyPath(backup) && path.Dir(backup) == caddyBackupRoot(inst) && caddyBackupName.MatchString(path.Base(backup))
}

const caddyBackupAccess = `safe_dir() {
  [ -d "$1" ] && [ ! -L "$1" ] && [ "$(cd -P -- "$1" && pwd -P)" = "$1" ]
}
`

// CaddyBackups lists the private timestamped transaction backup directories
// next to the main config, newest first. It does not read any file contents.
func CaddyBackups(ctx context.Context, run CaddyRunner, inst CaddyInstall) ([]string, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || inst.ContainerConfigUnmapped {
		return nil, ErrCaddyUnsafe
	}
	root := caddyBackupRoot(inst)
	script := caddyBackupAccess + "root=" + privilegeQuote(root) + `
if [ ! -e "$root" ] && [ ! -L "$root" ]; then exit '0'; fi
safe_dir "$root" || exit '1'
for entry in "$root"/*; do
  if [ -d "$entry" ] && [ ! -L "$entry" ]; then printf '%s\n' "$entry"; fi
done
`
	stdout, stderr, err := run(ctx, script, nil, nil)
	if err != nil {
		return nil, caddyCommandError(err, strings.TrimSpace(stderr))
	}
	var backups []string
	for line := range strings.SplitSeq(stdout, "\n") {
		if entry := strings.TrimSuffix(line, "\r"); caddyBackupAllowed(inst, entry) {
			backups = append(backups, entry)
		}
	}
	slices.Sort(backups)
	backups = slices.Compact(backups)
	slices.Reverse(backups)
	return backups, nil
}

// PreviewCaddyBackup reads a transaction's previous contents and current
// versions of touched paths. The returned change restores them through
// ApplyCaddyChange and refuses stale files. Files created by that original
// transaction are removed only if its backup manifest records those paths.
func PreviewCaddyBackup(ctx context.Context, run CaddyRunner, inst CaddyInstall, backup string) (CaddyChange, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || !caddyBackupAllowed(inst, backup) {
		return CaddyChange{}, ErrCaddyUnsafe
	}
	manifest := path.Join(backup, ".finallyshell-paths")
	command := caddyBackupAccess + "safe_dir " + privilegeQuote(backup) + " || exit '1'\n" +
		"if [ -f " + privilegeQuote(manifest) + " ] && [ ! -L " + privilegeQuote(manifest) + " ]; then " +
		SystemCommand("head", "-c", "524289", "--", manifest) + "; else " +
		SystemCommand("find", backup, "-type", "f", "-print") + " | " + SystemCommand("sort") + "; fi\n"
	stdout, stderr, err := run(ctx, command, nil, nil)
	if err != nil {
		return CaddyChange{}, caddyCommandError(err, strings.TrimSpace(stderr))
	}
	entries := strings.Split(strings.TrimSuffix(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n"), "\n")
	if len(stdout) > 524288 || len(entries) > caddyMaxFiles || (len(entries) == 1 && entries[0] == "") {
		return CaddyChange{}, ErrCaddyUnsafe
	}
	change := CaddyChange{Expect: map[string]string{}, Reload: inst.Active == "active"}
	seen := map[string]bool{}
	var currentTotal, backupTotal int64
	for _, entry := range entries {
		name := strings.TrimPrefix(entry, backup+"/")
		if name != entry {
			name = "/" + name
		}
		if !caddyPath(name) || seen[name] {
			return CaddyChange{}, ErrCaddyUnsafe
		}
		seen[name] = true
		old, err := caddyBackupReadOne(ctx, run, backup+name)
		if err != nil {
			return CaddyChange{}, err
		}
		current, err := caddyBackupReadOne(ctx, run, name)
		if err != nil {
			return CaddyChange{}, err
		}
		if len(current) > 0 {
			currentTotal += current[0].Size
			if currentTotal > caddyMaxTree {
				return CaddyChange{}, ErrCaddyUnsafe
			}
			change.Expect[name] = current[0].Content
		} else {
			change.ExpectMissing = append(change.ExpectMissing, name)
		}
		if len(old) > 0 {
			backupTotal += old[0].Size
			if backupTotal > caddyMaxTree {
				return CaddyChange{}, ErrCaddyUnsafe
			}
			old[0].Path = name
			change.Writes = append(change.Writes, old[0])
		} else if len(current) > 0 {
			change.Deletes = append(change.Deletes, name)
		}
	}
	if _, _, err := caddyChangePaths(inst, change); err != nil {
		return CaddyChange{}, err
	}
	return change, nil
}

func caddyBackupReadOne(ctx context.Context, run CaddyRunner, name string) ([]CaddyFile, error) {
	script, _ := caddyReadScript(name, true)
	script = caddyTransactionShell + "check_path " + privilegeQuote(name) + "\n" + script
	stdout, stderr, err := run(ctx, script, nil, nil)
	if err != nil {
		return nil, caddyCommandError(err, strings.TrimSpace(stderr))
	}
	files, err := caddyDecodeFiles(stdout)
	if err != nil || len(files) > 1 || (len(files) == 1 && files[0].Path != name) {
		return nil, ErrCaddyUnsafe
	}
	return files, nil
}

// DeleteCaddyBackup deletes one selected timestamped backup within the
// configured backup root, using the same lock as a configuration change.
// The caller must confirm the selected path before invoking it.
func DeleteCaddyBackup(ctx context.Context, run CaddyRunner, inst CaddyInstall, backup string) error {
	inst, err := caddyNormalize(inst)
	if err != nil || !caddyBackupAllowed(inst, backup) {
		return ErrCaddyUnsafe
	}
	lock := path.Join(path.Dir(inst.Config), ".finallyshell.lock")
	script := caddyBackupAccess + fmt.Sprintf(`set -eu
lock=%s
safe_dir %s
mkdir -- "$lock" || { printf 'CADDY_TX busy\n'; exit '1'; }
trap 'rmdir -- "$lock"' '0'
trap 'exit 1' HUP INT TERM
safe_dir %s
rm -rf -- %s
`, privilegeQuote(lock), privilegeQuote(caddyBackupRoot(inst)), privilegeQuote(backup), privilegeQuote(backup))
	stdout, stderr, err := run(ctx, script, nil, nil)
	if strings.Contains(stdout, "CADDY_TX busy") {
		return ErrCaddyBusy
	}
	if err != nil {
		return caddyCommandError(err, strings.TrimSpace(stderr))
	}
	return nil
}
