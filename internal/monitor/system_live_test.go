package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type liveSystem struct {
	client *ssh.Client
	root   *Privilege
}

func newLiveSystem(t *testing.T) liveSystem {
	t.Helper()
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE not set")
	}
	client := dialTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	credential, _, _ := strings.Cut(os.Getenv("FINALLYSHELL_TEST_SSH"), "@")
	_, secret, _ := strings.Cut(credential, ":")
	return liveSystem{client, NewPrivilege(ctx, SSHPrivilegeRunner(client), PrivilegeCredentials{
		Login: func(context.Context) []byte { return []byte(secret) },
	})}
}

func (box liveSystem) read(ctx context.Context, script string) (string, string, error) {
	return RunScript(ctx, box.client, script)
}

func (box liveSystem) elevated(ctx context.Context, script string) (string, string, error) {
	return box.root.Run(ctx, script, PrivilegeOptions{})
}

func (box liveSystem) run(t *testing.T, root bool, input io.Reader, script string, buildErr error) string {
	t.Helper()
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	var out, detail string
	var err error
	switch {
	case root:
		out, detail, err = box.root.Run(ctx, script, PrivilegeOptions{Stdin: input})
	case input != nil:
		out, detail, err = RunSystemInput(ctx, box.client, script, input, nil)
	default:
		out, detail, err = box.read(ctx, script)
	}
	if err != nil {
		t.Fatalf("remote command failed: %v: %s", err, detail)
	}
	return out
}

func (box liveSystem) command(t *testing.T, root bool, args ...string) string {
	t.Helper()
	return box.run(t, root, nil, SystemCommand(args...), nil)
}

func (box liveSystem) cleanup(t *testing.T, root bool, script string) {
	t.Helper()
	t.Cleanup(func() { box.run(t, root, nil, script, nil) })
}

func liveContains(t *testing.T, output, expected string) {
	t.Helper()
	if !strings.Contains(output, expected) {
		t.Errorf("missing %q in %q", expected, output)
	}
}

