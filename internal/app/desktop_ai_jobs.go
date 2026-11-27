package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// The agent's commands run as jobs on the server, as BT Panel's agent
// runs them: each in a folder of its own under /tmp/finallyshell-ai-<uid>
// (mode 700, the user's own), detached from the SSH channel (setsid, and
// ignoring hangups), its output and exit code written to files there. The channel
// that starts it waits up to aiJobSync for the end; a command still
// running then goes on in the background while the app looks at its
// state every aiJobPoll, on a short channel each time, until it ends or
// reaches its deadline, which stops it with its process group. A command
// that prints more than the model gets is kept whole on the server, and
// the model is told where. Folders older than a day are removed as a new
// command starts.

var (
	// aiJobSync is how long the channel that starts a command waits for
	// it to end, aiJobPoll how often a command in the background is
	// looked at, aiJobGrace how much longer than its wait the start may
	// take to answer, and aiJobStopWait how long stopping one may take;
	// tests shorten them.
	aiJobSync     = 20 * time.Second
	aiJobPoll     = 3 * time.Second
	aiJobGrace    = 20 * time.Second
	aiJobStopWait = 15 * time.Second
)

const (
	aiOutputHead = aiOutputLimit * 3 / 8
	aiOutputTail = aiOutputLimit - aiOutputHead
	// aiJobKeepMinutes is how long a finished command's folder is kept.
	aiJobKeepMinutes = 24 * 60
)

func newAIJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// aiShQuote quotes s for the shell.
func aiShQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// aiJobHead is the start of every script about the job id: its folder in
// d, refused unless it is the user's own, and emit, which prints the
// job's result: its exit code ("x" without one), its size and its folder,
// then its output, cut to its start and end.
func aiJobHead(id string) string {
	return fmt.Sprintf(`b=/tmp/finallyshell-ai-$(id -u)
d=$b/%s
emit() {
	r=$(cat "$d/rc" 2>/dev/null)
	s=$(wc -c <"$d/out" 2>/dev/null | tr -d ' ')
	printf 'finallyshell-rc %%s %%s %%s\n' "${r:-x}" "${s:-0}" "$d"
	if [ "${s:-0}" -le %d ]; then cat "$d/out" 2>/dev/null
	else head -c %d "$d/out"; printf '\nfinallyshell-cut\n'; tail -c %d "$d/out"; fi
}
`, id, aiOutputLimit, aiOutputHead, aiOutputTail)
}

// aiJobStart starts the command it reads on its input, as the job id,
// and waits up to wait for it to end.
func aiJobStart(id string, wait time.Duration) string {
	return aiJobHead(id) + fmt.Sprintf(`mkdir -p -m 700 "$b" 2>/dev/null
[ -d "$b" ] && [ ! -L "$b" ] && [ -O "$b" ] || { echo "finallyshell: $b is not the user's own folder" >&2; exit 1; }
find "$b" -mindepth 1 -maxdepth 1 -type d -mmin +%d -exec rm -rf {} + 2>/dev/null
mkdir -m 700 "$d" || exit 1
cat >"$d/cmd"
i=sh; command -v bash >/dev/null 2>&1 && i=bash
s=; command -v setsid >/dev/null 2>&1 && s=setsid
$s sh -c 'trap "" HUP; "$2" "$1/cmd" >"$1/out" 2>&1 </dev/null; echo $? >"$1/rc.t"; mv "$1/rc.t" "$1/rc"' sh "$d" "$i" >/dev/null 2>&1 </dev/null &
echo $! >"$d/pid"
end=$(( $(date +%%s) + %d ))
while [ ! -f "$d/rc" ] && [ "$(date +%%s)" -lt "$end" ]; do sleep 0.2 2>/dev/null || sleep 1; done
if [ -f "$d/rc" ]; then emit; else echo finallyshell-running; fi
`, aiJobKeepMinutes, max(int(wait/time.Second), 1))
}

// aiJobState prints the job's result once it ended, or whether it runs.
func aiJobState(id string) string {
	return aiJobHead(id) + `if [ -f "$d/rc" ]; then emit
elif [ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null; then echo finallyshell-running
else echo finallyshell-lost; fi
`
}

// aiJobStop stops the job, its process group with it (gently, then not),
// and prints what it printed.
func aiJobStop(id string) string {
	return aiJobHead(id) + `p=$(cat "$d/pid" 2>/dev/null)
if [ -n "$p" ] && [ ! -f "$d/rc" ]; then
	kill -TERM -"$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null
	n=0
	while [ ! -f "$d/rc" ] && kill -0 "$p" 2>/dev/null && [ $n -lt 10 ]; do sleep 0.3 2>/dev/null || sleep 1; n=$((n+1)); done
	kill -0 "$p" 2>/dev/null && { kill -KILL -"$p" 2>/dev/null || kill -KILL "$p" 2>/dev/null; }
fi
emit
`
}

// aiJobResult is what a script about a job printed.
type aiJobResult struct {
	state  string // "running", "done" or "lost"
	exit   int
	exited bool // it has an exit code
	output string
}

