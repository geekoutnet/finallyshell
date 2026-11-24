package dbclient

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Engine string

const (
	MySQL    Engine = "mysql"
	Postgres Engine = "postgres"
	Redis    Engine = "redis"
	PageSize        = 200
)

func (engine Engine) Port() int {
	switch engine {
	case MySQL:
		return 3306
	case Postgres:
		return 5432
	case Redis:
		return 6379
	}
	return 0
}

type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Engine   Engine `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
	TLS      string `json:"tls"`
	Remember bool   `json:"remember,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

var (
	ErrProfile    = errors.New("invalid database connection fields")
	ErrInput      = errors.New("invalid or unsupported input")
	ErrSecret     = errors.New("credential and session-control statements are not supported")
	ErrConnect    = errors.New("database connection failed")
	ErrReadOnly   = errors.New("statement is not allowed in read-only mode")
	ErrResultSize = errors.New("result size limit exceeded")
)

func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" || p.Engine.Port() == 0 || p.Port < 1 || p.Port > 65535 || !validHost(p.Host) ||
		(p.Engine != Redis && strings.TrimSpace(p.User) == "") ||
		(p.TLS != "disable" && p.TLS != "require" && p.TLS != "verify-full") ||
		strings.IndexFunc(p.Name+p.User+p.Database, unicode.IsControl) >= 0 {
		return ErrProfile
	}
	if p.Engine == Redis && p.Database != "" {
		number, err := strconv.ParseUint(p.Database, 10, 31)
		if err != nil || number > 1<<31-1 {
			return ErrProfile
		}
	}
	return nil
}

func validHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}
	if len(host) > 253 {
		return false
	}
	for part := range strings.SplitSeq(strings.TrimSuffix(host, "."), ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, char := range part {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func (p Profile) address() string { return net.JoinHostPort(p.Host, strconv.Itoa(p.Port)) }

type Dialer func(context.Context, string, string) (net.Conn, error)

type Namespace struct{ Database, Schema string }
type Object struct {
	Database, Namespace, Name string
	View                      bool
}
type Page struct {
	Index int
	Sort  string
	Desc  bool
	Where string
}
type Cell struct {
	Text         string
	Null, Binary bool
}
type Result struct {
	Columns       []string
	Rows          [][]Cell
	More          bool
	Affected      int64
	AffectedKnown bool
	Duration      time.Duration
	Verb          string
}
type Structure struct {
	Columns, Keys, Indexes Result
	Create                 string
	Reconstructed          bool
}
type KeyPage struct {
	Keys []string
	Next uint64
}
type KeyView struct {
	Type       string
	TTL        int64
	Data       Result
	Editable   bool
	Next       uint64
	StreamNext string
}
type KeyCursor struct {
	Position uint64
	Stream   string
}
type KeyChange struct {
	Keys                        []string
	Type, Field, Value          string
	Delete, Confirmed, ReadOnly bool
}
type Batch struct {
	Text, Database      string
	ReadOnly, Confirmed bool
}

type Client interface {
	Close() error
	Namespaces(context.Context) ([]Namespace, error)
	Objects(context.Context, Namespace) ([]Object, error)
	Browse(context.Context, Object, Page) (Result, error)
	Structure(context.Context, Object) (Structure, error)
	Scan(context.Context, string, uint64) (KeyPage, error)
	Key(context.Context, string, KeyCursor) (KeyView, error)
	ChangeKey(context.Context, KeyChange) error
	Run(context.Context, Batch) ([]Result, error)
}

type unsupported struct{}

func (unsupported) Namespaces(context.Context) ([]Namespace, error)      { return nil, ErrInput }
func (unsupported) Objects(context.Context, Namespace) ([]Object, error) { return nil, ErrInput }
func (unsupported) Browse(context.Context, Object, Page) (Result, error) { return Result{}, ErrInput }
func (unsupported) Structure(context.Context, Object) (Structure, error) {
	return Structure{}, ErrInput
}
func (unsupported) Scan(context.Context, string, uint64) (KeyPage, error) { return KeyPage{}, ErrInput }
func (unsupported) Key(context.Context, string, KeyCursor) (KeyView, error) {
	return KeyView{}, ErrInput
}
func (unsupported) ChangeKey(context.Context, KeyChange) error { return ErrInput }

func cell(value any, binary bool) Cell {
	switch value := value.(type) {
	case nil:
		return Cell{Null: true}
	case []byte:
		if binary {
			return Cell{Text: "0x" + hex.EncodeToString(value), Binary: true}
		}
		return Cell{Text: string(value)}
	case time.Time:
		return Cell{Text: value.Format(time.RFC3339Nano)}
	default:
		return Cell{Text: fmt.Sprint(value)}
	}
}

type redactor string

func (secret redactor) text(text string) string {
	if strings.Contains(text, "PRIVATE KEY-----") {
		return "•••"
	}
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, string(secret), "•••")
}

func (secret redactor) err(err error) error {
	if err == nil {
		return nil
	}
	if clean := secret.text(err.Error()); clean != err.Error() {
		return errors.New(clean)
	}
	return err
}

func (secret redactor) result(result Result) Result {
	for index, name := range result.Columns {
		result.Columns[index] = secret.text(name)
	}
	for _, row := range result.Rows {
		for index := range row {
			row[index].Text = secret.text(row[index].Text)
		}
	}
	return result
}

func (secret redactor) contains(text string) bool {
	return strings.Contains(text, "PRIVATE KEY-----") || secret != "" && strings.Contains(text, string(secret))
}
