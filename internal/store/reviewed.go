package store

import "fmt"

// Reviewed files are keyed by the branch they were reviewed against as well as
// the repository. The same file read against main and against a release branch
// are two different readings, and carrying one over to the other would tell a
// reviewer they had seen a diff they have not.

// MarkReviewed records that a file has been read, or clears it.
func (s *Store) MarkReviewed(root, base, path string, reviewed bool) error {
	var err error
	if reviewed {
		_, err = s.db.Exec(
			`INSERT INTO reviewed_files (root, base, path) VALUES (?, ?, ?)
			 ON CONFLICT(root, base, path) DO UPDATE SET reviewed_at = datetime('now')`,
			root, base, path)
	} else {
		_, err = s.db.Exec(
			`DELETE FROM reviewed_files WHERE root = ? AND base = ? AND path = ?`,
			root, base, path)
	}
	if err != nil {
		return fmt.Errorf("mark reviewed: %w", err)
	}
	return nil
}

// ReviewedFiles returns the paths already read for this repository and base.
func (s *Store) ReviewedFiles(root, base string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT path FROM reviewed_files WHERE root = ? AND base = ? ORDER BY path`,
		root, base)
	if err != nil {
		return nil, fmt.Errorf("list reviewed: %w", err)
	}
	defer rows.Close()

	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("scan reviewed: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// ClearReviewed forgets a whole review. Used when a branch is re-reviewed from
// the start rather than continued.
func (s *Store) ClearReviewed(root, base string) error {
	_, err := s.db.Exec(`DELETE FROM reviewed_files WHERE root = ? AND base = ?`, root, base)
	if err != nil {
		return fmt.Errorf("clear reviewed: %w", err)
	}
	return nil
}
