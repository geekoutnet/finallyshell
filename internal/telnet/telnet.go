package telnet

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"finallyshell/internal/config"
	"finallyshell/internal/termenc"
)

const (
	se           = 240
	nop          = 241
	sb           = 250
	will         = 251
	wont         = 252
	do           = 253
	dont         = 254
	iac          = 255
	echo         = 1
	sga          = 3
	ttype        = 24
	naws         = 31
	maxPrompt    = 1024
	writeTimeout = 3 * time.Second
)

type Options struct {
	Terminal                    string
	Username, Password          string
	LoginPrompt, PasswordPrompt string
	KeepAlive                   time.Duration
	CRAsNewline                 bool
	Encoding                    config.Encoding
}

type Conn struct {
	net                     net.Conn
	reader                  *bufio.Reader
	options                 Options
	done                    chan struct{}
	closeOnce               sync.Once
	mu                      sync.Mutex
	cols, rows              uint16
	local, remote           [256]bool
	lastCR                  bool
	login, password         *regexp.Regexp
	loginDeadline           time.Time
	manual                  atomic.Bool
	prompt, pending, output []byte
	secret                  []byte
	username                []byte
	passwordSent            bool
	readErr                 error
}

func New(raw net.Conn, opts Options) (*Conn, error) {
	c := &Conn{net: raw, reader: bufio.NewReader(raw), options: opts, done: make(chan struct{}), cols: 80, rows: 24}
	if opts.Username != "" {
		if strings.ContainsFunc(opts.Username, unicode.IsControl) || strings.ContainsFunc(opts.Password, unicode.IsControl) {
			return nil, errors.New("invalid Telnet login credentials: control characters are not allowed")
		}
		var err error
		c.username, err = termenc.EncodeLogin(opts.Username, opts.Encoding)
		if err != nil {
			return nil, errors.New("Telnet username cannot be represented in the terminal encoding")
		}
		c.login, err = regexp.Compile(cmp.Or(opts.LoginPrompt, config.DefaultTelnetLoginPrompt))
		if err != nil || c.login.MatchString("") {
			return nil, errors.New("invalid Telnet username prompt")
		}
		if opts.Password != "" {
			c.password, err = regexp.Compile(cmp.Or(opts.PasswordPrompt, config.DefaultTelnetPasswordPrompt))
			if err != nil || c.password.MatchString("") {
				return nil, errors.New("invalid Telnet password prompt")
			}
			c.secret, err = termenc.EncodeLogin(opts.Password, opts.Encoding)
			if err != nil {
				return nil, errors.New("Telnet password cannot be represented in the terminal encoding")
			}
		}
		c.loginDeadline = time.Now().Add(30 * time.Second)
	}
	c.options.Password = ""
	c.options.Username = ""
	if opts.KeepAlive > 0 {
		go c.keepAlive(opts.KeepAlive)
	}
	return c, nil
}

func (c *Conn) Close() (err error) {
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.net.Close()
	})
	return err
}

func (c *Conn) keepAlive(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-tick.C:
			c.mu.Lock()
			err := c.send([]byte{iac, nop})
			c.mu.Unlock()
			if err != nil {
				c.Close()
				return
			}
		}
	}
}

func (c *Conn) send(payload []byte) error {
	if err := c.net.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	count, err := c.net.Write(payload)
	if err == nil && count != len(payload) {
		return io.ErrShortWrite
	}
	return err
}

func escaped(dst []byte, value byte) []byte {
	dst = append(dst, value)
	if value == iac {
		dst = append(dst, iac)
	}
	return dst
}

