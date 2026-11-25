package monitor

import (
	"net/netip"
	"strconv"
)

const TunnelRelayDetectScript = `set -e
'command' '-v' 'setsid' >/dev/null 2>&1 || exit 1
for tool in 'socat' 'ncat' 'python3'; do
  'command' '-v' "$tool" >/dev/null 2>&1 || continue
  if [ "$tool" = 'python3' ] && ! 'python3' '-c' 'import asyncio,sys; sys.exit(sys.version_info < (3,8))'; then continue; fi
  'printf' '%s\n' "$tool"; exit 0
done
exit 1
`

func TunnelRelayScript(tool, addr string, port, target int) (string, error) {
	ip, err := netip.ParseAddr(addr)
	if err != nil || ip.Zone() != "" || ip.IsLoopback() || ip.Is4In6() || port < 1 || port > 65535 || target < 1 || target > 65535 || port == target {
		return "", ErrOpsInvalid
	}
	listen, destination := strconv.Itoa(port), strconv.Itoa(target)
	var command string
	switch tool {
	case "socat":
		family, bind := "TCP4", ip.String()
		if ip.Is6() {
			family, bind = "TCP6", "["+bind+"]"
		}
		command = opsCommand("socat", family+"-LISTEN:"+listen+",bind="+bind+",fork,reuseaddr", "TCP4:127.0.0.1:"+destination)
	case "ncat":
		family := "-4"
		if ip.Is6() {
			family = "-6"
		}
		command = opsCommand("ncat", family, "--listen", "--keep-open", "--sh-exec", opsCommand("ncat", "-4", "127.0.0.1", destination), ip.String(), listen)
	case "python3":
		command = opsCommand("python3", "-", ip.String(), listen, destination) + " <<'FINALLYSHELL_PY' &\n" + relayPython + "FINALLYSHELL_PY\n"
	default:
		return "", ErrOpsInvalid
	}
	if tool != "python3" {
		command += " </dev/null &\n"
	}
	return `set -eu
'exec' 3<&0
pid=''
watch=''
cleanup() {
  if [ -n "$pid" ]; then 'kill' '-TERM' "-$pid" 2>/dev/null || :; fi
  if [ -n "$watch" ]; then 'kill' '-TERM' "$watch" 2>/dev/null || :; fi
  if [ -n "$pid" ]; then 'wait' "$pid" 2>/dev/null || :; fi
}
trap 'cleanup' EXIT
trap 'exit 0' HUP INT TERM
'setsid' ` + command + `pid=$!
parent=$$
( while IFS= 'read' '-r' ignored; do :; done; 'kill' '-TERM' "$parent" 2>/dev/null || : ) <&3 &
watch=$!
'sleep' '1'
'kill' '-0' "-$pid"
'printf' '%s\n' 'FINALLYSHELL_RELAY_READY'
'wait' "$pid"
`, nil
}

const relayPython = `import asyncio, sys

async def copy(reader, writer):
    while True:
        data = await reader.read(65536)
        if not data:
            writer.write_eof()
            return
        writer.write(data)
        await writer.drain()

async def relay(reader, writer):
    remote = None
    tasks = []
    try:
        upstream, remote = await asyncio.wait_for(asyncio.open_connection('127.0.0.1', int(sys.argv[3])), 10)
        tasks = [asyncio.create_task(copy(reader, remote)), asyncio.create_task(copy(upstream, writer))]
        await asyncio.gather(*tasks)
    except (OSError, asyncio.TimeoutError):
        pass
    finally:
        for task in tasks:
            task.cancel()
        if remote is not None:
            remote.close()
        writer.close()

async def main():
    server = await asyncio.start_server(relay, sys.argv[1], int(sys.argv[2]))
    async with server:
        await server.serve_forever()

asyncio.run(main())
`