func TestSystemLiveServices(t *testing.T) {
	box := newLiveSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name := "fslive-" + strings.TrimSpace(box.command(t, true, "date", "+%s%N")) + ".service"
	file := "/run/systemd/system/" + name
	override, err := ServiceOverridePath(name)
	if err != nil {
		t.Fatal(err)
	}
	box.cleanup(t, true, "systemctl stop "+name+"; systemctl disable "+name+"; systemctl unmask "+name+"; systemctl reset-failed "+name+"; rm -f "+file+" "+override+" "+override+".bak-*; rmdir "+strings.TrimSuffix(override, "/override.conf")+" 2>/dev/null; systemctl daemon-reload")
	unit := "[Unit]\nDescription=fslive service\n[Service]\nExecStart=/bin/sleep infinity\nExecReload=/usr/bin/logger -p user.err -t fslive fslive-reload\n[Install]\nWantedBy=multi-user.target\n"
	writeUnit := func(text string) {
		box.run(t, true, strings.NewReader(text), "cat >"+file+"; systemctl daemon-reload", nil)
	}
	writeUnit(unit)
	read := func() ServiceUnit {
		rows, systemd, err := ReadServices(ctx, box.read)
		index := slices.IndexFunc(rows, func(row ServiceUnit) bool { return row.Name == name })
		if err != nil || !systemd || index < 0 {
			t.Fatalf("service missing: systemd=%v, err=%v", systemd, err)
		}
		return rows[index]
	}
	action := func(verb string) {
		t.Helper()
		script, err := ServiceActionCommand(verb, name)
		box.run(t, true, nil, script, err)
	}
	if row := read(); row.Enabled != "disabled" || row.Active != "inactive" {
		t.Fatalf("initial service: %+v", row)
	}
	action("start")
	started := read()
	if started.PID == 0 || started.Active != "active" || !started.CanReload {
		t.Fatalf("started service: %+v", started)
	}
	for verb, expected := range map[string]string{"status": "active (running)", "cat": "ExecStart=/bin/sleep", "show": "MainPID=" + strconv.FormatInt(started.PID, 10)} {
		script, err := ServiceReadCommand(verb, name)
		liveContains(t, box.run(t, false, nil, script, err), expected)
	}
	action("reload")
	if read().PID != started.PID {
		t.Error("reload restarted the service")
	}
	action("restart")
	if row := read(); row.PID == 0 || row.PID == started.PID {
		t.Errorf("restart did not replace the process: %+v", row)
	}
	action("stop")
	if row := read(); row.Active != "inactive" {
		t.Errorf("stopped service: %+v", row)
	}
	for _, step := range []struct{ verb, state string }{{"enable", "enabled"}, {"disable", "disabled"}, {"mask", "masked"}, {"unmask", "disabled"}, {"enable-now", "enabled"}} {
		action(step.verb)
		if row := read(); row.Enabled != step.state || step.verb == "enable-now" && row.Active != "active" {
			t.Errorf("%s: %+v", step.verb, row)
		}
	}
	action("stop")
	action("disable")
	writeUnit(strings.Replace(unit, "fslive service", "fslive edited", 1))
	script, err := SystemFileReadCommand(override)
	if out := box.run(t, true, nil, script, err); out != "missing\n" {
		t.Fatalf("unexpected initial override: %q", out)
	}
	good := "[Service]\nEnvironment=FSLIVE=yes\n"
	write, err := SystemFileWriteCommand(override, "", false)
	box.run(t, true, strings.NewReader(good), write, err)
	if out := box.run(t, true, nil, script, nil); out != "present\n"+good {
		t.Fatalf("unexpected override: %q", out)
	}
	bad := "[Service]\nExecStart=\nExecStart=/bin/false\n"
	write, err = SystemFileWriteCommand(override, good, true)
	box.run(t, true, strings.NewReader(bad), write, err)
	action("start")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if row := read(); row.Active == "failed" && row.Description == "fslive edited" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("expected failed service: %+v", row)
		}
	}
	for _, source := range []JournalSource{{Lines: 100, Since: "-10min", Priority: "err"}, {Unit: name, Lines: 5, Priority: "warning", Since: "boot"}} {
		log, err := JournalCommand(source)
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(150 * time.Millisecond) {
			out := box.run(t, true, nil, strings.Replace(log, " '-f'", "", 1), nil)
			if source.Unit == "" && strings.Contains(out, "fs-testbox") || source.Unit != "" && strings.Contains(out, name+": Failed with result") {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("missing error marker from %v", source)
				break
			}
		}
	}
	action("reset-failed")
	if row := read(); row.Active == "failed" {
		t.Errorf("reset-failed: %+v", row)
	}
}

func TestSystemLiveCron(t *testing.T) {
	box := newLiveSystem(t)
	for _, user := range []string{"", "root"} {
		name := user
		if name == "" {
			name = "login"
		}
		t.Run(name, func(t *testing.T) { box.testCrontab(t, user) })
	}
	file := "/etc/cron.d/fslive-" + strings.TrimSpace(box.command(t, true, "date", "+%s%N"))
	box.cleanup(t, true, "rm -f "+file+" "+file+".bak-*")
	script, err := SystemFileReadCommand(file)
	if out := box.run(t, true, nil, script, err); out != "missing\n" {
		t.Fatal("cron fixture already exists")
	}
	before := ""
	for _, after := range []string{"0 0 1 1 * root echo fslive\n", "0 0 1 2 * root echo fslive\n", CronDisabled + "0 0 1 2 * root echo fslive\n", "0 0 1 2 * root echo fslive\n", ""} {
		write, err := SystemFileWriteCommand(file, before, before != "")
		box.run(t, true, strings.NewReader(after), write, err)
		if out := box.run(t, true, nil, script, nil); out != "present\n"+after {
			t.Fatalf("system cron readback %q, want %q", out, after)
		}
		before = after
		if after != "" {
			time.Sleep(1100 * time.Millisecond)
		}
	}
	liveContains(t, box.run(t, false, nil, CronSystemFiles, nil), file)
	box.run(t, false, nil, CronTimersCommand(), nil)
}

