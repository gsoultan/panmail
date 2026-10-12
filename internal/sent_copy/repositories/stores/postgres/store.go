// Package postgres stores Sent copies settings, mailbox logins and the copies
// waiting to be filed.
package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/sent_copy/repositories"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/secrets"
)

var (
	//go:embed sql/get_settings.sql
	getSettingsQuery string
	//go:embed sql/save_settings.sql
	saveSettingsQuery string
	//go:embed sql/list_mailboxes.sql
	listMailboxesQuery string
	//go:embed sql/get_mailbox.sql
	getMailboxQuery string
	//go:embed sql/save_mailbox.sql
	saveMailboxQuery string
	//go:embed sql/delete_mailbox.sql
	deleteMailboxQuery string
	//go:embed sql/enqueue_job.sql
	enqueueJobQuery string
	//go:embed sql/due_jobs.sql
	dueJobsQuery string
	//go:embed sql/claim_job.sql
	claimJobQuery string
	//go:embed sql/get_job.sql
	getJobQuery string
	//go:embed sql/complete_job.sql
	completeJobQuery string
	//go:embed sql/reschedule_job.sql
	rescheduleJobQuery string
)

// ErrNoEncryptionKey is returned when a password would have to be written in
// the clear. Every other stored credential in this gateway is sealed with the
// data key, and a mailbox password must not be the exception.
var ErrNoEncryptionKey = errors.New(
	"sent copies: a mailbox password cannot be stored because no data encryption key is configured")

type store struct {
	conn    db.Connection
	keyring *secrets.Keyring
}

// NewStore builds the store. A nil keyring is accepted: NONE and Bcc to sender
// store no password, and work without a data key.
func NewStore(conn db.Connection, keyring *secrets.Keyring) repositories.Repository {
	return &store{conn: conn, keyring: keyring}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) GetSettings(ctx context.Context, tenantID string) (*entities.Settings, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}

	out := entities.Settings{TenantID: tenantID}
	var mode, sealed string
	err = dbConn.QueryRowContext(ctx, getSettingsQuery, tenantID).Scan(
		&mode, &out.IMAPHost, &out.IMAPPort, &out.IMAPUseSSL, &out.SentFolder,
		&out.MasterUsername, &sealed, &out.MasterSeparator, &out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the Sent copies setting: %w", err)
	}
	out.Mode = entities.Mode(mode)
	if out.MasterPassword, err = s.unseal(sealed); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *store) SaveSettings(ctx context.Context, set *entities.Settings) error {
	sealed, err := s.seal(set.MasterPassword)
	if err != nil {
		return err
	}
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = dbConn.ExecContext(ctx, saveSettingsQuery,
		set.TenantID, string(set.Mode), set.IMAPHost, set.IMAPPort, set.IMAPUseSSL, set.SentFolder,
		set.MasterUsername, sealed, set.MasterSeparator, set.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save the Sent copies setting: %w", err)
	}
	return nil
}

func (s *store) ListMailboxes(ctx context.Context, tenantID string) ([]*entities.Mailbox, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	rows, err := dbConn.QueryContext(ctx, listMailboxesQuery, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list Sent copies mailboxes: %w", err)
	}
	defer rows.Close()

	var out []*entities.Mailbox
	for rows.Next() {
		m := &entities.Mailbox{TenantID: tenantID}
		var sealed string
		if err := rows.Scan(&m.Address, &m.Username, &sealed, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to read a Sent copies mailbox: %w", err)
		}
		if m.Password, err = s.unseal(sealed); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *store) GetMailbox(ctx context.Context, tenantID, address string) (*entities.Mailbox, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	m := &entities.Mailbox{TenantID: tenantID}
	var sealed string
	err = dbConn.QueryRowContext(ctx, getMailboxQuery, tenantID, address).Scan(&m.Address, &m.Username, &sealed, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read a Sent copies mailbox: %w", err)
	}
	if m.Password, err = s.unseal(sealed); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *store) SaveMailbox(ctx context.Context, m *entities.Mailbox) error {
	sealed, err := s.seal(m.Password)
	if err != nil {
		return err
	}
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	if _, err := dbConn.ExecContext(ctx, saveMailboxQuery, m.TenantID, m.Address, m.Username, sealed, m.UpdatedAt); err != nil {
		return fmt.Errorf("failed to save a Sent copies mailbox: %w", err)
	}
	return nil
}

func (s *store) DeleteMailbox(ctx context.Context, tenantID, address string) (bool, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return false, err
	}
	res, err := dbConn.ExecContext(ctx, deleteMailboxQuery, tenantID, address)
	if err != nil {
		return false, fmt.Errorf("failed to delete a Sent copies mailbox: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *store) EnqueueJob(ctx context.Context, job *entities.Job, now time.Time) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	if _, err := dbConn.ExecContext(ctx, enqueueJobQuery,
		job.ID, job.TenantID, job.MessageID, string(job.Request), now.Unix(),
	); err != nil {
		return fmt.Errorf("failed to queue a Sent copy: %w", err)
	}
	return nil
}

// ClaimJobs claims each due job with a compare-and-set on its previous token,
// so two instances that read the same candidates claim each one only once.
func (s *store) ClaimJobs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*entities.Job, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}

	rows, err := dbConn.QueryContext(ctx, dueJobsQuery, now.Unix(), now.Add(-lease).Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read due Sent copies: %w", err)
	}
	type candidate struct{ id, token string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.token); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to read a due Sent copy: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var claimed []*entities.Job
	for _, c := range candidates {
		token, err := newToken()
		if err != nil {
			return claimed, err
		}
		res, err := dbConn.ExecContext(ctx, claimJobQuery, token, now.Unix(), c.id, c.token)
		if err != nil {
			return claimed, fmt.Errorf("failed to claim a Sent copy: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue
		}

		job := &entities.Job{}
		var request string
		var createdAt int64
		if err := dbConn.QueryRowContext(ctx, getJobQuery, c.id).Scan(
			&job.ID, &job.TenantID, &job.MessageID, &request, &job.Attempts, &job.ClaimToken, &job.LastError, &createdAt,
		); err != nil {
			return claimed, fmt.Errorf("failed to read a claimed Sent copy: %w", err)
		}
		job.Request = []byte(request)
		job.CreatedAt = time.Unix(createdAt, 0)
		claimed = append(claimed, job)
	}
	return claimed, nil
}

