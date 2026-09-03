package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/gsoultan/panmail/internal/system_settings/entities"
	"github.com/gsoultan/panmail/internal/system_settings/repositories"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/get_settings.sql
	getSettingsQuery string
	//go:embed sql/save_settings.sql
	saveSettingsQuery string
	//go:embed sql/seed_settings.sql
	seedSettingsQuery string
)

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) repositories.SettingsRepository {
	return &store{conn: conn}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Get(ctx context.Context) (*entities.Settings, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}

	var (
		out              entities.Settings
		retryPatternJSON sql.NullString
		logDays          sql.NullInt64
		webhookDays      sql.NullInt64
	)
	err = dbConn.QueryRowContext(ctx, getSettingsQuery).Scan(
		&out.BaseURL,
		&retryPatternJSON,
		&logDays,
		&webhookDays,
		&out.MessageRetentionDays,
		&out.OutboxRetentionDays,
		&out.AppLogRetentionDays,
		&out.InboundRetentionDays,
		&out.ArchiveRetentionDays,
		&out.QuarantineRetentionDays,
		&out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing stored yet. Not an error — the caller applies defaults.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if retryPatternJSON.Valid && retryPatternJSON.String != "" {
		_ = json.Unmarshal([]byte(retryPatternJSON.String), &out.RetryPattern)
	}
	// NULL stays nil rather than becoming zero. The difference is the whole
	// reason these two columns are nullable; see the migration.
	if logDays.Valid {
		v := int(logDays.Int64)
		out.LogRetentionDays = &v
	}
	if webhookDays.Valid {
		v := int(webhookDays.Int64)
		out.WebhookRetentionDays = &v
	}
	return &out, nil
}

func (s *store) Save(ctx context.Context, in *entities.Settings) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	args, err := writeArgs(in)
	if err != nil {
		return err
	}
	_, err = dbConn.ExecContext(ctx, saveSettingsQuery, args...)
	return err
}

func (s *store) Seed(ctx context.Context, in *entities.Settings) (bool, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return false, err
	}
	args, err := writeArgs(in)
	if err != nil {
		return false, err
	}
	res, err := dbConn.ExecContext(ctx, seedSettingsQuery, args...)
	if err != nil {
		return false, err
	}
	// DO NOTHING affects no rows, which is how a second instance learns it was
	// not the one that seeded.
	n, err := res.RowsAffected()
	if err != nil {
		// Not every driver reports it. Seeding still happened or was declined
		// correctly either way, so this is not worth failing startup over.
		return false, nil
	}
	return n > 0, nil
}

// writeArgs renders the settings as the parameter list both writes share.
func writeArgs(in *entities.Settings) ([]any, error) {
	if in == nil {
		return nil, errors.New("settings are required")
	}

	// An empty pattern is stored as SQL NULL rather than as "null" or "[]", so
	// that reading it back gives an empty slice and the caller's own default
	// applies. Marshalling a nil slice would write the four bytes "null",
	// which round-trips to nil too but leaves a value in the column that reads
	// as deliberate.
	var retryPattern any
	if len(in.RetryPattern) > 0 {
		encoded, err := json.Marshal(in.RetryPattern)
		if err != nil {
			return nil, err
		}
		retryPattern = string(encoded)
	}

	updatedAt := in.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return []any{
		in.BaseURL,
		retryPattern,
		nullableDays(in.LogRetentionDays),
		nullableDays(in.WebhookRetentionDays),
		in.MessageRetentionDays,
		in.OutboxRetentionDays,
		in.AppLogRetentionDays,
		in.InboundRetentionDays,
		in.ArchiveRetentionDays,
		in.QuarantineRetentionDays,
		updatedAt,
	}, nil
}

// nullableDays keeps "never set" and "set to forever" apart on the way in, the
// way the scan keeps them apart on the way out.
func nullableDays(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