func (box liveSystem) testCrontab(t *testing.T, user string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := user != ""
	readCommand, err := CronReadTransport(user)
	if err != nil {
		t.Fatal(err)
	}
	read := func() (string, bool) {
		t.Helper()
		run := box.read
		if root {
			run = box.elevated
		}
		text, detail, err := run(ctx, readCommand)
		if err != nil && !CronMissing(detail) {
			t.Fatalf("read crontab: %v: %s", err, detail)
		}
		return text, err == nil
	}
	original, existed := read()
	before, exists := original, existed
	marker := "fslive-cron-" + strings.TrimSpace(box.command(t, root, "date", "+%s%N"))
	var backups []string
	t.Cleanup(func() {
		if before != original || exists != existed {
			args, _ := cronArgs(user)
			cleanup := "set -e\ncurrent=$(" + readCommand + " 2>/dev/null) || exit 0\n" +
				"if [ \"$current\" = " + privilegeQuote(strings.TrimRight(before, "\n")) + " ]; then\n"
			if existed {
				cleanup += "printf '%s' " + privilegeQuote(original) + " | " + SystemCommand(append(args, "-")...)
			} else {
				cleanup += SystemCommand(append(args, "-r")...)
			}
			cleanup += "\nelse\nprintf '%s\\n' \"$current\" | grep -Fv -- " + privilegeQuote(marker) + " | " + SystemCommand(append(args, "-")...) + "\nfi"
			box.run(t, root, nil, cleanup, nil)
		}
		if len(backups) != 0 {
			box.command(t, root, append([]string{"rm", "-f", "--"}, backups...)...)
		}
	})
	write := func(after string) {
		t.Helper()
		command, err := CronWriteCommand(user, before, exists)
		out := box.run(t, root, strings.NewReader(after), command, err)
		if backup := strings.TrimSpace(strings.TrimPrefix(out, "Backup: ")); strings.HasPrefix(backup, "/home/tester/.crontab.bak-") || strings.HasPrefix(backup, "/root/.crontab.bak-") {
			backups = append(backups, backup)
		}
		before, exists = after, true
		if text, present := read(); !present || text != after {
			t.Fatal("crontab differs from written text")
		}
		if len(backups) > 0 {
			time.Sleep(1100 * time.Millisecond)
		}
	}
	job := func() CronJob {
		t.Helper()
		for _, job := range CronJobs(before, false) {
			if strings.Contains(job.Command, marker) {
				return job
			}
		}
		t.Fatal("fixture job missing")
		return CronJob{}
	}
	write(ChangeCron(before, -1, "0 0 1 1 * echo "+marker))
	write(ChangeCron(before, job().Line, "0 0 1 2 * echo "+marker+"-edit"))
	if job().Schedule != "0 0 1 2 *" || !strings.HasSuffix(job().Command, "-edit") {
		t.Fatal("edited crontab job not parsed")
	}
	write(ToggleCron(before, job()))
	if !job().Disabled {
		t.Fatal("disabled job not parsed")
	}
	write(ToggleCron(before, job()))
	if job().Disabled {
		t.Fatal("enabled job not parsed")
	}
	script, err := CronRunCommand(user)
	input, inputErr := CronManualInput(job().Command)
	if inputErr != nil {
		t.Fatal(inputErr)
	}
	liveContains(t, box.run(t, root, strings.NewReader(input), script, err), marker+"-edit")
	schedule, err := ParseCronSchedule(job().Schedule)
	if err != nil {
		t.Fatal(err)
	}
	next := schedule.Next(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), 1)
	if len(next) != 1 || next[0].Month() != time.February || next[0].Day() != 1 {
		t.Errorf("unexpected next run: %v", next)
	}
	write(ChangeCron(before, job().Line, ""))
}

