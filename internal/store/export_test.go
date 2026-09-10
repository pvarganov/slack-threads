package store

import "database/sql"

// DBOf exposes the raw handle so tests can seed tables whose repositories are
// implemented in later tasks.
func DBOf(s *Store) *sql.DB { return s.db }
