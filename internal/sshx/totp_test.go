package sshx

import (
	"errors"
	"slices"
	"testing"
)

func TestTOTPKeyboardInteractiveOrder(t *testing.T) {
	var prompts []string
	callback := keyboardInteractive(Options{Password: "test-login", AskVerificationCode: func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		return "000123", nil
	}})
	answers, err := callback("ignored", "ignored", []string{"Verification code:", "Password:", "动态口令："}, []bool{true, false, false})
	if err != nil || !slices.Equal(answers, []string{"000123", "test-login", "000123"}) || !slices.Equal(prompts, []string{"Verification code:", "动态口令："}) {
		t.Fatal("verification prompts were not answered separately, in order")
	}
	for _, prompt := range []string{"Verification code:", "Unknown:", "Passphrase:"} {
		if replies, err := keyboardInteractive(Options{Password: "test-login"})("", "", []string{prompt}, []bool{false}); err == nil || replies != nil {
			t.Fatalf("sent a credential for %q", prompt)
		}
	}
	cancelled := errors.New("cancelled")
	callback = keyboardInteractive(Options{AskVerificationCode: func(string) (string, error) { return "", cancelled }})
	if replies, err := callback("", "", []string{"OTP:"}, []bool{true}); !errors.Is(err, cancelled) || replies != nil {
		t.Fatal("cancel did not terminate the challenge")
	}
}
