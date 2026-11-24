package monitor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAlertFailedServices(t *testing.T) {
	const output = "  docs-worker.service loaded failed failed Documents worker\r\n● cache@build.service loaded failed failed Build cache\r\n" +
		"timer.service loaded active running Not a failure\n\n" + alertReadStart + "\r\n"
	for _, sample := range []struct {
		out  string
		err  error
		want []string
		ok   bool
	}{
		{output, nil, []string{"docs-worker.service", "cache@build.service"}, true},
		{alertReadStart + "\n", nil, nil, true},
		{"", nil, nil, false},
		{output, errors.New("bus not available"), nil, false},
	} {
		got, ok := AlertFailedServices(context.Background(), func(_ context.Context, script string) (string, string, error) {
			if !strings.Contains(script, "'systemctl' '--failed' '--type=service' '--no-legend' '--no-pager' '--plain'") {
				t.Fatalf("read command = %q", script)
			}
			return sample.out, "", sample.err
		})
		if ok != sample.ok || !reflect.DeepEqual(got, sample.want) {
			t.Fatalf("got %v (%v), want %v (%v)", got, ok, sample.want, sample.ok)
		}
	}
}
