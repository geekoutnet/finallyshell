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
	// A code without a way to get one fails; a shown question never gets
	// the password.
	for _, q := range []struct {
		prompt string
		echo   bool
	}{{"Verification code:", false}, {"Unknown:", true}, {"Username:", true}} {
		if replies, err := keyboardInteractive(Options{Password: "test-login"})("", "", []string{q.prompt}, []bool{q.echo}); err == nil || replies != nil {
			t.Fatalf("sent a credential for %q", q.prompt)
		}
	}
	// Hidden questions get the password however the server words them,
	// as before codes existed.
	for _, prompt := range []string{"Passcode:", "Kennwort:", "Пароль:", ""} {
		if replies, err := keyboardInteractive(Options{Password: "test-login"})("", "", []string{prompt}, []bool{false}); err != nil || !slices.Equal(replies, []string{"test-login"}) {
			t.Fatalf("no password for the hidden question %q: %q, %v", prompt, replies, err)
		}
	}
	cancelled := errors.New("cancelled")
	callback = keyboardInteractive(Options{AskVerificationCode: func(string) (string, error) { return "", cancelled }})
	if replies, err := callback("", "", []string{"OTP:"}, []bool{true}); !errors.Is(err, cancelled) || replies != nil {
		t.Fatal("cancel did not terminate the challenge")
	}
}
