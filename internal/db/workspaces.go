package db

import "fmt"

// EnsureWorkspace returns the workspace ID for the given cwd path,
// creating it if it doesn't exist.
func (s *Store) EnsureWorkspace(cwdPath string) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM workspaces WHERE path = ?", cwdPath).Scan(&id)
	if err == nil {
		return id, nil
	}

	res, err := s.db.Exec("INSERT INTO workspaces (path) VALUES (?)", cwdPath)
	if err != nil {
		return 0, fmt.Errorf("inserting workspace: %w", err)
	}
	return res.LastInsertId()
}

// GetWorkspaceID returns the workspace ID for the given cwd path, or 0 if not found.
func (s *Store) GetWorkspaceID(cwdPath string) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM workspaces WHERE path = ?", cwdPath).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}
