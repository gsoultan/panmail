package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// /admin/backup takes no credentials, on the reasoning that a loopback caller
// is already an operator on the box. That reasoning is sound and it stops
// holding the moment the listener is bound anywhere else — which --metrics-addr
// exists to allow, because Prometheus usually scrapes from another host.
//
// With 0.0.0.0:9090 and no gate, an unauthenticated POST from another address
// wrote the SQL database, every Pebble store and config.yaml — which carries
// the auth signing key — to a directory named in the query string, and returned
// a manifest listing what it had taken.
//
// So the test is on the predicate that decides whether it is mounted at all.

func TestOnlyLoopbackCountsAsLoopback(t *testing.T) {
	loopback := []string{
		"127.0.0.1:9090",
		"localhost:9090",
		"[::1]:9090",
		"127.0.0.5:9090", // the whole 127/8 range is local
	}
	for _, addr := range loopback {
		if !isLoopbackAddr(addr) {
			t.Errorf("%s is loopback; refusing to mount here removes backups for no reason", addr)
		}
	}

	exposed := []string{
		"0.0.0.0:9090",      // every interface — the obvious way to let Prometheus in
		":9090",             // the same thing, written shorter
		"192.168.1.10:9090", // a LAN address
		"[::]:9090",         // every interface, v6
		"10.0.0.1:9090",     // private, but not this machine
		"example.com:9090",  // a name that is not localhost
		"9090",              // not host:port at all — fail closed
		"",                  // ditto
	}
	for _, addr := range exposed {
		if isLoopbackAddr(addr) {
			t.Errorf("%s treated as loopback: an unauthenticated endpoint that writes the "+
				"auth signing key to a caller-named path would be mounted on it", addr)
		}
	}
}

// Profiling sits behind the same gate, for a comparable reason: a heap profile
// is a dump of whatever the process is holding — message bodies, decrypted
// credentials — and /debug/pprof/profile will spend thirty seconds of CPU for
// anyone who asks.
func TestProfilingIsMountedAndScopedToTheGatedMux(t *testing.T) {
	mux := http.NewServeMux()
	mountProfiling(mux)

	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s is not served; a leak would have to be diagnosed by rebuilding", path)
		}
	}

	// And the default mux is empty. Importing net/http/pprof registers five
	// handlers there whichever form the import takes — naming the package
	// rather than aliasing it to `_` does not avoid the init. Nothing here
	// serves DefaultServeMux today, but that is a fact about the current code
	// and not a property of it, so main empties it. This asserts that.
	discardDefaultMux()
	rec := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("DefaultServeMux serves /debug/pprof/ (%d); profiling would escape the gate "+
			"onto any server built from it", rec.Code)
	}
}

// The mux the gate protects: when the endpoint is not mounted, a request for it
// has to 404 rather than reach a handler.
func TestAnUnmountedBackupEndpointIsNotThere(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Deliberately not mounting the backup endpoint, as the exposed branch does.

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/backup?out=/tmp/stolen", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /admin/backup returned %d, want 404", rec.Code)
	}

	// And metrics still work, which is the point of separating them: scraping
	// from another host must stay possible.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /metrics returned %d; the gate should not cost scraping", rec.Code)
	}
}
