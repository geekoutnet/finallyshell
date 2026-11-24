package dbclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

func TestDriversUseSSHDialer(t *testing.T) {
	for _, engine := range []Engine{MySQL, Postgres, Redis} {
		t.Run(string(engine), func(t *testing.T) {
			p := Profile{Name: "Example", Engine: engine, Host: "database.invalid", Port: engine.Port(), User: "viewer", TLS: "disable"}
			calls := make(chan string, 10)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := Open(ctx, p, "example-credential", func(_ context.Context, network, address string) (net.Conn, error) {
				calls <- network + ":" + address
				client, server := net.Pipe()
				server.Close()
				return client, nil
			})
			if err == nil {
				t.Fatal("expected the fake peer to fail authentication")
			}
			select {
			case got := <-calls:
				if got != "tcp:"+p.address() {
					t.Fatal(got)
				}
			default:
				t.Fatalf("driver did not dial over SSH: %v", err)
			}
		})
	}
}

func TestDriversAuthenticateOverSSH(t *testing.T) {
	for _, test := range []struct {
		engine Engine
		peer   func(net.Conn) error
	}{{MySQL, mysqlPeer}, {Postgres, postgresPeer}, {Redis, redisPeer}} {
		t.Run(string(test.engine), func(t *testing.T) {
			profile := Profile{Name: "Example", Engine: test.engine, Host: "remote-db.invalid", Port: test.engine.Port(), User: "viewer", TLS: "disable"}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			finished := make(chan error, 2)
			client, err := Open(ctx, profile, "", func(_ context.Context, network, address string) (net.Conn, error) {
				if network != "tcp" || address != profile.address() {
					return nil, fmt.Errorf("unexpected dial target %s %s", network, address)
				}
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					server.SetDeadline(time.Now().Add(2 * time.Second))
					finished <- test.peer(server)
				}()
				return client, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func mysqlPeer(conn net.Conn) error {
	capabilities := uint32(1 | 1<<9 | 1<<13 | 1<<15 | 1<<19)
	var greeting bytes.Buffer
	greeting.WriteString("\x0a8.0.40\x00\x01\x00\x00\x00abcdefgh\x00")
	binary.Write(&greeting, binary.LittleEndian, uint16(capabilities))
	greeting.WriteString("\x2d\x02\x00")
	binary.Write(&greeting, binary.LittleEndian, uint16(capabilities>>16))
	greeting.WriteByte(21)
	greeting.Write(make([]byte, 10))
	greeting.WriteString("ijklmnopqrst\x00mysql_native_password\x00")
	write := func(sequence byte, payload []byte) error {
		_, err := conn.Write(append([]byte{byte(len(payload)), 0, 0, sequence}, payload...))
		return err
	}
	read := func() ([]byte, error) {
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return nil, err
		}
		payload := make([]byte, int(header[0])|int(header[1])<<8|int(header[2])<<16)
		_, err := io.ReadFull(conn, payload)
		return payload, err
	}
	if err := write(0, greeting.Bytes()); err != nil {
		return err
	}
	if _, err := read(); err != nil {
		return err
	}
	ok := []byte{0, 0, 0, 2, 0, 0, 0}
	if err := write(2, ok); err != nil {
		return err
	}
	for {
		command, err := read()
		if err != nil {
			return err
		}
		if len(command) == 0 {
			return io.ErrUnexpectedEOF
		}
		switch command[0] {
		case 0x0e:
			return write(1, ok)
		case 0x03:
			if err := write(1, ok); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected MySQL command %x", command[0])
		}
	}
}

func postgresPeer(conn net.Conn) error {
	backend := pgproto3.NewBackend(conn, conn)
	message, err := backend.ReceiveStartupMessage()
	if err != nil {
		return err
	}
	startup, ok := message.(*pgproto3.StartupMessage)
	if !ok || startup.Parameters["user"] != "viewer" || startup.Parameters["database"] != "postgres" {
		return fmt.Errorf("unexpected startup message")
	}
	backend.Send(&pgproto3.AuthenticationOk{})
	backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.4"})
	backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if err := backend.Flush(); err != nil {
		return err
	}
	messageAfterStartup, err := backend.Receive()
	if err != nil {
		return err
	}
	if query, ok := messageAfterStartup.(*pgproto3.Query); !ok || query.String != "-- ping" {
		return fmt.Errorf("unexpected PostgreSQL ping")
	}
	backend.Send(&pgproto3.EmptyQueryResponse{})
	backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	return backend.Flush()
}

func redisPeer(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "*") {
			return fmt.Errorf("unexpected Redis command framing")
		}
		count, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
		var args []string
		for range count {
			line, err = reader.ReadString('\n')
			if err != nil {
				return err
			}
			if !strings.HasPrefix(line, "$") {
				return fmt.Errorf("unexpected Redis arg framing")
			}
			size, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
			data := make([]byte, size+2)
			if _, err := io.ReadFull(reader, data); err != nil {
				return err
			}
			args = append(args, string(data[:size]))
		}
		if len(args) == 0 {
			return io.ErrUnexpectedEOF
		}
		reply := "-ERR unknown command\r\n"
		if strings.EqualFold(args[0], "PING") {
			_, err := io.WriteString(conn, "+PONG\r\n")
			return err
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return err
		}
	}
}

func TestSSHConnectionHasIndependentDeadlines(t *testing.T) {
	client, server := net.Pipe()
	conn := withDeadlines(client)
	defer conn.Close()
	defer server.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected deadline")
	}
	conn.Close()
	if _, err := server.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("remote channel not closed: %v", err)
	}
}
