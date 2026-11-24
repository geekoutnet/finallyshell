package zmodem

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
)

const (
	zdle      = 0x18
	hexHeader = 'B'
	bin16     = 'A'
	bin32     = 'C'
	zcrce     = 'h'
	zcrcg     = 'i'
	zcrcq     = 'j'
	zcrcw     = 'k'
)

const (
	zrqinit byte = iota
	zrinit
	zsinit
	zack
	zfile
	zskip
	znak
	zabort
	zfin
	zrpos
	zdata
	zeof
	zferr
	zcrc
	zchallenge
)

const CancelSequence = "\x18\x18\x18\x18\x18\x18\x18\x18\b\b\b\b\b\b\b\b"

var (
	errFrame    = errors.New("invalid ZMODEM frame")
	errChecksum = errors.New("invalid ZMODEM checksum")
	ErrCanceled = errors.New("ZMODEM canceled by peer")
	ErrTimeout  = errors.New("ZMODEM peer timed out")
)

type header struct {
	kind, format byte
	value        uint32
}

func crc16(payload []byte) uint16 {
	var crc uint16
	for _, value := range payload {
		crc ^= uint16(value) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func checksum(dst, payload []byte, format byte) []byte {
	if format == bin32 {
		return binary.LittleEndian.AppendUint32(dst, crc32.ChecksumIEEE(payload))
	}
	return binary.BigEndian.AppendUint16(dst, crc16(payload))
}

func appendEscaped(dst, payload []byte) []byte {
	for _, value := range payload {
		switch {
		case value == 0x7f:
			dst = append(dst, zdle, 'l')
		case value == 0xff:
			dst = append(dst, zdle, 'm')
		case value&0x60 == 0:
			dst = append(dst, zdle, value^0x40)
		default:
			dst = append(dst, value)
		}
	}
	return dst
}

func encodeHeader(h header) []byte {
	frame := binary.LittleEndian.AppendUint32([]byte{h.kind}, h.value)
	frame = checksum(frame, frame, h.format)
	if h.format == hexHeader {
		wire := append([]byte("**\x18B"), hex.EncodeToString(frame)...)
		wire = append(wire, '\r', 0x8a)
		if h.kind != zfin && h.kind != zack {
			wire = append(wire, 0x11)
		}
		return wire
	}
	return appendEscaped([]byte{'*', zdle, h.format}, frame)
}

func encodeData(payload []byte, end, format byte) []byte {
	wire := appendEscaped(make([]byte, 0, len(payload)+8), payload)
	wire = append(wire, zdle, end)
	check := checksum(nil, append(payload[:len(payload):len(payload)], end), format)
	wire = appendEscaped(wire, check)
	if end == zcrcw {
		wire = append(wire, 0x11)
	}
	return wire
}

func flow(value byte) bool { return value&0x7f == 0x11 || value&0x7f == 0x13 }

func readNonFlow(reader io.ByteReader) (byte, error) {
	for {
		value, err := reader.ReadByte()
		if err != nil || !flow(value) {
			return value, err
		}
	}
}

func readEscaped(reader io.ByteReader) (value byte, end byte, err error) {
	value, err = readNonFlow(reader)
	if err != nil || value != zdle {
		return value, 0, err
	}
	for count := 1; ; count++ {
		value, err = readNonFlow(reader)
		if err != nil {
			return 0, 0, err
		}
		switch {
		case value == zdle && count >= 4:
			return 0, 0, ErrCanceled
		case value == zdle:
			continue
		case value >= zcrce && value <= zcrcw:
			return 0, value, nil
		case value == 'l':
			return 0x7f, 0, nil
		case value == 'm':
			return 0xff, 0, nil
		case value&0x60 == 0x40:
			return value ^ 0x40, 0, nil
		default:
			return 0, 0, errFrame
		}
	}
}

func readHeader(reader io.ByteReader) (header, error) {
	var pads bool
	var cans int
	for range 1 << 20 {
		value, err := readNonFlow(reader)
		if err != nil {
			return header{}, err
		}
		if value == '*' || value == 0xaa {
			pads, cans = true, 0
			continue
		}
		if value == zdle {
			cans++
			if cans >= 5 {
				return header{}, ErrCanceled
			}
			continue
		}
		if pads && cans == 1 && (value == hexHeader || value == bin16 || value == bin32) {
			return decodeHeader(reader, value)
		}
		pads, cans = false, 0
	}
	return header{}, errFrame
}

func decodeHeader(reader io.ByteReader, format byte) (header, error) {
	var frame [9]byte
	size := 7
	if format == bin32 {
		size = 9
	}
	if format == hexHeader {
		var encoded [14]byte
		for index := range encoded {
			value, err := readNonFlow(reader)
			if err != nil {
				return header{}, err
			}
			encoded[index] = value
		}
		if _, err := hex.Decode(frame[:7], encoded[:]); err != nil {
			return header{}, errFrame
		}
		for _, expected := range []byte{'\r', '\n'} {
			value, err := readNonFlow(reader)
			if err != nil {
				return header{}, err
			}
			if value&0x7f != expected {
				return header{}, errFrame
			}
		}
	} else if err := readCheckBytes(reader, frame[:size]); err != nil {
		return header{}, err
	}
	if !validChecksum(frame[:size], 5, format) {
		return header{}, errChecksum
	}
	return header{kind: frame[0], format: format, value: binary.LittleEndian.Uint32(frame[1:5])}, nil
}

func readCheckBytes(reader io.ByteReader, target []byte) error {
	for index := range target {
		value, end, err := readEscaped(reader)
		if err != nil {
			return err
		}
		if end != 0 {
			return errFrame
		}
		target[index] = value
	}
	return nil
}

func validChecksum(packet []byte, size int, format byte) bool {
	if format == bin32 {
		return crc32.ChecksumIEEE(packet[:size]) == binary.LittleEndian.Uint32(packet[size:])
	}
	return crc16(packet[:size]) == binary.BigEndian.Uint16(packet[size:])
}

func readData(reader io.ByteReader, format byte) ([]byte, byte, error) {
	packet := make([]byte, 0, 8192+5)
	for len(packet) < 64<<10 {
		value, end, err := readEscaped(reader)
		if err != nil {
			return nil, 0, err
		}
		if end == 0 {
			packet = append(packet, value)
			continue
		}
		size := len(packet)
		packet = append(packet, end, 0, 0)
		if format == bin32 {
			packet = append(packet, 0, 0)
		}
		if err := readCheckBytes(reader, packet[size+1:]); err != nil {
			return nil, end, err
		}
		if !validChecksum(packet, size+1, format) {
			return nil, end, errChecksum
		}
		return packet[:size], end, nil
	}
	return nil, 0, errFrame
}
