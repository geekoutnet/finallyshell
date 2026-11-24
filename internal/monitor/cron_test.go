package monitor

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCronScheduleNext(t *testing.T) {
	zone, err := CronZone("+0530\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		rule, after string
		want        []string
	}{
		{"*/15 * * * *", "2026-01-02 10:00", []string{"2026-01-02 10:15", "2026-01-02 10:30", "2026-01-02 10:45"}},
		{"0 3 1-5 * *", "2026-01-04 03:00", []string{"2026-01-05 03:00", "2026-02-01 03:00", "2026-02-02 03:00"}},
		{"10,40 9 * JAN MON-FRI", "2026-01-02 09:40", []string{"2026-01-05 09:10", "2026-01-05 09:40", "2026-01-06 09:10"}},
		{"0 0 13 * FRI", "2026-01-08 00:00", []string{"2026-01-09 00:00", "2026-01-13 00:00", "2026-01-16 00:00"}},
		{"0 0 * * 7", "2026-01-02 00:00", []string{"2026-01-04 00:00", "2026-01-11 00:00", "2026-01-18 00:00"}},
		{"@yearly", "2026-01-02 00:00", []string{"2027-01-01 00:00", "2028-01-01 00:00", "2029-01-01 00:00"}},
		{"@reboot", "2026-01-02 00:00", nil},
		{"0 0 31 FEB *", "2026-01-02 00:00", nil},
	} {
		t.Run(sample.rule, func(t *testing.T) {
			const format = "2006-01-02 15:04"
			after, err := time.ParseInLocation(format, sample.after, zone)
			if err != nil {
				t.Fatal(err)
			}
			rule, err := ParseCronSchedule(sample.rule)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, next := range rule.Next(after, 3) {
				got = append(got, next.Format(format))
			}
			if !reflect.DeepEqual(got, sample.want) {
				t.Fatalf("next = %v, want %v", got, sample.want)
			}
		})
	}
	for _, invalid := range []string{"*/0 * * * *", "60 * * * *", "0 0 * * FRI-MON", "0 0 * JANUARY *", "+1 * * * *", "@unknown", "0 0 * *"} {
		if _, err := ParseCronSchedule(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}

func TestCronDocumentAndCommands(t *testing.T) {
	const source = "# report schedule\nMAILTO=\"\"\n\n  */15 * * * * /usr/local/bin/report 'daily batch'\n@reboot /usr/local/bin/warmup\n"
	rows := CronJobs(source, false)
	if len(rows) != 2 || rows[0].Line != 3 || rows[0].Command != "/usr/local/bin/report 'daily batch'" {
		t.Fatalf("rows = %#v", rows)
	}
	disabled := ToggleCron(source, rows[0])
	marked := CronJobs(disabled, false)
	if !marked[0].Disabled || ToggleCron(disabled, marked[0]) != source {
		t.Fatalf("round-trip = %q", disabled)
	}
	if removed := ChangeCron(source, 4, ""); removed != strings.Split(source, "@reboot")[0] {
		t.Fatalf("remove = %q", removed)
	}
	if rows := CronJobs("0 3 * * MON-FRI root /usr/local/bin/report\n", true); len(rows) != 1 || rows[0].User != "root" {
		t.Fatalf("system rows = %#v", rows)
	}
	command, err := CronReadCommand("batch-user")
	if err != nil || command != `'env' 'LC_ALL=C' 'crontab' '-u' 'batch-user' '-l'` {
		t.Fatalf("read = %q, %v", command, err)
	}
	command, err = CronWriteCommand("root", source, true)
	if err != nil || strings.Contains(command, "daily batch") || !strings.Contains(command, `'ln' "$tmp/old" "$home/.crontab.bak-$stamp"`) || !strings.Contains(command, `'env' 'LC_ALL=C' 'crontab' '-u' 'root' '-' <"$tmp/new"`) {
		t.Fatalf("write = %q, %v", command, err)
	}
	if _, err := CronRunCommand("-root"); err == nil {
		t.Fatal("accepted option as user")
	}
	if _, err := CronManualInput("echo x%y"); err == nil {
		t.Fatal("accepted cron input syntax for manual run")
	}
	if redirect, err := CronLogRedirect("/var/log/worker's output.log"); err != nil || redirect != ` >> '/var/log/worker'\''s output.log' 2>&1` {
		t.Fatalf("redirect = %q, %v", redirect, err)
	}
}
