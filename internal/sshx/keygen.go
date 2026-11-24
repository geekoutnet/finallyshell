package sshx

import (
	"bytes"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

var (
	keyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,99}$`)
	errInvalidKey  = errors.New("invalid or mismatched SSH key")
)

func ValidKeyName(name string) bool {
	lower := strings.ToLower(name)
	stem, _, _ := strings.Cut(lower, ".")
	if strings.Contains("|con|prn|aux|nul|", "|"+stem+"|") || len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && stem[3] >= '0' && stem[3] <= '9' {
		return false
	}
	return keyNamePattern.MatchString(name) && !strings.HasSuffix(lower, ".pub") && !strings.HasSuffix(lower, ".") && !strings.Contains(lower, ".bak-") && lower != "authorized_keys" && lower != "authorized_keys2" && lower != "config" && lower != "known_hosts" && lower != "known_hosts.old"
}

func ValidKeyComment(comment string) bool {
	return len(comment) <= 512 && !strings.ContainsFunc(comment, unicode.IsControl)
}

type KeyInfo struct {
	Public     ssh.PublicKey
	Comment    string
	Encrypted  bool
	Unverified bool
}

func (key KeyInfo) Fingerprint() string {
	if key.Public == nil {
		return ""
	}
	return ssh.FingerprintSHA256(key.Public)
}

func (key KeyInfo) Bits() int {
	if certificate, ok := key.Public.(*ssh.Certificate); ok {
		key.Public = certificate.Key
	}
	if public, ok := key.Public.(ssh.CryptoPublicKey); ok {
		switch value := public.CryptoPublicKey().(type) {
		case ed25519.PublicKey:
			return len(value) * 8
		case *rsa.PublicKey:
			return value.N.BitLen()
		case *ecdsa.PublicKey:
			return value.Params().BitSize
		case *dsa.PublicKey:
			return value.P.BitLen()
		}
	}
	return 0
}

func (key KeyInfo) Authorized() string {
	if key.Public == nil {
		return ""
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.Public)))
	if key.Comment != "" {
		line += " " + key.Comment
	}
	return line + "\n"
}

func (key KeyInfo) RandomArt() string {
	if key.Public == nil {
		return ""
	}
	const palette = " .o+=*BOX@%&#/^SE"
	board := [9][17]int{}
	column, row := 8, 4
	digest := sha256.Sum256(key.Public.Marshal())
	for _, value := range digest {
		for step := 0; step < 4; step++ {
			column = min(16, max(0, column+2*int(value&1)-1))
			row = min(8, max(0, row+2*int((value>>1)&1)-1))
			board[row][column] = min(len(palette)-3, board[row][column]+1)
			value >>= 2
		}
	}
	board[4][8] = len(palette) - 2
	board[row][column] = len(palette) - 1
	var art strings.Builder
	art.WriteString("+-----------------+\n")
	for _, line := range board {
		art.WriteByte('|')
		for _, square := range line {
			art.WriteByte(palette[square])
		}
		art.WriteString("|\n")
	}
	art.WriteString("+----[SHA256]-----+")
	return art.String()
}

func GenerateKey(kind, comment, passphrase string) ([]byte, []byte, error) {
	if !ValidKeyComment(comment) {
		return nil, nil, errInvalidKey
	}
	var private any
	var err error
	switch kind {
	case "ed25519":
		_, private, err = ed25519.GenerateKey(rand.Reader)
	case "rsa":
		private, err = rsa.GenerateKey(rand.Reader, 4096)
	default:
		return nil, nil, errInvalidKey
	}
	if err != nil {
		return nil, nil, err
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, nil, err
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(private, comment)
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(private, comment, []byte(passphrase))
	}
	if err != nil {
		return nil, nil, err
	}
	key := KeyInfo{Public: signer.PublicKey(), Comment: comment}
	return pem.EncodeToMemory(block), []byte(key.Authorized()), nil
}

func InspectKey(path string) (KeyInfo, error) {
	data, err := readKeyFile(path)
	if err != nil {
		return KeyInfo{}, err
	}
	defer clear(data)
	return inspectKey(path, data, "")
}

func ImportKey(path, passphrase string) ([]byte, []byte, error) {
	data, err := readKeyFile(path)
	if err != nil {
		return nil, nil, err
	}
	key, err := inspectKey(path, data, passphrase)
	if err == nil && key.Encrypted && passphrase == "" {
		err = ErrNeedPassphrase
	}
	if err != nil || key.Public == nil {
		clear(data)
		return nil, nil, errors.Join(err, errInvalidKey)
	}
	if key.Encrypted && !bytes.HasPrefix(data, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
		raw, err := ssh.ParseRawPrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			clear(data)
			return nil, nil, errInvalidKey
		}
		block, err := ssh.MarshalPrivateKeyWithPassphrase(raw, key.Comment, []byte(passphrase))
		clear(data)
		if err != nil {
			return nil, nil, errInvalidKey
		}
		data = pem.EncodeToMemory(block)
	}
	return data, []byte(key.Authorized()), nil
}

func KeyMatches(path, passphrase, fingerprint string, verify bool) bool {
	data, err := readKeyFile(path)
	if err != nil {
		return false
	}
	defer clear(data)
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if !verify && missing.PublicKey != nil {
			return ssh.FingerprintSHA256(missing.PublicKey) == fingerprint
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	}
	return err == nil && signer != nil && ssh.FingerprintSHA256(signer.PublicKey()) == fingerprint
}

func inspectKey(path string, data []byte, passphrase string) (KeyInfo, error) {
	var key KeyInfo
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		key.Encrypted, key.Public = true, missing.PublicKey
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		} else {
			err = nil
		}
	}
	if err != nil {
		return key, errInvalidKey
	}
	if signer != nil {
		key.Public = signer.PublicKey()
	}
	if public, err := readKeyFile(path + ".pub"); err == nil {
		pub, comment, options, rest, parseErr := ssh.ParseAuthorizedKey(public)
		if parseErr != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || key.Public != nil && !bytes.Equal(pub.Marshal(), key.Public.Marshal()) {
			return KeyInfo{}, errInvalidKey
		}
		key.Unverified = key.Public == nil
		key.Public = pub
		if ValidKeyComment(comment) {
			key.Comment = comment
		}
	}
	return key, nil
}

func readKeyFile(path string) ([]byte, error) {
	const limit = 1 << 20
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errInvalidKey
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if len(data) > limit {
		clear(data)
		return nil, errInvalidKey
	}
	return data, err
}

func WriteKeyPair(path string, private, public []byte, overwrite bool, stamp time.Time) error {
	if !ValidKeyName(filepath.Base(path)) {
		return errInvalidKey
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	targets := []string{path, path + ".pub"}
	staged, backups := make([]string, len(targets)), make([]string, len(targets))
	for index, target := range targets {
		info, err := os.Lstat(target)
		if err == nil && (!overwrite || !info.Mode().IsRegular()) {
			return os.ErrExist
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info != nil {
			backup := target + ".bak-" + stamp.Format("20060102150405")
			old, err := readKeyFile(target)
			if err != nil {
				return err
			}
			copyPath, err := stageKey(dir, old)
			clear(old)
			if err != nil {
				return err
			}
			defer os.Remove(copyPath)
			if err := os.Link(copyPath, backup); err != nil {
				return err
			}
			backups[index] = backup
		}
		staged[index], err = stageKey(dir, [][]byte{private, public}[index])
		if err != nil {
			return err
		}
		defer os.Remove(staged[index])
	}
	for index, target := range targets {
		install := os.Link
		if backups[index] != "" {
			install = os.Rename
		}
		if err := install(staged[index], target); err != nil {
			if index > 0 {
				if backups[0] == "" {
					err = errors.Join(err, os.Remove(targets[0]))
				} else {
					err = errors.Join(err, os.Remove(targets[0]), os.Link(backups[0], targets[0]))
				}
			}
			return err
		}
	}
	return nil
}

func stageKey(dir string, contents []byte) (string, error) {
	file, err := os.CreateTemp(dir, ".ssh-key-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err = protectKeyFile(file); err == nil {
		_, err = file.Write(contents)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		os.Remove(name)
		return "", fmt.Errorf("write key: %w", err)
	}
	return name, nil
}

type verifiedKeySigner struct {
	ssh.AlgorithmSigner
	used *bool
}

func (s verifiedKeySigner) Sign(reader io.Reader, payload []byte) (*ssh.Signature, error) {
	sig, err := s.AlgorithmSigner.Sign(reader, payload)
	*s.used = err == nil
	return sig, err
}

func (s verifiedKeySigner) SignWithAlgorithm(reader io.Reader, payload []byte, algorithm string) (*ssh.Signature, error) {
	sig, err := s.AlgorithmSigner.SignWithAlgorithm(reader, payload, algorithm)
	*s.used = err == nil
	return sig, err
}
