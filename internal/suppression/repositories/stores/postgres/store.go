package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/internal/suppression/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_suppression.sql
	createSuppressionQuery string
	//go:embed sql/delete_suppression.sql
	deleteSuppressionQuery string
	//go:embed sql/get_suppression_by_email.sql
	getSuppressionByEmailQuery string
	//go:embed sql/list_suppressions.sql
	listSuppressionsQuery string
	//go:embed sql/list_suppressions_by_emails.sql
	listSuppressionsByEmailsQuery string
)

// emailPlaceholderToken is what the batch statement carries in place of an IN
// list whose length is only known at call time.
const emailPlaceholderToken = "__EMAIL_PLACEHOLDERS__"

// maxEmailsPerLookup bounds one round trip. Recipient lists are caller-supplied
// and nothing upstream caps their length, so an unbounded IN list would let a
// single message build a statement with as many parameters as it liked —
// PostgreSQL stops at 65535 and the planner suffers long before that. Chunking
// keeps the query a fixed shape and the win intact: a thousand recipients cost
// two round trips rather than a thousand.
const maxEmailsPerLookup = 500

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) stores.SuppressionRepository {
	return &store{conn: conn}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Create(ctx context.Context, sup *entities.Suppression) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createSuppressionQuery, sup.ID, sup.TenantID, sup.Email, sup.Reason, sup.CreatedAt)
	return err
}

func (s *store) Delete(ctx context.Context, tenantID, email string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteSuppressionQuery, tenantID, email)
	return err
}

func (s *store) GetByEmail(ctx context.Context, tenantID, email string) (*entities.Suppression, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	sup := &entities.Suppression{}
	err = db.QueryRowContext(ctx, getSuppressionByEmailQuery, tenantID, email).Scan(&sup.ID, &sup.TenantID, &sup.Email, &sup.Reason, &sup.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return sup, nil
}

func (s *store) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Suppression, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listSuppressionsQuery), ";")
	query += " LIMIT $2 OFFSET $3"

	rows, err := dbConn.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var res []*entities.Suppression
	for rows.Next() {
		sup := &entities.Suppression{}
		err := rows.Scan(&sup.ID, &sup.TenantID, &sup.Email, &sup.Reason, &sup.CreatedAt)
		if err != nil {
			return nil, "", err
		}
		res = append(res, sup)
	}

	nextPageToken := ""
	if len(res) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return res, nextPageToken, nil
}

// GetByEmails looks up many addresses in one round trip.
//
// It exists because admission checks every recipient of a message, and doing
// that one query at a time made a hundred-recipient send cost a hundred
// sequential round trips to a database that is usually on another host. The
// check itself is unchanged: every address is still looked up, still scoped to
// the tenant, and an address missing from the result is not suppressed.
//
// The returned map is keyed by the normalised address the caller passed, so a
// caller can look up what it asked for rather than what the database stored.
func (s *store) GetByEmails(
	ctx context.Context, tenantID string, emails []string,
) (map[string]*entities.Suppression, error) {
	found := make(map[string]*entities.Suppression, len(emails))
	if len(emails) == 0 {
		return found, nil
	}

	db, err := s.getDB()
	if err != nil {
		return nil, err
	}

	// De-duplicated first: the same address in To and Cc is one lookup, and
	// the caller gets the same answer for both.
	unique := make([]string, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		normalised := strings.ToLower(strings.TrimSpace(email))
		if normalised == "" {
			continue
		}
		if _, ok := seen[normalised]; ok {
			continue
		}
		seen[normalised] = struct{}{}
		unique = append(unique, normalised)
	}

	for start := 0; start < len(unique); start += maxEmailsPerLookup {
		end := min(start+maxEmailsPerLookup, len(unique))
		if err := s.appendSuppressed(ctx, db, tenantID, unique[start:end], found); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// appendSuppressed runs one chunk of the batch lookup.
func (s *store) appendSuppressed(
	ctx context.Context,
	db *sql.DB,
	tenantID string,
	emails []string,
	into map[string]*entities.Suppression,
) error {
	placeholders := make([]string, len(emails))
	args := make([]any, 0, len(emails)+1)
	args = append(args, tenantID)
	for i, email := range emails {
		// Parameters start at $2: $1 is the tenant.
		placeholders[i] = "$" + strconv.Itoa(i+2)
		args = append(args, email)
	}

	// Exactly one occurrence, so a token that ever appears twice — in a
	// comment, say — fails here rather than sending the database a statement
	// with the placeholder still in it.
	if strings.Count(listSuppressionsByEmailsQuery, emailPlaceholderToken) != 1 {
		return fmt.Errorf(
			"suppression batch query must contain %s exactly once",
			emailPlaceholderToken,
		)
	}
	query := strings.Replace(
		listSuppressionsByEmailsQuery,
		emailPlaceholderToken,
		strings.Join(placeholders, ", "),
		1,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		sup := &entities.Suppression{}
		if err := rows.Scan(
			&sup.ID, &sup.TenantID, &sup.Email, &sup.Reason, &sup.CreatedAt,
		); err != nil {
			return err
		}
		into[strings.ToLower(strings.TrimSpace(sup.Email))] = sup
	}
	return rows.Err()
}
