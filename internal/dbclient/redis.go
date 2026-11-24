package dbclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

type redisClient struct {
	unsupported
	profile  Profile
	password redactor
	dial     Dialer
}

func openRedis(ctx context.Context, profile Profile, password string, dial Dialer) (Client, error) {
	client := &redisClient{profile: profile, password: redactor(password), dial: dial}
	conn := client.connection(ctx)
	defer conn.Close()
	if err := conn.Ping(ctx).Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", ErrConnect, client.password.text(err.Error()))
	}
	return client, nil
}

func (c *redisClient) Close() error { return nil }
func (c *redisClient) connection(life context.Context) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: c.profile.address(), Username: c.profile.User, Password: string(c.password), DB: c.profile.redisDB(),
		Protocol: 2, DisableIdentity: true, MaxRetries: -1, PoolSize: 1, ContextTimeoutEnabled: true,
		MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := c.dial(ctx, network, address)
			if err != nil {
				return nil, err
			}
			raw := conn
			stop := context.AfterFunc(life, func() { raw.Close() })
			if cfg := c.profile.tlsConfig(); cfg != nil {
				stream := tls.Client(conn, cfg)
				if err := stream.HandshakeContext(ctx); err != nil {
					stop()
					conn.Close()
					return nil, err
				}
				conn = stream
			}
			return &redisConn{Conn: conn, stop: stop}, nil
		},
	})
}

type redisConn struct {
	net.Conn
	stop func() bool
}

func (c *redisConn) Close() error { c.stop(); return c.Conn.Close() }

func (c *redisClient) Scan(ctx context.Context, pattern string, cursor uint64) (KeyPage, error) {
	if c.password.contains(pattern) {
		return KeyPage{}, ErrSecret
	}
	conn := c.connection(ctx)
	defer conn.Close()
	keys, next, err := conn.Scan(ctx, cursor, pattern, PageSize).Result()
	filtered := keys[:0]
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if !c.password.contains(key) && !seen[key] {
			filtered = append(filtered, key)
			seen[key] = true
		}
	}
	return KeyPage{Keys: filtered, Next: next}, c.password.err(err)
}

func redisCell(text string) Cell {
	return cell([]byte(text), !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0)
}

func (c *redisClient) Key(ctx context.Context, key string, cursor KeyCursor) (KeyView, error) {
	if c.password.contains(key) {
		return KeyView{}, ErrSecret
	}
	conn := c.connection(ctx)
	defer conn.Close()
	pipe := conn.Pipeline()
	kind, ttl := pipe.Type(ctx, key), pipe.PTTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return KeyView{}, c.password.err(err)
	}
	view := KeyView{Type: kind.Val(), TTL: ttl.Val().Milliseconds()}
	if ttl.Val() < 0 {
		view.TTL = int64(ttl.Val())
	}
	var err error
	if view.Type == "stream" {
		view.Data, view.StreamNext, err = redisStream(ctx, conn, key, cursor.Stream)
	} else {
		view.Data, view.Next, view.Editable, err = redisKeyData(ctx, conn, key, view.Type, cursor.Position)
	}
	view.Data = c.password.result(view.Data)
	if view.Type == "string" && len(view.Data.Rows) != 0 && strings.Contains(view.Data.Rows[0][0].Text, "•••") {
		view.Editable = false
	}
	return view, c.password.err(err)
}

func redisKeyData(ctx context.Context, conn *redis.Client, key, kind string, cursor uint64) (Result, uint64, bool, error) {
	var result Result
	var next uint64
	var err error
	appendPair := func(values []string) {
		for index := 0; index+1 < len(values); index += 2 {
			result.Rows = append(result.Rows, []Cell{redisCell(values[index]), redisCell(values[index+1])})
		}
	}
	switch kind {
	case "none":
	case "string":
		var value string
		value, err = conn.GetRange(ctx, key, 0, 1<<20).Result()
		result.Columns = []string{"Value"}
		result.More = len(value) > 1<<20
		if result.More {
			value = value[:1<<20]
		}
		result.Rows = [][]Cell{{redisCell(value)}}
		return result, 0, !result.More && !result.Rows[0][0].Binary, err
	case "hash", "zset":
		var values []string
		if kind == "hash" {
			values, next, err = conn.HScan(ctx, key, cursor, "", PageSize).Result()
			result.Columns = []string{"Field", "Value"}
		} else {
			values, next, err = conn.ZScan(ctx, key, cursor, "", PageSize).Result()
			result.Columns = []string{"Member", "Score"}
		}
		appendPair(values)
	case "list":
		if cursor > 1<<40 {
			return Result{}, 0, false, ErrInput
		}
		var values []string
		values, err = conn.LRange(ctx, key, int64(cursor), int64(cursor)+PageSize).Result()
		result.Columns = []string{"Index", "Value"}
		if len(values) > PageSize {
			values = values[:PageSize]
			next = cursor + PageSize
		}
		for index, value := range values {
			result.Rows = append(result.Rows, []Cell{{Text: strconv.FormatUint(cursor+uint64(index), 10)}, redisCell(value)})
		}
	case "set":
		var values []string
		values, next, err = conn.SScan(ctx, key, cursor, "", PageSize).Result()
		result.Columns = []string{"Member"}
		for _, value := range values {
			result.Rows = append(result.Rows, []Cell{redisCell(value)})
		}
	}
	return result, next, kind == "hash", err
}