// aiJobParse reads what a script about a job printed.
func aiJobParse(text string) aiJobResult {
	for line, rest := range aiJobLines(text) {
		switch {
		case line == "finallyshell-running":
			return aiJobResult{state: "running"}
		case line == "finallyshell-lost":
			return aiJobResult{state: "lost"}
		case strings.HasPrefix(line, "finallyshell-rc "):
			f := strings.Fields(line)
			res := aiJobResult{state: "done"}
			if len(f) < 4 {
				return res
			}
			if code, err := strconv.Atoi(f[1]); err == nil {
				res.exit, res.exited = code, true
			}
			size, _ := strconv.Atoi(f[2])
			dir := strings.Join(f[3:], " ")
			output := rest
			if head, tail, cut := strings.Cut(rest, "\nfinallyshell-cut\n"); cut {
				left := max(size-len(head)-len(tail), 0)
				output = head + fmt.Sprintf("\n[… %d bytes left out …]\n", left) + tail +
					fmt.Sprintf("\n[The whole output is on the server: %s/out, %d bytes; read parts of it with grep, sed -n or tail.]", dir, size)
			}
			res.output = strings.ToValidUTF8(output, "�")
			return res
		}
	}
	return aiJobResult{}
}

// aiJobLines yields each line of text with what follows it, to find the
// scripts' marker after anything a login script printed.
func aiJobLines(text string) func(func(string, string) bool) {
	return func(yield func(string, string) bool) {
		for text != "" {
			line, rest, _ := strings.Cut(text, "\n")
			if !yield(strings.TrimSuffix(line, "\r"), rest) {
				return
			}
			text = rest
		}
	}
}

// aiJobExec runs script with sh on server, input on its standard input,
// and returns what it printed; ctx ends it.
func aiJobExec(ctx context.Context, server *ssh.Client, script string, input io.Reader) (string, error) {
	session, err := server.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var out, errOut strings.Builder
	session.Stdout, session.Stderr, session.Stdin = &out, &errOut, input
	done := make(chan error, 1)
	go func() { done <- session.Run("sh -c " + aiShQuote(script)) }()
	select {
	case err = <-done:
		if err != nil && strings.TrimSpace(errOut.String()) != "" {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(errOut.String()))
		}
		return out.String(), err
	case <-ctx.Done():
		session.Close()
		<-done
		return "", ctx.Err()
	}
}

// aiJobKill stops the job id, whatever happened to the request, and
// returns what it printed.
func aiJobKill(server *ssh.Client, id string) aiJobResult {
	ctx, cancel := context.WithTimeout(context.Background(), aiJobStopWait)
	defer cancel()
	out, err := aiJobExec(ctx, server, aiJobStop(id), nil)
	if err != nil {
		return aiJobResult{}
	}
	return aiJobParse(out)
}

// aiRunCommand runs command on server as a job, with no terminal and no
// input, stopping it at deadline, which may move as it runs
// (aiCommandTimeout from now when nil), or when ctx ends. It returns its
// exit code and its output, stdout and stderr together, cut to its start
// and end.
func aiRunCommand(ctx context.Context, server *ssh.Client, command string, deadline *aiDeadline) (exit int, output string, timedOut bool, err error) {
	if server == nil {
		return 0, "", false, errors.New("not connected")
	}
	if deadline == nil {
		deadline = newAIDeadline(time.Now(), aiCommandTimeout)
	}
	id := newAIJobID()
	wait := min(aiJobSync, max(time.Until(deadline.when()), time.Second))
	start, cancel := context.WithTimeout(ctx, wait+aiJobGrace)
	out, err := aiJobExec(start, server, aiJobStart(id, wait), strings.NewReader(command))
	cancel()
	if err != nil {
		aiJobKill(server, id) // it may have started
		if ctx.Err() != nil {
			return 0, "", false, ctx.Err()
		}
		return 0, "", false, err
	}
	res := aiJobParse(out)
	if res.state == "running" {
		deadline.setBackground()
	}
	failures := 0
	for res.state == "running" {
		timer := time.NewTimer(min(aiJobPoll, max(time.Until(deadline.when()), 0)))
		select {
		case <-ctx.Done():
			timer.Stop()
			stopped := aiJobKill(server, id)
			return 0, stopped.output, false, ctx.Err()
		case <-timer.C:
		}
		if !time.Now().Before(deadline.when()) {
			return 0, aiJobKill(server, id).output, true, nil
		}
		look, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := aiJobExec(look, server, aiJobState(id), nil)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				continue // the select above stops it
			}
			// A server slow to answer once is not a failed command.
			if failures++; failures >= 5 {
				return 0, "", false, err
			}
			continue
		}
		failures = 0
		res = aiJobParse(out)
	}
	switch {
	case res.state == "lost":
		return 0, "", false, errors.New("the command ended without an exit code: the server may have restarted, or its temporary files were removed")
	case res.state != "done":
		return 0, "", false, errors.New("the server did not answer as expected")
	case !res.exited:
		return 0, res.output, false, errors.New("the command was stopped on the server")
	}
	return res.exit, res.output, false, nil
}
