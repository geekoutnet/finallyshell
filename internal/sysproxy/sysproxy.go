package sysproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const BackupName = "sysproxy-backup.json"

var (
	ErrUnsupported = errors.New("system proxy is not supported in this desktop environment")
	ErrAddress     = errors.New("system proxy needs a loopback address and a bound port")
	ErrRule        = errors.New("invalid PAC domain or CIDR")
	ErrBackup      = errors.New("invalid system proxy backup")
)

type Settings struct {
	Address string
	SOCKS   bool
	PAC     []string
	PACURL  string
}

type Backend interface {
	Snapshot() (json.RawMessage, error)
	Set(Settings, json.RawMessage) error
	Restore(json.RawMessage) error
}

type Manager struct {
	mu      sync.Mutex
	path    string
	backend Backend
	server  *http.Server
}

func New(path string, backend Backend) *Manager { return &Manager{path: path, backend: backend} }

func (manager *Manager) Apply(settings Settings) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, _, err := proxyAddress(settings.Address); err != nil {
		return err
	}
	settings.PACURL = ""
	var server *http.Server
	if len(settings.PAC) != 0 {
		policy, err := BuildPAC(settings)
		if err != nil {
			return err
		}
		server, settings.PACURL, err = servePAC(policy)
		if err != nil {
			return err
		}
	}
	if err := manager.set(settings); err != nil {
		if server != nil {
			server.Close()
		}
		return err
	}
	manager.stopPAC()
	manager.server = server
	return nil
}

func (manager *Manager) set(settings Settings) error {
	previous, err := manager.backend.Snapshot()
	if err != nil {
		return err
	}
	created := false
	original, err := manager.readBackup()
	if errors.Is(err, os.ErrNotExist) {
		if err := manager.writeBackup(previous); err != nil {
			return err
		}
		original = previous
		created = true
	} else if err != nil {
		return err
	}
	if err := manager.backend.Set(settings, original); err != nil {
		rollback := manager.backend.Restore(previous)
		if rollback == nil && created {
			rollback = os.Remove(manager.path)
		}
		if rollback != nil && !created {
			if fallback := manager.backend.Restore(original); fallback == nil {
				manager.stopPAC()
				rollback = errors.Join(rollback, os.Remove(manager.path))
			} else {
				rollback = errors.Join(rollback, fallback)
			}
		}
		return errors.Join(err, rollback)
	}
	return nil
}

func (manager *Manager) Restore() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	data, err := manager.readBackup()
	if errors.Is(err, os.ErrNotExist) {
		manager.stopPAC()
		return nil
	}
	if err != nil {
		return err
	}
	if err := manager.backend.Restore(data); err != nil {
		return err
	}
	manager.stopPAC()
	return os.Remove(manager.path)
}

func (manager *Manager) stopPAC() {
	if manager.server != nil {
		manager.server.Close()
		manager.server = nil
	}
}

func (manager *Manager) readBackup() (json.RawMessage, error) {
	file, err := os.Open(manager.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err == nil && (len(data) > 1<<20 || !json.Valid(data)) {
		err = ErrBackup
	}
	return data, err
}

func (manager *Manager) writeBackup(data json.RawMessage) error {
	if len(data) > 1<<20 || !json.Valid(data) {
		return ErrBackup
	}
	dir := filepath.Dir(manager.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".sysproxy-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), manager.path)
}

func proxyAddress(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	ip, ipErr := netip.ParseAddr(host)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || ipErr != nil || !ip.IsLoopback() || portErr != nil || number == 0 {
		return "", "", ErrAddress
	}
	return host, strconv.FormatUint(number, 10), nil
}

func servePAC(text string) (*http.Server, string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	addr := listener.Addr().String()
	server := &http.Server{
		ReadHeaderTimeout: 3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       3 * time.Second,
		MaxHeaderBytes:    8192,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != addr || r.URL.Path != "/proxy.pac" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, text)
		}),
	}
	go func() { _ = server.Serve(listener) }()
	return server, "http://" + addr + "/proxy.pac", nil
}