func redisStream(ctx context.Context, conn *redis.Client, key, first string) (Result, string, error) {
	if first == "" {
		first = "-"
	} else if strings.IndexFunc(first, func(char rune) bool { return char != '-' && (char < '0' || char > '9') }) >= 0 {
		return Result{}, "", ErrInput
	}
	values, err := conn.XRangeN(ctx, key, first, "+", PageSize+1).Result()
	result := Result{Columns: []string{"ID", "Fields"}}
	var next string
	if len(values) > PageSize {
		next = values[PageSize].ID
	}
	for _, message := range values[:min(len(values), PageSize)] {
		encoded, _ := json.Marshal(message.Values)
		result.Rows = append(result.Rows, []Cell{{Text: message.ID}, redisCell(string(encoded))})
	}
	return result, next, err
}

func (c *redisClient) ChangeKey(ctx context.Context, change KeyChange) error {
	if err := authorize(change.ReadOnly, change.Confirmed, false); err != nil {
		return err
	}
	if len(change.Keys) == 0 || len(change.Keys) > 1000 || len(change.Value) > 1<<20 || c.password.contains(change.Value) || c.password.contains(change.Field) {
		return ErrInput
	}
	for _, key := range change.Keys {
		if c.password.contains(key) {
			return ErrSecret
		}
	}
	conn := c.connection(ctx)
	defer conn.Close()
	if change.Delete {
		return c.password.err(conn.Del(ctx, change.Keys...).Err())
	}
	if len(change.Keys) != 1 || change.Type != "hash" && change.Type != "string" {
		return ErrInput
	}
	key := change.Keys[0]
	err := conn.Watch(ctx, func(tx *redis.Tx) error {
		kind, err := tx.Type(ctx, key).Result()
		if err != nil {
			return err
		}
		if kind != change.Type {
			return ErrInput
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			if kind == "string" {
				pipe.SetArgs(ctx, key, change.Value, redis.SetArgs{Mode: "XX", KeepTTL: true})
			} else {
				pipe.HSet(ctx, key, change.Field, change.Value)
			}
			return nil
		})
		return err
	}, key)
	return c.password.err(err)
}

func (c *redisClient) Run(ctx context.Context, batch Batch) ([]Result, error) {
	if c.password.contains(batch.Text) {
		return nil, ErrSecret
	}
	statements, err := RedisStatements(batch.Text)
	if err != nil {
		return nil, err
	}
	for _, statement := range statements {
		if err := authorize(batch.ReadOnly, batch.Confirmed, statement.ReadOnly); err != nil {
			return nil, err
		}
	}
	conn := c.connection(ctx)
	defer conn.Close()
	results := make([]Result, 0, len(statements))
	for _, statement := range statements {
		args := make([]any, len(statement.Args))
		for index, arg := range statement.Args {
			args[index] = arg
		}
		start := time.Now()
		value, err := conn.Do(ctx, args...).Result()
		result := Result{Columns: []string{"Reply"}, Verb: statement.Args[0], Duration: time.Since(start)}
		if values, ok := value.([]any); ok {
			result.More = len(values) > PageSize
			for _, value := range values[:min(len(values), PageSize)] {
				result.Rows = append(result.Rows, []Cell{redisReply(value)})
			}
		} else if err == nil || errors.Is(err, redis.Nil) {
			result.Rows = [][]Cell{{redisReply(value)}}
		}
		results = append(results, c.password.result(result))
		if err != nil && !errors.Is(err, redis.Nil) {
			return results, c.password.err(err)
		}
	}
	return results, nil
}

func redisReply(value any) Cell {
	if text, ok := value.(string); ok {
		return redisCell(text)
	}
	if _, ok := value.([]any); ok {
		if encoded, err := json.Marshal(value); err == nil {
			return redisCell(string(encoded))
		}
	}
	return cell(value, false)
}