func TestSystemLiveDisk(t *testing.T) {
	box := newLiveSystem(t)
	public := strings.TrimSpace(box.command(t, false, "mktemp", "-d", "/tmp/fslive-disk-XXXXXXXX"))
	publicFile := public + "/fslive-file"
	box.cleanup(t, false, "rm -f "+publicFile+"; rmdir "+public)
	box.command(t, false, "touch", publicFile)
	ordinaryScan, err := DiskScanScript(public, false)
	if scan := ParseDiskScan(box.run(t, false, nil, ordinaryScan, err), public, false); !slices.ContainsFunc(scan.Entries, func(row DiskEntry) bool { return row.Path == publicFile && row.File }) {
		t.Error("ordinary login scan did not list the fixture")
	}
	dir := strings.TrimSpace(box.command(t, true, "mktemp", "-d", "/root/fslive-disk-XXXXXXXX"))
	box.cleanup(t, true, "rm -f "+dir+"/fslive*; rmdir "+dir)
	file, log := dir+"/fslive-large", dir+"/fslive.log"
	rotated := "/var/log/" + strings.TrimPrefix(dir, "/root/") + ".log.1"
	box.cleanup(t, true, SystemCommand("rm", "-f", rotated))
	box.command(t, true, "truncate", "-s", "60M", file)
	box.run(t, true, nil, "echo fslive-log >"+log+"; cp "+log+" "+rotated, nil)
	if mounts := ParseDiskMounts(box.run(t, false, nil, DiskMountScript, nil)); len(mounts) == 0 {
		t.Fatal("no disk mounts parsed")
	}
	for _, largest := range []bool{false, true} {
		script, err := DiskScanScript(dir, largest)
		scan := ParseDiskScan(box.run(t, true, nil, script, err), dir, largest)
		if !slices.ContainsFunc(scan.Entries, func(row DiskEntry) bool { return row.Path == file }) {
			t.Errorf("large file missing in scan (largest=%v): %+v", largest, scan)
		}
	}
	if box.client.User() == "tester" {
		script, _ := DiskScanScript(dir, false)
		_, _, err := box.read(context.Background(), script)
		if err == nil {
			t.Error("private fixture unexpectedly readable without sudo")
		}
	}
	script, err := DiskTruncateScript(log)
	box.run(t, true, nil, script, err)
	box.command(t, true, "test", "-f", log)
	if size := strings.TrimSpace(box.command(t, true, "stat", "-c", "%s", log)); size != "0" {
		t.Errorf("truncate left %s bytes", size)
	}
	script, err = DiskRotatedScript([]string{rotated})
	box.run(t, true, nil, script, err)
	script, err = DiskDeleteScript([]string{file})
	box.run(t, true, nil, script, err)
	box.run(t, true, nil, "set -e; test ! -e "+file+"; test ! -e "+rotated, nil)
}

func TestSystemLivePackages(t *testing.T) {
	box := newLiveSystem(t)
	name := "fslive-update-" + strings.TrimSpace(box.command(t, true, "date", "+%s%N"))
	dir := "/tmp/" + name
	list := "/etc/apt/sources.list.d/" + name + ".list"
	box.cleanup(t, true, "set -e; if dpkg-query -W "+name+" >/dev/null 2>&1; then dpkg --purge "+name+"; fi; rm -f "+list+" /var/lib/apt/lists/*"+name+"* /var/cache/apt/archives/"+name+"_*.deb; rm -rf -- "+dir)
	box.run(t, true, nil, "set -eu\nname="+name+"\ndir="+dir+"\nlist="+list+`
mkdir -m 755 "$dir"
mkdir -p "$dir/package/DEBIAN" "$dir/repo"
build() {
    printf 'Package: %s\nVersion: %s\nArchitecture: all\nMaintainer: fslive <fixture@example.invalid>\nDescription: fslive APT test fixture\n' "$name" "$1" >"$dir/package/DEBIAN/control"
    dpkg-deb --build "$dir/package" "$2"
}
build 1 "$dir/initial.deb"
dpkg -i "$dir/initial.deb"
build 2 "$dir/repo/$name.deb"
cd "$dir/repo"
dpkg-deb --field "$name.deb" >Packages
printf 'Filename: ./%s.deb\nSize: %s\nSHA256: %s\n\n' "$name" "$(stat -c %s "$name.deb")" "$(sha256sum "$name.deb" | cut -d ' ' -f 1)" >>Packages
printf 'deb [trusted=yes] file:%s/repo ./\n' "$dir" >"$list"
`, nil)
	read := func() []PackageUpdate {
		return ParsePackageUpdates("apt", box.run(t, false, nil, PackageListScript("apt"), nil), box.run(t, false, nil, PackageDetailsScript("apt"), nil))
	}
	script, err := PackageRefreshScript("apt")
	box.run(t, true, nil, script, err)
	updates := read()
	if !slices.ContainsFunc(updates, func(row PackageUpdate) bool { return row.Name == name && row.Current == "1" && row.New == "2" }) {
		t.Fatalf("APT did not list the synthetic upgrade: %+v", updates)
	}
	script, err = PackageUpgradeScript("apt", "selected", []string{name})
	box.run(t, true, nil, script, err)
	installed := strings.TrimSpace(box.command(t, false, "dpkg-query", "-W", "-f=${Version}", name))
	if installed != "2" || slices.ContainsFunc(read(), func(row PackageUpdate) bool { return row.Name == name }) {
		t.Fatalf("package upgrade not reflected: installed=%q", installed)
	}
}

