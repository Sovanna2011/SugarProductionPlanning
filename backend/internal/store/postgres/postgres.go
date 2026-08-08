// Package postgres is the PostgreSQL implementation of the data access layer.
package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a pool against the given DSN and verifies it.
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the underlying pool for transactional callers.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Migrate applies every migration that has not yet been recorded in
// schema_migrations. Each file runs in its own transaction, which the files
// themselves declare with BEGIN/COMMIT.
func (s *Store) Migrate(ctx context.Context, files map[string]string) error {
	// schema_migrations is created by the first migration, so on an empty
	// database it does not exist yet. Ask whether the table is there rather
	// than selecting from it and ignoring the failure: a failed query is
	// logged as an ERROR by the server, and a fresh deployment should not
	// leave alarming lines in the database log on its first boot.
	var migrationsTableExists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT to_regclass('public.schema_migrations') IS NOT NULL`).
		Scan(&migrationsTableExists); err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	if migrationsTableExists {
		rows, err := s.pool.Query(ctx, `SELECT version FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("read applied migrations: %w", err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			applied[v] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	versions := make([]string, 0, len(files))
	for name := range files {
		versions = append(versions, name)
	}
	sort.Strings(versions)

	for _, version := range versions {
		if applied[version] {
			continue
		}
		sql := files[version]
		if _, err := s.pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`,
			version); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}

// filter accumulates optional WHERE clauses and their arguments.
type filter struct {
	clauses []string
	args    []any
}

// add appends a condition, substituting the next positional placeholder for
// each occurrence of "?" in the fragment.
func (f *filter) add(fragment string, args ...any) {
	for _, a := range args {
		f.args = append(f.args, a)
		fragment = strings.Replace(fragment, "?", fmt.Sprintf("$%d", len(f.args)), 1)
	}
	f.clauses = append(f.clauses, fragment)
}

// where renders the accumulated clauses, prefixed with AND so it can be
// appended to a query that already has a WHERE.
func (f *filter) where() string {
	if len(f.clauses) == 0 {
		return ""
	}
	return " AND " + strings.Join(f.clauses, " AND ")
}

func collect[T any](rows pgx.Rows, scan func(pgx.Rows) (T, error)) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
