package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestTOTPRFC6238(t *testing.T) {
	keys := []string{"12345678901234567890", "12345678901234567890123456789012", "1234567890123456789012345678901234567890123456789012345678901234"}
	algorithms := []string{"SHA1", "SHA256", "SHA512"}
	for _, vector := range []struct {
		unix int64
		want [3]string
	}{
		{59, [3]string{"94287082", "46119246", "90693936"}},
		{1111111109, [3]string{"07081804", "68084774", "25091201"}},
		{1111111111, [3]string{"14050471", "67062674", "99943326"}},
		{1234567890, [3]string{"89005924", "91819424", "93441116"}},
		{2000000000, [3]string{"69279037", "90698825", "38618901"}},
		{20000000000, [3]string{"65353130", "77737706", "47863826"}},
	} {
		for index, algorithm := range algorithms {
			code, err := Parse("otpauth://totp/test?secret=" + base32.StdEncoding.EncodeToString([]byte(keys[index])) + "&digits=8&algorithm=" + algorithm)
			if err != nil {
				t.Fatal(err)
			}
			if got := code.Code(time.Unix(vector.unix, 0)); got != vector.want[index] {
				t.Errorf("%s at %d: %s, want %s", algorithm, vector.unix, got, vector.want[index])
			}
		}
	}
}

func TestTOTPParse(t *testing.T) {
	for _, input := range []string{
		"  jbsw y3dp ehpk 3pxp  ",
		"otpauth://totp/Example%3Atest?secret=JBSWY3DPEHPK3PXP&issuer=Example",
	} {
		code, err := Parse(input)
		if err != nil || code.Code(time.Unix(59, 0)) != "996554" || code.Remaining(time.Unix(59, 500000000)) != 500*time.Millisecond {
			t.Fatalf("unexpected default configuration: %v", err)
		}
	}
	code, err := Parse("otpauth://totp/test?secret=JBSWY3DPEHPK3PXP&digits=8&algorithm=sha256&period=45")
	if err != nil || len(code.Code(time.Unix(59, 0))) != 8 || code.Remaining(time.Unix(59, 0)) != 31*time.Second {
		t.Fatalf("unexpected URI configuration: %v", err)
	}
	for _, input := range []string{"", "0O1!", "MY===", strings.Repeat("AA", 257), "otpauth://hotp/test?secret=MY", "otpauth://totp/test?secret=MY&secret=ME", "otpauth://totp/test?secret=MY&algorithm=MD5", "otpauth://totp/test?secret=MY&period=3", "otpauth://totp/test?secret=MY&digits=9"} {
		if _, err := Parse(input); err == nil || strings.Contains(err.Error(), input) && input != "" {
			t.Error("invalid input was accepted or included in the error")
		}
	}
}

func TestTOTPVerificationPrompt(t *testing.T) {
	for _, question := range []string{"Verification code:", "Enter the one-time password: ", "Enter OTP code:", "OTP:", "Token password:", "Google Authenticator code:", "请输入验证码：", "输入动态口令："} {
		if !VerificationPrompt(question) {
			t.Errorf("did not recognize %q", question)
		}
	}
	for _, question := range []string{"Password:", "Enter passphrase:", "Hotplug login:", "Press enter:", "yubikey password:"} {
		if VerificationPrompt(question) {
			t.Errorf("misrecognized %q", question)
		}
	}
}
