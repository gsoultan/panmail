package postgres

import (
	"testing"

	"github.com/gsoultan/panmail/internal/storetest"
)

type checkpointingLocal struct {
	localStub
	dir string
}

func (c *checkpointingLocal) Checkpoint(dir string) error { c.dir = dir; return nil }

// A backup of a gateway in shared mode snapshots the local store through the
// shared one. It used to refuse to run at all: the shared store could not
// snapshot itself, and the backup treats a store it cannot snapshot as fatal.
func TestTheSharedStoreCanBeBackedUp(t *testing.T) {
	conn := storetest.NewConnection(t)
	local := &checkpointingLocal{}
	s := NewStore(conn, local, NewWriter(conn))

	if err := s.Checkpoint("/backup/events.db"); err != nil || local.dir != "/backup/events.db" {
		t.Errorf("Checkpoint reached the local store with %q, %v", local.dir, err)
	}
	if err := NewStore(conn, &localStub{}, NewWriter(conn)).Checkpoint("/x"); err == nil {
		t.Error("a local store that cannot snapshot was reported as snapshotted")
	}
}
