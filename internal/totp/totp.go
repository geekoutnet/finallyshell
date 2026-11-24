package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"hash"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var ErrInvalidSecret = errors.New("invalid TOTP configuration")

var (
	base32Key    = base32.StdEncoding.WithPadding(base32.NoPadding)
	verification = regexp.MustCompile(`(?i)verification[\s-]+code|one[\s-]+time|\botp\b|\btoken\b|google\s+authenticator|验证码|动态口令`)
)

type Secret struct {
	key       []byte
	algorithm string
	digits    int
	period    int64
}

func VerificationPrompt(question string) bool { return verification.MatchString(question) }

func Parse(input string) (Secret, error) {
	secret := Secret{algorithm: "SHA1", digits: 6, period: 30}
	input = strings.TrimSpace(input)
	if len(input) > 4096 {
		return Secret{}, ErrInvalidSecret
	}
	if strings.HasPrefix(strings.ToLower(input), "otpauth:") {
		uri, err := url.Parse(input)
		if err != nil || uri.Scheme != "otpauth" || !strings.EqualFold(uri.Host, "totp") || uri.User != nil || len(uri.Path) < 2 || uri.Fragment != "" {
			return Secret{}, ErrInvalidSecret
		}
		query, err := url.ParseQuery(uri.RawQuery)
		if err != nil || len(query["secret"]) != 1 || len(query["algorithm"]) > 1 || len(query["digits"]) > 1 || len(query["period"]) > 1 {
			return Secret{}, ErrInvalidSecret
		}
		input = query.Get("secret")
		if query.Has("algorithm") {
			secret.algorithm = strings.ToUpper(query.Get("algorithm"))
			if secret.algorithm != "SHA1" && secret.algorithm != "SHA256" && secret.algorithm != "SHA512" {
				return Secret{}, ErrInvalidSecret
			}
		}
		if query.Has("digits") {
			secret.digits, err = strconv.Atoi(query.Get("digits"))
			if err != nil || (secret.digits != 6 && secret.digits != 8) {
				return Secret{}, ErrInvalidSecret
			}
		}
		if query.Has("period") {
			secret.period, err = strconv.ParseInt(query.Get("period"), 10, 64)
			if err != nil || secret.period < 4 || secret.period > 86400 {
				return Secret{}, ErrInvalidSecret
			}
		}
	}
	encoded := strings.ToUpper(strings.Join(strings.FieldsFunc(input, unicode.IsSpace), ""))
	plain := strings.TrimRight(encoded, "=")
	if len(plain) > 512 {
		return Secret{}, ErrInvalidSecret
	}
	key, err := base32Key.DecodeString(plain)
	if err != nil || len(key) == 0 || base32Key.EncodeToString(key) != plain || (plain != encoded && base32.StdEncoding.EncodeToString(key) != encoded) {
		return Secret{}, ErrInvalidSecret
	}
	secret.key = key
	return secret, nil
}

func (s Secret) Encode() string {
	query := url.Values{"secret": {base32Key.EncodeToString(s.key)}, "algorithm": {s.algorithm}, "digits": {strconv.Itoa(s.digits)}, "period": {strconv.FormatInt(s.period, 10)}}
	return "otpauth://totp/SSH?" + query.Encode()
}

func (s Secret) Remaining(now time.Time) time.Duration {
	return time.Duration(s.period-now.Unix()%s.period)*time.Second - time.Duration(now.Nanosecond())
}

func (s Secret) Code(now time.Time) string {
	var digest func() hash.Hash = sha1.New
	switch s.algorithm {
	case "SHA256":
		digest = sha256.New
	case "SHA512":
		digest = sha512.New
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/s.period))
	mac := hmac.New(digest, s.key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:]) & 0x7fffffff
	var code [8]byte
	for index := s.digits - 1; index >= 0; index-- {
		code[index] = byte('0' + value%10)
		value /= 10
	}
	return string(code[:s.digits])
}
