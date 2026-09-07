package pebble

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// archiveRoot is where retention archives are written, one directory per
// tenant.
const archiveRoot = "archives"

// tenantArchiveDir returns the directory holding a tenant's archives.
//
// filepath.Base strips any path the caller supplied, so a tenant id taken from
// a request cannot walk out of the archive root.
func tenantArchiveDir(tenantID string) string {
	return filepath.Join(archiveRoot, filepath.Base(tenantID))
}

// tenantArchiveSet writes archived events into a separate file per tenant,
// creating each file only when that tenant actually has something to archive.
type tenantArchiveSet struct {
	filename string
	files    map[string]*os.File
}

func newTenantArchiveSet(filename string) *tenantArchiveSet {
	return &tenantArchiveSet{
		filename: filename,
		files:    make(map[string]*os.File),
	}
}

func (a *tenantArchiveSet) write(tenantID string, record []byte) error {
	file, err := a.fileFor(tenantID)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(record, '\n')); err != nil {
		return fmt.Errorf("failed to write archive record: %w", err)
	}
	return nil
}

func (a *tenantArchiveSet) fileFor(tenantID string) (*os.File, error) {
	if file, ok := a.files[tenantID]; ok {
		return file, nil
	}

	dir := tenantArchiveDir(tenantID)
	// Archives contain recipients and subjects, so the directory is
	// owner-only.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create archive directory: %w", err)
	}

	file, err := os.OpenFile(filepath.Join(dir, a.filename), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive file: %w", err)
	}

	a.files[tenantID] = file
	return file, nil
}

func (a *tenantArchiveSet) closeAll() {
	for _, file := range a.files {
		_ = file.Close()
	}
}

// ArchiveEvents writes events into this instance's per-tenant JSONL archives.
//
// Exported for the shared event store. When delivery events live in the
// database, the rows are deleted by SQL — but the archive they are written to
// first is still a file on this disk, served by ListArchives and GetArchive,
// which the shared store delegates here. Without this it would have deleted
// without archiving: log_retention_days defaults to 14 days, so a deployment
// would silently lose the escape hatch two weeks after switching over.
//
// One file per tenant per call, named the same way the retention pass names
// its own, because an archive shared between tenants would let anyone who can
// download one read every other tenant's mail history.
func (s *store) ArchiveEvents(ctx context.Context, events []*entities.EmailEvent) error {
	if len(events) == 0 {
		return nil
	}

	archives := newTenantArchiveSet(fmt.Sprintf("archive_%s%s", time.Now().Format(archiveStamp), archiveExt))
	defer archives.closeAll()

	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e == nil || e.TenantID == "" {
			// No tenant means no directory to write into, and a shared file is
			// the one thing the per-tenant layout exists to prevent.
			continue
		}
		record, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := archives.write(e.TenantID, record); err != nil {
			return err
		}
	}
	return nil
}
