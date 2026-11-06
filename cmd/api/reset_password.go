package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	authstores "github.com/gsoultan/panmail/internal/auth/repositories/stores/postgres"
	authusecases "github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/db"
	"golang.org/x/term"
)

// handleResetPasswordCommand sets a new password on an account without the
// old one, for an administrator who is locked out.
//
//	panmail reset-password --email admin@example.com
//
// It runs on the server against the configured database, so it needs nothing
// the operator does not already have: whoever can read config.yaml can change
// the users table by hand. That is also why it is a command and not an API
// route — a route would be a way in for someone who has neither.
//
// The password is read from the terminal without echo, twice. A script passes
// --password-stdin and pipes it in instead; there is deliberately no flag that
// takes the password itself, because a flag lands in shell history and in the
// process list where any local user can read it.
func handleResetPasswordCommand() {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	configFlag := fs.String("config", "", "Path to configuration file")
	emailFlag := fs.String("email", "", "Email address of the account to reset (required)")
	stdinFlag := fs.Bool("password-stdin", false, "Read the new password from the first line of standard input instead of prompting")
	disable2FAFlag := fs.Bool("disable-2fa", false, "Also turn off two-factor sign-in for the account, for a lost authenticator")
	_ = fs.Parse(os.Args[2:])

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if *emailFlag == "" {
		fmt.Fprintln(os.Stderr, "reset-password: --email is required")
		fs.Usage()
		os.Exit(2)
	}
	if *configFlag != "" {
		config.SetConfigPath(*configFlag)
	}

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		fmt.Fprintf(os.Stderr, "reset-password: no configuration found (%v)\n", err)
		os.Exit(1)
	}

	stdinFD := int(os.Stdin.Fd())
	password, err := readNewPassword(*stdinFlag, os.Stdin, os.Stderr, term.IsTerminal(stdinFD), func() ([]byte, error) {
		return term.ReadPassword(stdinFD)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password: %v\n", err)
		os.Exit(1)
	}
	// Checked here as well as in the reset, so a too-short password is
	// refused before anything touches the database.
	if err := authusecases.ValidatePassword(password); err != nil {
		fmt.Fprintf(os.Stderr, "reset-password: %v\n", err)
		os.Exit(1)
	}

	sqlDB, err := db.Connect(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password: cannot connect to the database: %v\n", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	user, err := authusecases.ResetPassword(ctx, authstores.NewStore(db.NewConnection(sqlDB)), authusecases.PasswordReset{
		Email:            *emailFlag,
		NewPassword:      password,
		DisableTwoFactor: *disable2FAFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("password reset for %s (role %s)\n", user.Email, user.Role)
	switch {
	case *disable2FAFlag:
		fmt.Println("two-factor sign-in is off; turn it back on from your profile after signing in")
	case user.TwoFactorEnabled:
		fmt.Println("two-factor sign-in is still on; pass --disable-2fa if the authenticator is lost too")
	}
}

// errPasswordMismatch is returned when the confirmation does not match.
var errPasswordMismatch = errors.New("the two passwords did not match; nothing was changed")

// readNewPassword gets the new password from wherever the operator chose.
//
// With fromStdin it takes the first line of in, so `echo "$PW" | panmail
// reset-password ...` and a heredoc both work. Otherwise it prompts on out
// and reads twice without echo, refusing when there is no terminal to prompt
// on: reading a "password" from a pipe nobody meant as one is a worse outcome
// than an error that names the flag.
func readNewPassword(fromStdin bool, in io.Reader, out io.Writer, isTerminal bool, readSecret func() ([]byte, error)) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		if password == "" {
			return "", errors.New("no password on standard input")
		}
		return password, nil
	}

	if !isTerminal {
		return "", errors.New("standard input is not a terminal; pipe the password in with --password-stdin")
	}

	fmt.Fprint(out, "New password: ")
	first, err := readSecret()
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(out, "Confirm new password: ")
	second, err := readSecret()
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(first) != string(second) {
		return "", errPasswordMismatch
	}
	if len(first) == 0 {
		return "", errors.New("the password is empty; nothing was changed")
	}
	return string(first), nil
}