func TestSystemLiveLogFollow(t *testing.T) {
	box := newLiveSystem(t)
	file := "/var/log/fslive-" + strings.TrimSpace(box.command(t, true, "date", "+%s%N")) + ".log"
	box.cleanup(t, true, SystemCommand("rm", "-f", file))
	box.run(t, true, strings.NewReader("fslive-old\nfslive-recent\n"), "umask 077; cat >"+file, nil)
	script, err := JournalCommand(JournalSource{File: file, Lines: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	chunks, ended := make(chan string, 10), make(chan error, 1)
	streamBox := newLiveSystem(t)
	go func() {
		_, _, err := streamBox.root.Run(ctx, "exec "+script, PrivilegeOptions{Progress: func(chunk string) { chunks <- chunk }, Interrupt: true})
		ended <- err
	}()
	await := func(want string) string {
		t.Helper()
		var output string
		for !strings.Contains(output, want) {
			select {
			case chunk := <-chunks:
				output += chunk
			case err := <-ended:
				t.Fatalf("log ended: %v", err)
			case <-ctx.Done():
				t.Fatalf("missing log line %q", want)
			}
		}
		return output
	}
	if strings.Contains(await("fslive-recent\n"), "fslive-old") {
		t.Error("initial read exceeded line limit")
	}
	box.run(t, true, strings.NewReader("fslive-follow\n"), "cat >>"+file, nil)
	await("fslive-follow\n")
	stop()
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(fmt.Errorf("cancel follow: %w", err))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not stop")
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(150 * time.Millisecond) {
		out := box.run(t, true, nil, "ps -C tail -o args= || :", nil)
		if !strings.Contains(out, file) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote tail kept running after cancellation")
		}
	}
}

func TestSystemLiveFirewall(t *testing.T) {
	box := newLiveSystem(t)
	endpoint := box.run(t, false, nil, FirewallSessionScript, nil)
	source, port := FirewallSessionEndpoint(endpoint)
	listening := box.run(t, true, nil, FirewallListeningScript, nil)
	ports := FirewallSSHPorts(2222, box.run(t, true, nil, FirewallConnectionsScript, nil))
	if port != 22 || !slices.Contains(ports, port) || !strings.Contains(listening, "sshd") {
		t.Fatalf("SSH listener not protected: actual=%d, detected=%v", port, ports)
	}
	liveContains(t, box.run(t, false, nil, FirewallDetectScript, nil), "ufw")
	if _, detail, err := box.elevated(context.Background(), FirewallNFTScript); err != nil {
		if !strings.Contains(detail, "not found") {
			t.Fatalf("nftables read: %v: %s", err, detail)
		}
		dir := strings.TrimSpace(box.command(t, true, "mktemp", "-d", "/root/fslive-nft-XXXXXXXX"))
		box.cleanup(t, true, SystemCommand("rm", "-rf", dir))
		box.run(t, true, nil, "set -e; cd "+dir+"; apt-get download nftables; dpkg-deb -x nftables_*.deb .", nil)
		box.run(t, true, nil, "PATH="+dir+"/usr/sbin:$PATH\n"+FirewallNFTScript, nil)
	}
	box.run(t, true, nil, FirewallIPTablesScript, nil)
	if state := ParseUFW(box.run(t, true, nil, UFWReadScript, nil)); state.Active {
		t.Skip("UFW must be inactive at start")
	}
	if added := box.command(t, true, "ufw", "show", "added"); strings.Contains(added, "ufw allow") || strings.Contains(added, "ufw deny") || strings.Contains(added, "ufw limit") || strings.Contains(added, "ufw reject") {
		t.Skip("UFW already has configured rules")
	}
	marker := "fslive-" + strings.TrimSpace(box.command(t, true, "date", "+%s%N"))
	// Docker exec is not needed for recovery: the testbox allows a source-specific SSH rule,
	// and this timer disables UFW even if the SSH transport becomes unreachable.
	watchdog := marker + "-ufw"
	box.command(t, true, "systemd-run", "--unit="+watchdog, "--on-active=45s", "/usr/sbin/ufw", "disable")
	box.cleanup(t, true, "systemctl stop "+watchdog+".timer")
	before := box.run(t, true, nil, "find /etc/ufw /lib/ufw -maxdepth 1 -type f -name '*.bak-*' -print", nil)
	box.cleanup(t, true, "find /etc/ufw /lib/ufw -maxdepth 1 -type f -name '*.bak-*' -print | while IFS= read -r file; do case "+privilegeQuote("\n"+before)+" in *\"$file\"*) ;; *) rm -f -- \"$file\";; esac; done")
	box.cleanup(t, true, "set -e; ufw disable; ufw --force delete allow proto tcp from "+privilegeQuote(source.String())+" to any port 22; ufw --force delete allow proto tcp from 192.0.2.30 to any port 18090; ufw --force delete deny proto tcp from 192.0.2.30 to any port 18091")
	change := func(script string, err error) {
		t.Helper()
		box.run(t, true, nil, script, err)
		time.Sleep(1100 * time.Millisecond)
	}
	change(UFWAddScript("allow", "18090", "tcp", "192.0.2.30", marker+"-allow", ports))
	change(UFWAddScript("deny", "18091", "tcp", "192.0.2.30", marker+"-deny", ports))
	change(UFWToggleScript(true, port, source))
	connection, err := net.DialTimeout("tcp", box.client.RemoteAddr().String(), 3*time.Second)
	if err != nil {
		t.Fatal("new SSH connection unreachable with UFW active: ", err)
	}
	connection.Close()
	for _, wanted := range []string{marker + "-allow", marker + "-deny"} {
		state := ParseUFW(box.run(t, true, nil, UFWReadScript, nil))
		index := slices.IndexFunc(state.Rules, func(rule FirewallRule) bool { return strings.Contains(rule.Comment, wanted) })
		if !state.Active || index < 0 {
			t.Fatalf("missing active rule %q", wanted)
		}
		change(UFWDeleteScript(state.Rules[index], ports))
	}
	if out := box.run(t, true, nil, UFWReadScript, nil); strings.Contains(out, marker) {
		t.Error("fixture rules remain after deletion")
	}
	change(UFWToggleScript(false, port, source))
	if ParseUFW(box.run(t, true, nil, UFWReadScript, nil)).Active {
		t.Error("UFW still active")
	}
}

func TestSystemLiveNginx(t *testing.T) {
	box := newLiveSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	status := ParseNginxStatus(box.run(t, false, nil, NginxStatusCommand, nil))
	if !status.Installed || !status.Unit || status.State != "inactive" {
		t.Skip("requires installed, stopped Nginx")
	}
	name := "fslive-" + strings.TrimSpace(box.command(t, true, "date", "+%s%N"))
	domain := name + ".test"
	certDir := "/etc/ssl/fslive"
	cert, key := certDir+"/"+name+".pem", certDir+"/"+name+".key"
	box.command(t, true, "mkdir", "-p", certDir)
	box.cleanup(t, true, "rm -f "+cert+" "+key+"; rmdir "+certDir+" 2>/dev/null || :")
	box.command(t, true, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2", "-subj", "/CN="+domain, "-addext", "subjectAltName=DNS:"+domain, "-keyout", key, "-out", cert)
	read := func() NginxInventory {
		t.Helper()
		inv, err := ReadNginxInventory(ctx, box.elevated)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	inv := read()
	file, link, err := inv.NewSite(name)
	if err != nil || link == "" {
		t.Fatalf("Debian layout: %v", err)
	}
	box.cleanup(t, true, "rm -f "+file+" "+link+" "+file+".bak-*")
	text := "server {\n    listen 127.0.0.1:18090 ssl;\n    server_name " + domain + ";\n    ssl_certificate " + cert + ";\n    ssl_certificate_key " + key + ";\n    return 200 fslive-original;\n}\n"
	script, err := inv.CreateCommand(name, text, false, true)
	box.run(t, true, strings.NewReader(text), script, err)
	inv = read()
	if inv.Links[name] != "enabled" {
		t.Fatal("website did not become enabled")
	}
	files := CertificateFiles(box.run(t, true, nil, CertificateDiscoveryCommand, nil), inv.Certificates())
	certs, _ := ParseCertificateFiles(box.run(t, true, nil, CertificateReadCommand(files), nil), files)
	if !slices.ContainsFunc(certs, func(row Certificate) bool { return row.File == cert && slices.Contains(row.SANs, domain) }) {
		t.Fatal("fixture certificate not listed")
	}
	readFile, err := NginxReadCommand(file)
	old := box.run(t, true, nil, readFile, err)
	if old != text {
		t.Fatal("new site content differs from form")
	}
	changed := strings.Replace(text, "fslive-original", "fslive-edited", 1)
	edit, err := NewNginxChange(file, old, changed, time.Now(), inv.NginxTree)
	if err != nil {
		t.Fatal(err)
	}
	script, err = edit.SaveCommand(old, inv.NginxTree)
	box.run(t, true, strings.NewReader(changed), script, err)
	if box.run(t, true, nil, readFile, nil) != changed {
		t.Fatal("edited site was not saved")
	}
	for _, enable := range []bool{false, true} {
		inv = read()
		script, err := inv.ToggleCommand([]string{name}, enable, false, true)
		box.run(t, true, nil, script, err)
		if state := read().Links[name]; enable && state != "enabled" || !enable && state != "disabled" {
			t.Fatalf("enable=%v: state=%s", enable, state)
		}
	}
	script, err = NginxActionCommand("test", true, NginxMain)
	box.run(t, true, nil, script, err)
	if link := strings.TrimSpace(box.command(t, true, "readlink", NginxEnabled+"default")); link != NginxAvailable+"default" {
		t.Skip("expected Debian default site link for temporary port conflict isolation")
	}
	defaultLink := NginxEnabled + "default"
	// Keep the existing default link intact: move it aside while Caddy owns port 80.
	holding := "/etc/nginx/" + name + "-default-link"
	box.command(t, true, "mv", "-T", defaultLink, holding)
	box.cleanup(t, true, SystemCommand("mv", "-T", holding, defaultLink))
	stopScript, err := ServiceActionCommand("stop", "nginx.service")
	if err != nil {
		t.Fatal(err)
	}
	box.cleanup(t, true, stopScript)
	script, err = ServiceActionCommand("start", "nginx.service")
	box.run(t, true, nil, script, err)
	box.command(t, true, "systemctl", "is-active", "nginx.service")
	script, err = NginxActionCommand("reload", true, NginxMain)
	box.run(t, true, nil, script, err)
	liveContains(t, box.command(t, false, "curl", "-fsS", "--cacert", cert, "--resolve", domain+":18090:127.0.0.1", "https://"+domain+":18090"), "fslive-edited")
	box.run(t, true, nil, stopScript, nil)
	if state := ParseNginxStatus(box.run(t, false, nil, NginxStatusCommand, nil)); state.State != "inactive" {
		t.Errorf("Nginx did not stop: %s", state.State)
	}
	box.command(t, true, "rm", "--", link, file)
	if _, exists := read().Links[name]; exists {
		t.Error("removed website still in inventory")
	}
}
