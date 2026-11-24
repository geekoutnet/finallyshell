package serial

import (
	"errors"
	"io"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/config"
)

var (
	ErrPort        = errors.New("invalid serial device name")
	ErrSettings    = errors.New("invalid serial settings")
	ErrUnsupported = errors.New("serial ports are not supported on this platform")
	comName        = regexp.MustCompile(`^COM[1-9][0-9]{0,8}$`)
	linuxName      = regexp.MustCompile(`^tty(?:S|USB|ACM)[0-9]+$`)
)

func ValidPort(platform, name string) bool {
	if strings.ContainsAny(name, "\x00\r\n") {
		return false
	}
	switch platform {
	case "windows":
		return comName.MatchString(strings.ToUpper(name))
	case "linux", "darwin":
		base, ok := strings.CutPrefix(name, "/dev/")
		return ok && base != "" && !strings.Contains(base, "/") && base != "." && base != ".."
	default:
		return name != ""
	}
}

func portNames(platform string, names []string) []string {
	var ports []string
	for _, name := range names {
		switch platform {
		case "windows":
			name = strings.ToUpper(name)
		case "linux":
			if !linuxName.MatchString(name) {
				continue
			}
			name = "/dev/" + name
		case "darwin":
			if !strings.HasPrefix(name, "cu.") || name == "cu." {
				continue
			}
			name = "/dev/" + name
		}
		if ValidPort(platform, name) {
			ports = append(ports, name)
		}
	}
	if platform == "windows" {
		slices.SortFunc(ports, func(first, second string) int {
			a, _ := strconv.Atoi(first[3:])
			b, _ := strconv.Atoi(second[3:])
			return a - b
		})
	} else {
		slices.Sort(ports)
	}
	return slices.Compact(ports)
}

func Ports() ([]string, error) {
	names, err := nativePorts()
	return portNames(runtime.GOOS, names), err
}

func normalize(cfg config.SerialConfig) (config.SerialConfig, error) {
	if cfg.Baud == 0 {
		cfg.Baud = 9600
	}
	if cfg.DataBits == 0 {
		cfg.DataBits = 8
	}
	if cfg.StopBits == 0 {
		cfg.StopBits = 1
	}
	if cfg.Baud < 1 || cfg.Baud > 100_000_000 || cfg.DataBits < 5 || cfg.DataBits > 8 ||
		!slices.Contains([]string{"", "none", "even", "odd"}, cfg.Parity) ||
		!slices.Contains([]float64{1, 1.5, 2}, cfg.StopBits) ||
		!slices.Contains([]string{"", "none", "rtscts", "xonxoff"}, cfg.Flow) ||
		!slices.Contains([]string{"", "cr", "lf", "crlf"}, cfg.LineEnding) {
		return cfg, ErrSettings
	}
	return cfg, nil
}

func Validate(platform string, cfg config.SerialConfig) error {
	cfg, err := normalize(cfg)
	if err != nil {
		return err
	}
	if cfg.StopBits == 1.5 && (platform != "windows" || cfg.DataBits != 5) ||
		platform == "windows" && cfg.StopBits == 2 && cfg.DataBits == 5 {
		return ErrSettings
	}
	return nil
}

func Open(name string, cfg config.SerialConfig) (io.ReadWriteCloser, error) {
	if !ValidPort(runtime.GOOS, name) {
		return nil, ErrPort
	}
	if err := Validate(runtime.GOOS, cfg); err != nil {
		return nil, err
	}
	cfg, _ = normalize(cfg)
	port, err := openPort(name, cfg)
	if err != nil {
		return nil, err
	}
	return &serialStream{port: port, ending: lineEnding(cfg.LineEnding)}, nil
}

type serialStream struct {
	port       io.ReadWriteCloser
	ending     string
	previousCR bool
}

func lineEnding(ending string) string {
	switch ending {
	case "lf":
		return "\n"
	case "crlf":
		return "\r\n"
	default:
		return "\r"
	}
}

func (s *serialStream) Read(dst []byte) (int, error) { return s.port.Read(dst) }
func (s *serialStream) Close() error                 { return s.port.Close() }

func (s *serialStream) Write(data []byte) (int, error) {
	payload := make([]byte, 0, len(data))
	for _, value := range data {
		switch {
		case value == '\r' || value == '\n' && !s.previousCR:
			payload = append(payload, s.ending...)
		case value != '\n':
			payload = append(payload, value)
		}
		s.previousCR = value == '\r'
	}
	if len(payload) > 0 {
		count, err := s.port.Write(payload)
		if err != nil {
			return 0, err
		}
		if count != len(payload) {
			return 0, io.ErrShortWrite
		}
	}
	return len(data), nil
}
