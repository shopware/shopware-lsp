package entityschema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const SnapshotFormatVersion = 2
const SnapshotRelativeDirectory = "src/Resources/shopware-lsp/schema"

type SnapshotKind string

const (
	SnapshotMigration SnapshotKind = "migration"
	SnapshotBaseline  SnapshotKind = "baseline"
	SnapshotMerge     SnapshotKind = "merge"
)

type PluginIdentity struct {
	ComposerName string `json:"composerName"`
}

type MigrationReference struct {
	Path      string `json:"path"`
	Timestamp int64  `json:"timestamp"`
	SHA256    string `json:"sha256"`
}

type Decision struct {
	Kind   string `json:"kind"`
	Entity string `json:"entity"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Snapshot struct {
	FormatVersion   int                  `json:"formatVersion"`
	ID              string               `json:"id"`
	Parents         []string             `json:"parents"`
	Kind            SnapshotKind         `json:"kind"`
	Plugin          PluginIdentity       `json:"plugin"`
	ShopwareVersion string               `json:"shopwareVersion,omitempty"`
	Migrations      []MigrationReference `json:"migrations"`
	Schema          Schema               `json:"schema"`
	Decisions       []Decision           `json:"decisions"`
}

func (s Snapshot) normalized(includeID bool) Snapshot {
	s.FormatVersion = SnapshotFormatVersion
	if !includeID {
		s.ID = ""
	}
	s.Parents = append([]string(nil), s.Parents...)
	sort.Strings(s.Parents)
	s.Migrations = append([]MigrationReference(nil), s.Migrations...)
	sort.Slice(s.Migrations, func(i, j int) bool {
		if s.Migrations[i].Timestamp != s.Migrations[j].Timestamp {
			return s.Migrations[i].Timestamp < s.Migrations[j].Timestamp
		}
		return s.Migrations[i].Path < s.Migrations[j].Path
	})
	s.Decisions = append([]Decision(nil), s.Decisions...)
	sort.Slice(s.Decisions, func(i, j int) bool {
		left := s.Decisions[i].Kind + ":" + s.Decisions[i].Entity + ":" + s.Decisions[i].From + ":" + s.Decisions[i].To
		right := s.Decisions[j].Kind + ":" + s.Decisions[j].Entity + ":" + s.Decisions[j].From + ":" + s.Decisions[j].To
		return left < right
	})
	s.Schema = s.Schema.Normalize()
	return s
}

func (s Snapshot) Seal() (Snapshot, error) {
	s = s.normalized(false)
	encoded, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, err
	}
	hash := sha256.Sum256(encoded)
	s.ID = hex.EncodeToString(hash[:])
	return s.normalized(true), nil
}

func MarshalSnapshot(snapshot Snapshot) ([]byte, error) {
	sealed, err := snapshot.Seal()
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(sealed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func ParseSnapshot(source []byte) (Snapshot, error) {
	var snapshot Snapshot
	if err := json.Unmarshal(source, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.FormatVersion != SnapshotFormatVersion {
		return Snapshot{}, fmt.Errorf("unsupported entity snapshot format %d", snapshot.FormatVersion)
	}
	if snapshot.ID == "" {
		return Snapshot{}, errors.New("entity snapshot id is empty")
	}
	sealed, err := snapshot.Seal()
	if err != nil {
		return Snapshot{}, err
	}
	if sealed.ID != snapshot.ID {
		return Snapshot{}, fmt.Errorf("entity snapshot id does not match content")
	}
	return snapshot.normalized(true), nil
}

type SnapshotFile struct {
	Path     string
	Snapshot Snapshot
}

func ReadSnapshots(pluginRoot string) ([]SnapshotFile, error) {
	directory := filepath.Join(pluginRoot, filepath.FromSlash(SnapshotRelativeDirectory))
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []SnapshotFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".snapshot.json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		snapshot, parseErr := ParseSnapshot(content)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		result = append(result, SnapshotFile{Path: path, Snapshot: snapshot})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

type SnapshotGraph struct {
	Files   map[string]SnapshotFile
	Leaves  []SnapshotFile
	Missing map[string][]string
}

func BuildSnapshotGraph(files []SnapshotFile) (SnapshotGraph, error) {
	graph := SnapshotGraph{
		Files:   make(map[string]SnapshotFile, len(files)),
		Missing: make(map[string][]string),
	}
	children := make(map[string]int)
	for _, file := range files {
		id := file.Snapshot.ID
		if _, duplicate := graph.Files[id]; duplicate {
			return SnapshotGraph{}, fmt.Errorf("duplicate entity snapshot id %s", id)
		}
		graph.Files[id] = file
	}
	for _, file := range files {
		for _, parent := range file.Snapshot.Parents {
			if _, found := graph.Files[parent]; !found {
				graph.Missing[file.Snapshot.ID] = append(graph.Missing[file.Snapshot.ID], parent)
				continue
			}
			children[parent]++
		}
	}
	if err := detectSnapshotCycles(graph.Files); err != nil {
		return SnapshotGraph{}, err
	}
	for id, file := range graph.Files {
		if children[id] == 0 {
			graph.Leaves = append(graph.Leaves, file)
		}
	}
	sort.Slice(graph.Leaves, func(i, j int) bool {
		return graph.Leaves[i].Snapshot.ID < graph.Leaves[j].Snapshot.ID
	})
	return graph, nil
}

func detectSnapshotCycles(files map[string]SnapshotFile) error {
	const (
		unseen = iota
		visiting
		done
	)
	state := make(map[string]int, len(files))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case visiting:
			return fmt.Errorf("entity snapshot ancestry contains a cycle at %s", id)
		case done:
			return nil
		}
		state[id] = visiting
		for _, parent := range files[id].Snapshot.Parents {
			if _, found := files[parent]; found {
				if err := visit(parent); err != nil {
					return err
				}
			}
		}
		state[id] = done
		return nil
	}
	for id := range files {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func FileSHA256(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}