func (s *store) CompleteJob(ctx context.Context, id, claimToken string) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	if _, err := dbConn.ExecContext(ctx, completeJobQuery, id, claimToken); err != nil {
		return fmt.Errorf("failed to finish a Sent copy: %w", err)
	}
	return nil
}

func (s *store) RescheduleJob(ctx context.Context, id, claimToken string, attempts int, next time.Time, lastError string) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	if _, err := dbConn.ExecContext(ctx, rescheduleJobQuery, id, claimToken, attempts, next.Unix(), lastError); err != nil {
		return fmt.Errorf("failed to reschedule a Sent copy: %w", err)
	}
	return nil
}

// seal encrypts a password, or refuses. Empty stays empty, so "no password
// stored" is visible in the column itself.
func (s *store) seal(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	if s.keyring == nil {
		return "", ErrNoEncryptionKey
	}
	sealed, err := s.keyring.Encrypt(password)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt a Sent copies password: %w", err)
	}
	return sealed, nil
}

func (s *store) unseal(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if s.keyring == nil {
		return "", ErrNoEncryptionKey
	}
	plain, err := s.keyring.Decrypt(stored)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt a Sent copies password: %w", err)
	}
	return plain, nil
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to make a claim token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// RotateSecrets rewrites every stored password under the keyring's primary
// key: the IMAP master password on each tenant's settings, and each mailbox
// login.
//
// Values already on the primary key are skipped, so the pass is idempotent and
// an interrupted run resumes by running it again. Not tenant-scoped: a key
// still holding one tenant's rows is a key that cannot be retired.
func (s *store) RotateSecrets(ctx context.Context) (rotated int, err error) {
	dbConn, err := s.getDB()
	if err != nil {
		return 0, err
	}

	tables := []struct {
		what   string
		list   string
		update string
	}{
		{
			"Sent copies settings",
			`SELECT tenant_id, '', COALESCE(master_password, '') FROM sent_copy_settings`,
			`UPDATE sent_copy_settings SET master_password = $3 WHERE tenant_id = $1`,
		},
		{
			"Sent copies mailbox",
			`SELECT tenant_id, address, COALESCE(password, '') FROM sent_copy_mailboxes`,
			`UPDATE sent_copy_mailboxes SET password = $3 WHERE tenant_id = $1 AND address = $2`,
		},
	}

	for _, t := range tables {
		n, err := rotateColumn(ctx, dbConn, s.keyring, t.list, t.update)
		rotated += n
		if err != nil {
			return rotated, fmt.Errorf("%s: %w", t.what, err)
		}
	}
	return rotated, nil
}

// rotateColumn re-seals one encrypted column. list returns (key1, key2, value)
// rows and update takes the same two keys and the new value.
func rotateColumn(ctx context.Context, dbConn *sql.DB, keyring *secrets.Keyring, list, update string) (int, error) {
	type row struct{ k1, k2, value string }

	// Read everything first: holding a cursor open while updating the same
	// table deadlocks on some engines and re-reads rewritten rows on others.
	rows, err := dbConn.QueryContext(ctx, list)
	if err != nil {
		return 0, err
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.k1, &r.k2, &r.value); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	rotated := 0
	for _, r := range all {
		if !keyring.NeedsRotation(r.value) {
			continue
		}
		// A failure here means a key is missing, and rewriting the row anyway
		// would replace a recoverable password with an unreadable one.
		plain, err := keyring.Decrypt(r.value)
		if err != nil {
			return rotated, fmt.Errorf("tenant %s: %w", r.k1, err)
		}
		sealed, err := keyring.Encrypt(plain)
		if err != nil {
			return rotated, fmt.Errorf("tenant %s: %w", r.k1, err)
		}
		if _, err := dbConn.ExecContext(ctx, update, r.k1, r.k2, sealed); err != nil {
			return rotated, fmt.Errorf("tenant %s: %w", r.k1, err)
		}
		rotated++
	}
	return rotated, nil
}