func (c *Conn) writeData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	wire := make([]byte, 0, len(data))
	for index := 0; index < len(data); index++ {
		switch value := data[index]; value {
		case '\r':
			wire = append(wire, '\r')
			if index+1 < len(data) && (data[index+1] == '\n' || data[index+1] == 0) {
				index++
				wire = append(wire, data[index])
			} else if c.options.CRAsNewline {
				wire = append(wire, '\n')
			} else {
				wire = append(wire, 0)
			}
		case '\n':
			wire = append(wire, '\r', '\n')
		default:
			wire = escaped(wire, value)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send(wire)
}

func (c *Conn) Write(data []byte) (int, error) {
	if len(data) > 0 {
		c.manual.Store(true)
	}
	if err := c.writeData(data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func subnegotiation(dst []byte, option byte, payload []byte) []byte {
	dst = append(dst, iac, sb, option)
	for _, value := range payload {
		dst = escaped(dst, value)
	}
	return append(dst, iac, se)
}

func (c *Conn) windowSize(dst []byte) []byte {
	return subnegotiation(dst, naws, []byte{byte(c.cols >> 8), byte(c.cols), byte(c.rows >> 8), byte(c.rows)})
}

func (c *Conn) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	width, height := uint16(min(max(cols, 1), 65535)), uint16(min(max(rows, 1), 65535))
	if width == c.cols && height == c.rows {
		return nil
	}
	c.cols, c.rows = width, height
	if c.local[naws] {
		return c.send(c.windowSize(nil))
	}
	return nil
}

func (c *Conn) negotiate(command, option byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var reply []byte
	switch command {
	case will:
		if option != echo && option != sga {
			reply = []byte{iac, dont, option}
		} else if !c.remote[option] {
			c.remote[option] = true
			reply = []byte{iac, do, option}
		}
	case wont:
		if c.remote[option] {
			c.remote[option] = false
			reply = []byte{iac, dont, option}
		}
	case do:
		if option != naws && option != ttype && option != sga {
			reply = []byte{iac, wont, option}
		} else if !c.local[option] {
			c.local[option] = true
			reply = []byte{iac, will, option}
			if option == naws {
				reply = c.windowSize(reply)
			}
		}
	case dont:
		if c.local[option] {
			c.local[option] = false
			reply = []byte{iac, wont, option}
		}
	}
	if len(reply) != 0 {
		return c.send(reply)
	}
	return nil
}

func (c *Conn) readSub() error {
	option, err := c.reader.ReadByte()
	if err != nil {
		return err
	}
	var first byte
	size := 0
	for {
		value, err := c.reader.ReadByte()
		if err != nil {
			return err
		}
		if value == iac {
			value, err = c.reader.ReadByte()
			if err != nil {
				return err
			}
			if value == se {
				break
			}
		}
		if size == 0 {
			first = value
		}
		size = min(2, size+1)
	}
	if option == ttype && size == 1 && first == 1 {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.local[ttype] {
			name := cmp.Or(c.options.Terminal, config.DefaultTermType)
			return c.send(subnegotiation(nil, ttype, append([]byte{0}, name...)))
		}
	}
	return nil
}

func (c *Conn) next() (byte, error) {
	for {
		value, err := c.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if value == iac {
			command, err := c.reader.ReadByte()
			if err != nil {
				return 0, err
			}
			switch command {
			case iac:
			case sb:
				if err := c.readSub(); err != nil {
					return 0, err
				}
				continue
			case will, wont, do, dont:
				option, err := c.reader.ReadByte()
				if err != nil {
					return 0, err
				}
				if err := c.negotiate(command, option); err != nil {
					return 0, err
				}
				continue
			default:
				continue
			}
		}
		drop := c.lastCR && value == 0
		c.lastCR = value == '\r'
		if !drop {
			return value, nil
		}
	}
}

func (c *Conn) autoLogin(value byte) error {
	if c.login == nil && c.password == nil {
		return nil
	}
	if c.manual.Load() || time.Now().After(c.loginDeadline) {
		c.login, c.password, c.prompt = nil, nil, nil
		if !c.passwordSent {
			clear(c.secret)
			c.secret = nil
		}
		return nil
	}
	if len(c.prompt) == maxPrompt {
		copy(c.prompt, c.prompt[maxPrompt/2:])
		c.prompt = c.prompt[:maxPrompt/2]
	}
	c.prompt = append(c.prompt, value)
	switch {
	case c.login != nil && c.login.Match(c.prompt):
		c.login, c.prompt = nil, c.prompt[:0]
		return c.writeData(append(c.username, '\r', '\n'))
	case c.login == nil && c.password != nil && c.password.Match(c.prompt):
		c.password, c.prompt = nil, nil
		c.passwordSent = true
		line := append(bytes.Clone(c.secret), '\r', '\n')
		defer clear(line)
		return c.writeData(line)
	}
	return nil
}

func (c *Conn) filter(value byte) {
	if !c.passwordSent {
		c.output = append(c.output, value)
		return
	}
	c.pending = append(c.pending, value)
	for !bytes.HasPrefix(c.secret, c.pending) {
		c.output = append(c.output, c.pending[0])
		c.pending = c.pending[1:]
	}
	if len(c.pending) == len(c.secret) {
		c.pending = c.pending[:0]
	}
}

func (c *Conn) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for c.readErr == nil && (len(c.output) == 0 || len(c.output) < len(dst) && c.reader.Buffered() > 0) {
		value, err := c.next()
		if err == nil {
			c.filter(value)
			err = c.autoLogin(value)
		}
		if err != nil {
			c.pending = nil
			clear(c.secret)
			c.secret = nil
			c.readErr = err
		}
	}
	if len(c.output) == 0 {
		return 0, c.readErr
	}
	count := copy(dst, c.output)
	c.output = c.output[count:]
	return count, nil
}
