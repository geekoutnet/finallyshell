package dbclient

import (
	"slices"
	"testing"
)

func TestDiscoverEndpoints(t *testing.T) {
	listeners := `State Recv-Q Send-Q Local Address:Port Peer Address:Port Process
LISTEN 0 128 0.0.0.0:3306 0.0.0.0:* users:(("mariadbd",pid=722,fd=20))
LISTEN 0 511 [::1]:16379 [::]:* users:(("redis-server",pid=110,fd=7))
LISTEN 0 100 *:9000 *:* users:(("sample-worker",pid=31,fd=6))
tcp 0 0 127.0.0.1:5432 0.0.0.0:* LISTEN
LISTEN 0 100 127.0.0.1:70000 *:*`
	docker := "0.0.0.0:3306->3306/tcp, :::25432->5432/tcp\r\n6379/tcp, [::1]:26379->6379/tcp, 0.0.0.0:56379->6379/udp\n"
	want := []Endpoint{{MySQL, "127.0.0.1", 3306}, {Redis, "::1", 16379}, {Postgres, "127.0.0.1", 5432}, {Postgres, "::1", 25432}, {Redis, "::1", 26379}}
	if got := Discover(listeners, docker); !slices.Equal(got, want) {
		t.Fatalf("%+v", got)
	}
	if DockerScript != `'docker' 'ps' '--format' '{{.Ports}}'` {
		t.Fatal(DockerScript)
	}
}
