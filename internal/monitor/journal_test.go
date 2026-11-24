package monitor

import "testing"

func TestJournalCommands(t *testing.T) {
	for _, sample := range []struct {
		source JournalSource
		want   string
	}{
		{JournalSource{Lines: 300}, `'env' 'LC_ALL=C' 'journalctl' '-o' 'short-iso' '--no-pager' '-n' '300' '-f'`},
		{JournalSource{Unit: `worker@blue\x2dgreen.service`, Priority: "warning", Since: "-10min", Lines: 25}, `'env' 'LC_ALL=C' 'journalctl' '-o' 'short-iso' '--no-pager' '-n' '25' '-f' '-u' 'worker@blue\x2dgreen.service' '-p' 'warning' '--since' '-10min'`},
		{JournalSource{Since: "boot", Lines: 10}, `'env' 'LC_ALL=C' 'journalctl' '-o' 'short-iso' '--no-pager' '-n' '10' '-f' '--boot'`},
		{JournalSource{File: "/var/log/a'b.log", Lines: 100}, `'tail' '-n' '100' '-F' '--' '/var/log/a'\''b.log'`},
		{JournalSource{File: "--help", Lines: 10}, ""},
		{JournalSource{File: "/var/log/../a", Lines: 100}, ""},
		{JournalSource{File: "/root/.ssh/id_ed25519", Lines: 100}, ""},
		{JournalSource{Unit: "*.service", Lines: 10}, ""},
		{JournalSource{Since: "-1h; exit", Lines: 10}, ""},
		{JournalSource{Priority: "debug", Lines: 10}, ""},
	} {
		got, err := JournalCommand(sample.source)
		if got != sample.want || (err != nil) != (sample.want == "") {
			t.Errorf("%+v: %q, %v", sample.source, got, err)
		}
	}
}
