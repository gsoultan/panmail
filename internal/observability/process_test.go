package observability

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

// A goroutine leak, a connection leak and a heap that never comes down all look
// identical from outside: the queues drain, the endpoints answer, and one
// morning the container is OOMKilled with no record of why. These are the
// numbers that distinguish them, and none of them existed.
func TestProcessMetricsArePublished(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if err := m.ObserveProcess(db); err != nil {
		t.Fatalf("register: %v", err)
	}

	body := scrape(t, m)
	for _, want := range []string{
		"panmail_goroutines",
		"panmail_heap_in_use_bytes",
		"panmail_db_connections_in_use",
		"panmail_db_connections_open",
		"panmail_db_connections_wait_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s is not published; an operator cannot see it", want)
		}
	}
}

// Goroutines is the leak signal, so it has to actually track them rather than
// report a constant.
func TestTheGoroutineCountIsLive(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := m.ObserveProcess(nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	before := goroutineValue(t, scrape(t, m))

	// Hold a batch of goroutines open across a scrape.
	release := make(chan struct{})
	var running, done sync.WaitGroup
	for range 50 {
		running.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			running.Done()
			<-release
		}()
	}
	running.Wait()

	during := goroutineValue(t, scrape(t, m))
	close(release)
	done.Wait()

	if during <= before {
		t.Errorf("goroutines read %d then %d while 50 were held open; the gauge is not live",
			before, during)
	}
}

// A gateway with no SQL handle must still publish what it can, rather than
// registering nothing.
func TestProcessMetricsWithoutADatabase(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := m.ObserveProcess(nil); err != nil {
		t.Fatalf("a nil database should not be an error: %v", err)
	}

	body := scrape(t, m)
	if !strings.Contains(body, "panmail_goroutines") {
		t.Error("goroutines were not published without a database handle")
	}
	if strings.Contains(body, "panmail_db_connections_in_use") {
		t.Error("connection metrics were published with no database to read them from")
	}
}

// goroutineValue pulls the sample out of the Prometheus text format.
func goroutineValue(t *testing.T, body string) int {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "panmail_goroutines") || strings.HasPrefix(line, "panmail_goroutines_") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		f, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			return int(f)
		}
	}
	t.Fatalf("no panmail_goroutines sample in:\n%s", body)
	return 0
}
