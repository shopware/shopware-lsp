package indexer

import (
	"os"
	"path/filepath"
)

func (fs *FileScanner) trackedPathsUnder(path string) ([]string, error) {
	prefix := filepath.Clean(path) + string(os.PathSeparator) + "%"
	rows, err := fs.db.Query(
		"SELECT path FROM file_hashes WHERE path = ? OR path LIKE ?",
		filepath.Clean(path),
		prefix,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var tracked string
		if err := rows.Scan(&tracked); err != nil {
			return nil, err
		}
		paths = append(paths, tracked)
	}
	return paths, rows.Err()
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	return keys
}
