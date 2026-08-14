// Package guards holds checks about the shape of the codebase rather than the
// behaviour of any part of it.
//
// This one exists because the same mistake was made three times in one
// afternoon — /admin/backup, /readyz and /inbound/ each returned an internal
// error to a caller who had no business seeing it — and all three were written
// by someone who knew better and was not thinking about which listener the
// handler would end up on. That is not a thing more care prevents. It is a
// thing a test prevents.
package guards

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rawErrorToClient matches handing an error's text straight to an HTTP caller.
var rawErrorToClient = regexp.MustCompile(`http\.Error\(\s*w\s*,\s*err\.Error\(\)|http\.Error\(\s*w\s*,\s*fmt\.Sprintf\([^)]*%[vs]"?\s*,\s*err\b`)

// allowed lists the places where publishing the error is correct, and why.
//
// Every entry is a claim that the handler cannot be reached from outside this
// machine. Adding one means making that claim; if it is wrong, this file is
// where the wrongness will be recorded, which is the point of writing it down
// rather than adding a nolint comment at the site.
var allowed = map[string]string{
	"cmd/api/main.go": "the backup endpoint, mounted only on a loopback listener — its caller is " +
		"an operator who asked why their backup failed, and sending them to the log for an answer " +
		"they asked for directly would be worse",
}

// What went wrong the three times it went wrong, so a failure explains itself.
const why = `
A handler is returning an internal error to its caller.

That text is not written for the public. The three found so far carried, between
them: the database user and name, an internal hostname and port, the storage
engine, and a filesystem path.

  - /admin/backup wrote the auth signing key to a caller-named directory
  - /readyz published the database DSN to anyone who asked
  - /inbound/ returned Pebble paths and the outbox's connection error

Log the error and return something that says what failed without saying how.
Keep the status code: a provider reads anything below 500 as accepted and drops
the message.

If the handler genuinely cannot be reached from off-box, add the file to the
allowed map above with the reason.`

func TestNoHandlerPublishesAnInternalError(t *testing.T) {
	root := repoRoot(t)

	var offences []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if _, ok := allowed[filepath.ToSlash(rel)]; ok {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			for line := 1; scanner.Scan(); line++ {
				if rawErrorToClient.MatchString(scanner.Text()) {
					offences = append(offences, fmt.Sprintf("%s:%d: %s",
						filepath.ToSlash(rel), line, strings.TrimSpace(scanner.Text())))
				}
			}
			return scanner.Err()
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(offences) > 0 {
		t.Errorf("%s\n\n%s", strings.Join(offences, "\n"), why)
	}
}

// The guard has to actually match the thing it is guarding against. A regex
// that quietly matches nothing is worse than no test, because it reads as
// coverage.
func TestTheGuardMatchesWhatItIsFor(t *testing.T) {
	shouldMatch := []string{
		`http.Error(w, err.Error(), http.StatusInternalServerError)`,
		`		http.Error(w, err.Error(), 500)`,
		`http.Error( w , err.Error(), http.StatusBadRequest)`,
		`http.Error(w, fmt.Sprintf("could not process: %v", err), 500)`,
	}
	for _, line := range shouldMatch {
		if !rawErrorToClient.MatchString(line) {
			t.Errorf("the guard does not match %q, so that form could be added freely", line)
		}
	}

	shouldNotMatch := []string{
		`http.Error(w, "Could not process the message", http.StatusInternalServerError)`,
		`http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)`,
		`slog.Error("failed to process an inbound email", "error", err)`,
		`return fmt.Errorf("migration failed: %w", err)`,
		`http.Error(w, "out= is required", http.StatusBadRequest)`,
	}
	for _, line := range shouldNotMatch {
		if rawErrorToClient.MatchString(line) {
			t.Errorf("the guard flags %q, which is fine; false positives get it disabled", line)
		}
	}
}

// An allowance is a claim that a handler is unreachable from off-box. It has to
// come with the reasoning, because the next person to read it needs to be able
// to check whether it is still true.
func TestEveryAllowanceIsJustified(t *testing.T) {
	root := repoRoot(t)

	for file, reason := range allowed {
		if len(reason) < 40 {
			t.Errorf("%s is allowed with reason %q; say why it cannot be reached from off-box", file, reason)
		}
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Errorf("%s is allowed but does not exist; a stale allowance hides a real offence "+
				"if the path comes back", file)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the repository root")
		}
		dir = parent
	}
}
