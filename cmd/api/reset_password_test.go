package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func typedSecrets(values ...string) func() ([]byte, error) {
	i := 0
	return func() ([]byte, error) {
		if i >= len(values) {
			return nil, io.EOF
		}
		v := values[i]
		i++
		return []byte(v), nil
	}
}

func noSecret() ([]byte, error) { panic("prompted when it should not have") }

func TestReadNewPasswordFromStdinTakesTheFirstLine(t *testing.T) {
	for _, in := range []string{"s3cret pass phrase\n", "s3cret pass phrase\r\nignored\n", "s3cret pass phrase"} {
		got, err := readNewPassword(true, strings.NewReader(in), io.Discard, false, noSecret)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != "s3cret pass phrase" {
			t.Fatalf("%q: got %q", in, got)
		}
	}
}

func TestReadNewPasswordFromStdinRefusesEmpty(t *testing.T) {
	if _, err := readNewPassword(true, strings.NewReader("\n"), io.Discard, false, noSecret); err == nil {
		t.Fatal("expected an error for an empty line")
	}
}

func TestReadNewPasswordPromptsTwice(t *testing.T) {
	var out strings.Builder
	got, err := readNewPassword(false, nil, &out, true, typedSecrets("correct-horse-battery", "correct-horse-battery"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "correct-horse-battery" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(out.String(), "correct-horse") {
		t.Fatal("the password was echoed to the prompt output")
	}
}

func TestReadNewPasswordRefusesAMismatch(t *testing.T) {
	_, err := readNewPassword(false, nil, io.Discard, true, typedSecrets("correct-horse-battery", "correct-horse-batterx"))
	if !errors.Is(err, errPasswordMismatch) {
		t.Fatalf("err = %v, want errPasswordMismatch", err)
	}
}

func TestReadNewPasswordNeedsATerminalToPrompt(t *testing.T) {
	_, err := readNewPassword(false, strings.NewReader("piped\n"), io.Discard, false, noSecret)
	if err == nil || !strings.Contains(err.Error(), "--password-stdin") {
		t.Fatalf("err = %v, want one naming --password-stdin", err)
	}
}
