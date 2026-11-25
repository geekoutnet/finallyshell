package monitor

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func caddyDockerReplacementScript(inst CaddyInstall, id, image string, request recreate) (string, error) {
	config, ok := caddyContainerPath(inst, inst.Config)
	if !ok || inst.Resume || !caddyIdentifier.MatchString(inst.Container) || !validCtrImage(image) || len(id) != 64 || !isCtrHex(id) {
		return "", ErrCaddyUnsafe
	}
	var script strings.Builder
	script.WriteString(caddyInstallShell)
	fmt.Fprintf(&script, "id=%s\nname=%s\nimage=%s\n", privilegeQuote(id), privilegeQuote(inst.Container), privilegeQuote(image))
	script.WriteString(`endpoint=${DOCKER_HOST-}
if [ -z "$endpoint" ] || [ -n "${DOCKER_CONTEXT-}" ]; then endpoint=$(docker 'context' 'inspect' '--format' '{{.Endpoints.docker.Host}}'); fi
case "$endpoint" in unix:///*) sock=${endpoint#unix://} ;; *) exit '1' ;; esac
[ -S "$sock" ]
api() {
  curl -q --silent --show-error --fail --globoff --noproxy '*' --unix-socket "$sock" --max-time '60' "$@"
}
[ "$(docker 'inspect' '--type' 'container' '--format' '{{.Id}}' "$name")" = "$id" ]
api "http://localhost/containers/$id/json" >/dev/null
docker 'pull' '--' "$image"
expected=$(docker 'image' 'inspect' '--format' '{{.Id}}' "$image")
old="finallyshell-caddy-$(date -u '+%Y%m%d%H%M%S')-$$"
[ "$(docker 'inspect' '--type' 'container' '--format' '{{.Id}}' "$name")" = "$id" ]
was_running=$(docker 'inspect' '--format' '{{.State.Running}}' "$id")
policy=$(docker 'inspect' '--format' '{{.HostConfig.RestartPolicy.Name}}{{if and (eq .HostConfig.RestartPolicy.Name "on-failure") .HostConfig.RestartPolicy.MaximumRetryCount}}:{{.HostConfig.RestartPolicy.MaximumRetryCount}}{{end}}' "$id")
new=''; stopped=''; renamed=''; policy_changed=''
recover() {
  status=$?; trap - '0'; trap '' HUP INT TERM; set +e
  if [ "$status" -ne '0' ]; then
    if [ -n "$new" ]; then docker 'rm' '-f' "$new" >/dev/null || status='125'; fi
    if [ -n "$renamed" ]; then docker 'rename' "$id" "$name" || status='125'; fi
    if [ -n "$policy_changed" ]; then docker 'update' '--restart' "$policy" "$id" >/dev/null || status='125'; fi
    if [ -n "$stopped" ]; then docker 'start' "$id" >/dev/null || status='125'; fi
    if [ "$status" = '125' ]; then printf '%s\n' 'CADDY_RECOVERY_FAILED' "Original container: $id ($old)" >&2; fi
  fi
  rm -rf -- "$tmp"
  exit "$status"
}
trap recover '0'
if [ "$was_running" = 'true' ]; then
  stopped='yes'
  docker 'stop' "$id" >/dev/null
fi
docker 'rename' "$id" "$old"
renamed='yes'
if [ "$policy" != 'no' ] && [ -n "$policy" ]; then
  docker 'update' '--restart' 'no' "$id" >/dev/null
  policy_changed='yes'
fi
`)
	if image != request.image {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(request.body, &body); err != nil {
			return "", err
		}
		body["Image"], _ = json.Marshal(image)
		var err error
		request.body, err = json.Marshal(body)
		if err != nil {
			return "", err
		}
	}
	caddyPayload(&script, `base64 -d > "$tmp/create.json"`, string(request.body))
	fmt.Fprintf(&script, `result=$(api '--header' 'Content-Type: application/json' '--data-binary' "@$tmp/create.json" %s)
new=$(printf '%%s' "$result" | sed -n 's/.*"Id"[[:space:]]*:[[:space:]]*"\([0-9a-f]*\)".*/\1/p')
[ "${#new}" = '64' ]
[ "$(docker 'inspect' '--format' '{{.Image}}' "$new")" = "$expected" ]
`, privilegeQuote("http://localhost/containers/create?name="+url.QueryEscape(inst.Container)))
	for index, network := range request.networks {
		marker := fmt.Sprintf("%d", index)
		caddyPayload(&script, `base64 -d > "$tmp/network-`+marker+`-before"`, network.before)
		fmt.Fprintf(&script, `printf '%%s%%s%%s' "$(cat "$tmp/network-%s-before")" "$new" %s | api '--header' 'Content-Type: application/json' '--data-binary' '@-' %s >/dev/null
`, marker, privilegeQuote(network.after), privilegeQuote("http://localhost/networks/"+url.PathEscape(network.name)+"/connect"))
	}
	validate := caddyValidateCommand(inst, config)
	fmt.Fprintf(&script, `if [ "$was_running" = 'true' ]; then
  docker 'start' "$new" >/dev/null
  %s
  [ "$(docker 'inspect' '--format' '{{.State.Running}}' "$new")" = 'true' ]
fi
renamed=''; stopped=''; new=''; policy_changed=''
printf '%%s\n' "Previous container retained (stopped): $old"
`, validate)
	return script.String(), nil
}
