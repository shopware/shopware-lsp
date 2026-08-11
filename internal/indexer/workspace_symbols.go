package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// WorkspaceSymbolKind mirrors the numeric LSP SymbolKind values without
// coupling the persistence layer to the protocol package.
type WorkspaceSymbolKind int

const (
	WorkspaceSymbolFile WorkspaceSymbolKind = 1 + iota
	WorkspaceSymbolModule
	WorkspaceSymbolNamespace
	WorkspaceSymbolPackage
	WorkspaceSymbolClass
	WorkspaceSymbolMethod
	WorkspaceSymbolProperty
	WorkspaceSymbolField
	WorkspaceSymbolConstructor
	WorkspaceSymbolEnum
	WorkspaceSymbolInterface
	WorkspaceSymbolFunction
	WorkspaceSymbolVariable
	WorkspaceSymbolConstant
	WorkspaceSymbolString
	WorkspaceSymbolNumber
	WorkspaceSymbolBoolean
	WorkspaceSymbolArray
	WorkspaceSymbolObject
	WorkspaceSymbolKey
	WorkspaceSymbolNull
	WorkspaceSymbolEnumMember
	WorkspaceSymbolStruct
	WorkspaceSymbolEvent
	WorkspaceSymbolOperator
	WorkspaceSymbolTypeParameter
)

// Symbol priorities are applied after textual relevance. This keeps an exact
// member match useful while ensuring equally good type matches sort above
// members and framework aliases.
const (
	WorkspaceSymbolPriorityFrameworkMember = 50
	WorkspaceSymbolPriorityPHPMember       = 60
	WorkspaceSymbolPriorityFramework       = 70
	WorkspaceSymbolPriorityPHPGlobal       = 80
	WorkspaceSymbolPriorityPHPType         = 100
)

type WorkspaceSymbolPosition struct {
	Line      int
	Character int
}

type WorkspaceSymbolRange struct {
	Start WorkspaceSymbolPosition
	End   WorkspaceSymbolPosition
}

func WorkspaceSymbolRangeFromText(
	file *ParsedFile,
	rangeValue cst.TextRange,
) WorkspaceSymbolRange {
	if file == nil || rangeValue == (cst.TextRange{}) {
		return WorkspaceSymbolRange{}
	}
	startLine, startCharacter := file.LineIndex().PositionUTF16(rangeValue.Start)
	endLine, endCharacter := file.LineIndex().PositionUTF16(rangeValue.End)
	return WorkspaceSymbolRange{
		Start: WorkspaceSymbolPosition{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: WorkspaceSymbolPosition{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
}

// WorkspaceSymbolRangeAtLine converts the one-based lines used by legacy
// framework records into the zero-based LSP coordinate system.
func WorkspaceSymbolRangeAtLine(line int) WorkspaceSymbolRange {
	line = max(1, line) - 1
	position := WorkspaceSymbolPosition{Line: line}
	return WorkspaceSymbolRange{Start: position, End: position}
}

// WorkspaceSymbol is the language-neutral persisted workspace symbol model.
// Aliases are searchable but are not returned to LSP clients.
type WorkspaceSymbol struct {
	Name          string
	ContainerName string
	Aliases       []string
	Path          string
	Domain        string
	Kind          WorkspaceSymbolKind
	Priority      int
	Range         WorkspaceSymbolRange
}

type WorkspaceSymbolDocument struct {
	Path    string
	Symbols []WorkspaceSymbol
}

// WorkspaceSymbolContributor lets a normal file indexer publish declarations
// from the same prepared value it already computed for persistence.
type WorkspaceSymbolContributor interface {
	WorkspaceSymbols(
		file *ParsedFile,
		prepared any,
	) ([]WorkspaceSymbol, error)
}

// WorkspaceSymbolCatalog is a compact, SQLite-backed FTS catalog. Interactive
// queries do not need to restore any of the language-specific object graphs.
type WorkspaceSymbolCatalog struct {
	db        *sql.DB
	store     *Store
	ownedDB   bool
	bulk      atomic.Bool
	bulkDirty atomic.Bool
}

const workspaceSymbolSchema = `
	CREATE TABLE IF NOT EXISTS workspace_symbols (
		id INTEGER PRIMARY KEY,
		owner_path TEXT NOT NULL,
		file_path TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		container_name TEXT NOT NULL DEFAULT '',
		aliases TEXT NOT NULL DEFAULT '',
		search_text TEXT NOT NULL,
		domain TEXT NOT NULL DEFAULT '',
		kind INTEGER NOT NULL,
		priority INTEGER NOT NULL,
		start_line INTEGER NOT NULL,
		start_character INTEGER NOT NULL,
		end_line INTEGER NOT NULL,
		end_character INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_workspace_symbols_path
		ON workspace_symbols(owner_path);
	CREATE INDEX IF NOT EXISTS idx_workspace_symbols_priority
		ON workspace_symbols(priority DESC, name);
	CREATE VIRTUAL TABLE IF NOT EXISTS workspace_symbols_fts USING fts4(
		search_text,
		content='workspace_symbols',
		tokenize=unicode61
	);
	CREATE TABLE IF NOT EXISTS workspace_symbol_meta (
		key TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	);
`

func NewWorkspaceSymbolCatalog(store *Store) (*WorkspaceSymbolCatalog, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("workspace symbol store is required")
	}
	if _, err := store.db.Exec(workspaceSymbolSchema); err != nil {
		return nil, fmt.Errorf("initialize workspace symbol catalog: %w", err)
	}
	return &WorkspaceSymbolCatalog{db: store.db, store: store}, nil
}

// BeginBulkPopulation defers FTS maintenance while a cold workspace index is
// writing many small file transactions. EndBulkPopulation builds the inverted
// index once from the completed content table.
func (catalog *WorkspaceSymbolCatalog) BeginBulkPopulation() {
	if catalog != nil {
		catalog.bulkDirty.Store(false)
		catalog.bulk.Store(true)
	}
}

func (catalog *WorkspaceSymbolCatalog) EndBulkPopulation(
	ctx context.Context,
) error {
	if catalog == nil || catalog.store == nil {
		return nil
	}
	catalog.bulk.Store(false)
	if !catalog.bulkDirty.Swap(false) {
		return nil
	}
	mutation, err := catalog.store.BeginMutation(ctx)
	if err != nil {
		return err
	}
	if _, err := mutation.tx.Exec(`
		INSERT INTO workspace_symbols_fts(workspace_symbols_fts)
		VALUES('rebuild')
	`); err != nil {
		return errors.Join(
			fmt.Errorf("rebuild workspace symbol FTS: %w", err),
			mutation.Rollback(),
		)
	}
	return mutation.Commit()
}

// OpenWorkspaceSymbolCatalog opens an existing catalog without creating or
// migrating it. It is used by lightweight CLI reads before a workspace exists.
func OpenWorkspaceSymbolCatalog(dbPath string) (*WorkspaceSymbolCatalog, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_query_only=1")
	if err != nil {
		return nil, fmt.Errorf("open workspace symbol catalog: %w", err)
	}
	db.SetMaxOpenConns(1)
	var exists int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'workspace_symbols_fts'
	`).Scan(&exists); err != nil || exists == 0 {
		_ = db.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect workspace symbol catalog: %w", err)
		}
		return nil, fmt.Errorf("workspace symbol catalog is not initialized")
	}
	return &WorkspaceSymbolCatalog{db: db, ownedDB: true}, nil
}

func (catalog *WorkspaceSymbolCatalog) Close() error {
	if catalog == nil || !catalog.ownedDB || catalog.db == nil {
		return nil
	}
	return catalog.db.Close()
}

func (catalog *WorkspaceSymbolCatalog) Ready(ctx context.Context) (bool, error) {
	if catalog == nil || catalog.db == nil {
		return false, nil
	}
	var ready int
	err := catalog.db.QueryRowContext(ctx, `
		SELECT value FROM workspace_symbol_meta WHERE key = 'ready'
	`).Scan(&ready)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query workspace symbol readiness: %w", err)
	}
	return ready != 0, nil
}

func (catalog *WorkspaceSymbolCatalog) SetReady(
	ctx context.Context,
	ready bool,
) error {
	if catalog == nil || catalog.store == nil {
		return fmt.Errorf("writable workspace symbol catalog is required")
	}
	mutation, err := catalog.store.BeginMutation(ctx)
	if err != nil {
		return err
	}
	if err := catalog.SetReadyIn(mutation, ready); err != nil {
		return errors.Join(err, mutation.Rollback())
	}
	return mutation.Commit()
}

func (catalog *WorkspaceSymbolCatalog) SetReadyIn(
	mutation *Mutation,
	ready bool,
) error {
	if mutation == nil || mutation.tx == nil {
		return fmt.Errorf("workspace symbol mutation is required")
	}
	value := 0
	if ready {
		value = 1
	}
	_, err := mutation.tx.Exec(`
		INSERT INTO workspace_symbol_meta(key, value) VALUES('ready', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, value)
	return err
}

func (catalog *WorkspaceSymbolCatalog) ReplaceFiles(
	ctx context.Context,
	files []WorkspaceSymbolDocument,
) error {
	if catalog == nil || catalog.store == nil {
		return fmt.Errorf("writable workspace symbol catalog is required")
	}
	if len(files) == 0 {
		return nil
	}
	mutation, err := catalog.store.BeginMutation(ctx)
	if err != nil {
		return err
	}
	if err := catalog.ReplaceFilesIn(mutation, files); err != nil {
		return errors.Join(err, mutation.Rollback())
	}
	return mutation.Commit()
}

func (catalog *WorkspaceSymbolCatalog) DeleteFiles(
	ctx context.Context,
	paths []string,
) error {
	if catalog == nil || catalog.store == nil {
		return fmt.Errorf("writable workspace symbol catalog is required")
	}
	if len(paths) == 0 {
		return nil
	}
	mutation, err := catalog.store.BeginMutation(ctx)
	if err != nil {
		return err
	}
	if err := catalog.DeleteFilesIn(mutation, paths); err != nil {
		return errors.Join(err, mutation.Rollback())
	}
	return mutation.Commit()
}

func (catalog *WorkspaceSymbolCatalog) Clear(ctx context.Context) error {
	if catalog == nil || catalog.store == nil {
		return fmt.Errorf("writable workspace symbol catalog is required")
	}
	mutation, err := catalog.store.BeginMutation(ctx)
	if err != nil {
		return err
	}
	if catalog.bulk.Load() {
		catalog.bulkDirty.Store(true)
	}
	if err := catalog.ClearIn(mutation); err != nil {
		return errors.Join(err, mutation.Rollback())
	}
	return mutation.Commit()
}

func (catalog *WorkspaceSymbolCatalog) ReplaceFilesIn(
	mutation *Mutation,
	files []WorkspaceSymbolDocument,
) error {
	if mutation == nil || mutation.tx == nil {
		return fmt.Errorf("workspace symbol mutation is required")
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if file.Path != "" {
			paths = append(paths, file.Path)
		}
	}
	if catalog.bulk.Load() && len(paths) != 0 {
		catalog.bulkDirty.Store(true)
	}
	if err := catalog.DeleteFilesIn(mutation, paths); err != nil {
		return err
	}
	type symbolInsert struct {
		ownerPath    string
		locationPath string
		symbol       WorkspaceSymbol
		aliases      string
		searchText   string
	}
	var inserts []symbolInsert
	for _, file := range files {
		for _, symbol := range file.Symbols {
			if symbol.Name == "" || file.Path == "" {
				continue
			}
			aliases := strings.Join(compactStrings(symbol.Aliases), "\x1f")
			searchText := workspaceSymbolIndexText(symbol)
			locationPath := symbol.Path
			if locationPath == file.Path {
				locationPath = ""
			}
			inserts = append(inserts, symbolInsert{
				ownerPath: file.Path, locationPath: locationPath,
				symbol: symbol, aliases: aliases, searchText: searchText,
			})
		}
	}
	// Thirteen parameters per symbol stay below SQLite's traditional 999
	// parameter limit. Each batch crosses cgo only twice: once for metadata and
	// once for FTS, rather than twice per declaration.
	const insertBatchSize = 75
	for start := 0; start < len(inserts); start += insertBatchSize {
		end := min(start+insertBatchSize, len(inserts))
		batch := inserts[start:end]
		symbolStatement, err := mutation.Prepare(
			workspaceSymbolInsertSQL(len(batch)),
		)
		if err != nil {
			return err
		}
		args := make([]any, 0, len(batch)*13)
		for _, insert := range batch {
			symbol := insert.symbol
			args = append(args,
				insert.ownerPath,
				insert.locationPath,
				symbol.Name,
				symbol.ContainerName,
				insert.aliases,
				insert.searchText,
				symbol.Domain,
				symbol.Kind,
				symbol.Priority,
				max(0, symbol.Range.Start.Line),
				max(0, symbol.Range.Start.Character),
				max(0, symbol.Range.End.Line),
				max(0, symbol.Range.End.Character),
			)
		}
		inserted, err := symbolStatement.Exec(args...)
		if err != nil {
			return fmt.Errorf("insert workspace symbol batch: %w", err)
		}
		lastID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		firstID := lastID - int64(len(batch)) + 1
		if catalog.bulk.Load() {
			continue
		}
		ftsStatement, err := mutation.Prepare(
			workspaceSymbolFTSInsertSQL(len(batch)),
		)
		if err != nil {
			return err
		}
		ftsArgs := make([]any, 0, len(batch)*2)
		for index, insert := range batch {
			ftsArgs = append(
				ftsArgs,
				firstID+int64(index),
				insert.searchText,
			)
		}
		if _, err := ftsStatement.Exec(ftsArgs...); err != nil {
			return fmt.Errorf("index workspace symbol batch: %w", err)
		}
	}
	return nil
}

func workspaceSymbolInsertSQL(count int) string {
	const prefix = `INSERT INTO workspace_symbols(
		owner_path, file_path, name, container_name, aliases, search_text, domain,
		kind, priority, start_line, start_character, end_line, end_character
	) VALUES `
	return prefix + strings.TrimSuffix(
		strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),", count),
		",",
	)
}

func workspaceSymbolFTSInsertSQL(count int) string {
	const prefix = `INSERT INTO workspace_symbols_fts(docid, search_text) VALUES `
	return prefix + strings.TrimSuffix(strings.Repeat("(?, ?),", count), ",")
}

func (catalog *WorkspaceSymbolCatalog) DeleteFilesIn(
	mutation *Mutation,
	paths []string,
) error {
	if len(paths) == 0 {
		return nil
	}
	if mutation == nil || mutation.tx == nil {
		return fmt.Errorf("workspace symbol mutation is required")
	}
	const batchSize = 128
	for start := 0; start < len(paths); start += batchSize {
		end := min(start+batchSize, len(paths))
		batch := paths[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for index, path := range batch {
			args[index] = path
		}
		if !catalog.bulk.Load() {
			if _, err := mutation.tx.Exec(
				"DELETE FROM workspace_symbols_fts WHERE docid IN ("+
					"SELECT id FROM workspace_symbols WHERE owner_path IN ("+
					placeholders+"))",
				args...,
			); err != nil {
				return fmt.Errorf("delete workspace symbol search rows: %w", err)
			}
		}
		if _, err := mutation.tx.Exec(
			"DELETE FROM workspace_symbols WHERE owner_path IN ("+
				placeholders+")",
			args...,
		); err != nil {
			return fmt.Errorf("delete workspace symbols: %w", err)
		}
	}
	return nil
}

func (catalog *WorkspaceSymbolCatalog) ClearIn(mutation *Mutation) error {
	if mutation == nil || mutation.tx == nil {
		return fmt.Errorf("workspace symbol mutation is required")
	}
	for _, query := range []string{
		"DELETE FROM workspace_symbols_fts",
		"DELETE FROM workspace_symbols",
		"DELETE FROM workspace_symbol_meta",
	} {
		if _, err := mutation.tx.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func (catalog *WorkspaceSymbolCatalog) Query(
	ctx context.Context,
	query string,
	limit int,
) ([]WorkspaceSymbol, error) {
	return catalog.QueryWithOptions(
		ctx, query, limit, WorkspaceSymbolQueryOptions{},
	)
}

type WorkspaceSymbolQueryOptions struct {
	ExcludedDomains []string
}

func (catalog *WorkspaceSymbolCatalog) QueryWithOptions(
	ctx context.Context,
	query string,
	limit int,
	options WorkspaceSymbolQueryOptions,
) ([]WorkspaceSymbol, error) {
	if catalog == nil || catalog.db == nil || limit <= 0 {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	candidateLimit := min(max(limit*8, 1000), 8000)
	baseSelect := `
		SELECT s.name, s.container_name, s.aliases,
			CASE WHEN s.file_path = '' THEN s.owner_path ELSE s.file_path END,
			s.domain,
			s.kind, s.priority, s.start_line, s.start_character,
			s.end_line, s.end_character
		FROM workspace_symbols s
	`
	var (
		rows *sql.Rows
		err  error
	)
	domainPredicate, domainArguments := workspaceSymbolDomainExclusion(
		options.ExcludedDomains,
	)
	if query == "" {
		statement := baseSelect
		if domainPredicate != "" {
			statement += " WHERE " + domainPredicate
		}
		statement += " ORDER BY s.priority DESC, s.name LIMIT ?"
		arguments := append(domainArguments, candidateLimit)
		rows, err = catalog.db.QueryContext(
			ctx,
			statement,
			arguments...,
		)
	} else {
		match := workspaceSymbolMatchQuery(query)
		if match == "" {
			return nil, nil
		}
		statement := baseSelect + `
				JOIN workspace_symbols_fts f ON f.docid = s.id
				WHERE workspace_symbols_fts MATCH ?`
		arguments := []any{match}
		if domainPredicate != "" {
			statement += " AND " + domainPredicate
			arguments = append(arguments, domainArguments...)
		}
		statement += " ORDER BY s.priority DESC, s.name LIMIT ?"
		arguments = append(arguments, candidateLimit)
		rows, err = catalog.db.QueryContext(
			ctx,
			statement,
			arguments...,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query workspace symbols: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type rankedSymbol struct {
		symbol WorkspaceSymbol
		score  int
	}
	ranked := make([]rankedSymbol, 0, min(candidateLimit, limit*2))
	for rows.Next() {
		var aliases string
		var current rankedSymbol
		if err := rows.Scan(
			&current.symbol.Name,
			&current.symbol.ContainerName,
			&aliases,
			&current.symbol.Path,
			&current.symbol.Domain,
			&current.symbol.Kind,
			&current.symbol.Priority,
			&current.symbol.Range.Start.Line,
			&current.symbol.Range.Start.Character,
			&current.symbol.Range.End.Line,
			&current.symbol.Range.End.Character,
		); err != nil {
			return nil, err
		}
		if aliases != "" {
			current.symbol.Aliases = strings.Split(aliases, "\x1f")
		}
		current.score = workspaceSymbolTextScore(query, current.symbol)
		ranked = append(ranked, current)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(ranked, func(left, right rankedSymbol) int {
		if left.score != right.score {
			return left.score - right.score
		}
		if left.symbol.Priority != right.symbol.Priority {
			return right.symbol.Priority - left.symbol.Priority
		}
		if compared := strings.Compare(
			strings.ToLower(left.symbol.Name),
			strings.ToLower(right.symbol.Name),
		); compared != 0 {
			return compared
		}
		if compared := strings.Compare(
			left.symbol.ContainerName,
			right.symbol.ContainerName,
		); compared != 0 {
			return compared
		}
		return strings.Compare(left.symbol.Path, right.symbol.Path)
	})
	result := make([]WorkspaceSymbol, 0, min(limit, len(ranked)))
	for _, current := range ranked[:min(limit, len(ranked))] {
		result = append(result, current.symbol)
	}
	return result, nil
}

func workspaceSymbolDomainExclusion(domains []string) (string, []any) {
	unique := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain != "" {
			unique[domain] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return "", nil
	}
	ordered := make([]string, 0, len(unique))
	for domain := range unique {
		ordered = append(ordered, domain)
	}
	slices.Sort(ordered)
	placeholders := make([]string, len(ordered))
	arguments := make([]any, len(ordered))
	for index, domain := range ordered {
		placeholders[index] = "?"
		arguments[index] = domain
	}
	return "s.domain NOT IN (" + strings.Join(placeholders, ",") + ")", arguments
}

func workspaceSymbolTextScore(query string, symbol WorkspaceSymbol) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 4
	}
	best := 5
	values := make([]string, 0, 2+len(symbol.Aliases))
	values = append(values, symbol.Name, symbol.ContainerName)
	values = append(values, symbol.Aliases...)
	for _, value := range values {
		value = strings.ToLower(value)
		switch {
		case value == query:
			best = min(best, 0)
		case strings.HasPrefix(value, query):
			best = min(best, 1)
		case strings.Contains(value, query):
			best = min(best, 2)
		default:
			best = min(best, 3)
		}
	}
	return best
}

func workspaceSymbolMatchQuery(query string) string {
	terms := identifierWords(query)
	if len(terms) == 0 {
		return ""
	}
	identifierOnly := true
	for _, current := range query {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			identifierOnly = false
			break
		}
	}
	if identifierOnly {
		return strings.ToLower(strings.Join(terms, "")) + "*"
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		// identifierWords strips FTS operators and punctuation, so the token
		// can safely use SQLite's unquoted prefix form.
		parts = append(parts, strings.ToLower(term)+"*")
	}
	return strings.Join(parts, " AND ")
}

func workspaceSymbolIndexText(symbol WorkspaceSymbol) string {
	values := []string{symbol.Name}
	if container := workspaceSymbolLeafName(symbol.ContainerName); container != "" {
		values = append(values, container)
	}
	for _, alias := range symbol.Aliases {
		if leaf := workspaceSymbolLeafName(alias); leaf != "" {
			values = append(values, leaf)
		}
	}
	return workspaceSymbolSearchText(values...)
}

func workspaceSymbolLeafName(value string) string {
	parts := strings.FieldsFunc(value, func(current rune) bool {
		return current == '\\' || current == '/' || current == '·' ||
			current == ':'
	})
	if len(parts) == 0 {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func workspaceSymbolSearchText(values ...string) string {
	var result []string
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, chunk := range strings.FieldsFunc(value, func(current rune) bool {
			return !unicode.IsLetter(current) && !unicode.IsDigit(current)
		}) {
			words := identifierWords(chunk)
			for _, word := range words {
				appendUniqueString(&result, seen, strings.ToLower(word))
			}
			for start := 0; start < len(words); start++ {
				var joined strings.Builder
				for _, word := range words[start:] {
					joined.WriteString(word)
				}
				appendUniqueString(
					&result,
					seen,
					strings.ToLower(joined.String()),
				)
			}
		}
	}
	return strings.Join(result, " ")
}

func identifierWords(value string) []string {
	runes := []rune(value)
	var words []string
	start := -1
	for index, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			if start >= 0 {
				words = append(words, string(runes[start:index]))
				start = -1
			}
			continue
		}
		if start < 0 {
			start = index
			continue
		}
		previous := runes[index-1]
		nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		boundary := unicode.IsUpper(current) && unicode.IsLower(previous) ||
			unicode.IsUpper(current) && unicode.IsUpper(previous) && nextLower ||
			unicode.IsDigit(current) != unicode.IsDigit(previous)
		if boundary {
			words = append(words, string(runes[start:index]))
			start = index
		}
	}
	if start >= 0 {
		words = append(words, string(runes[start:]))
	}
	return compactStrings(words)
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func appendUniqueString(result *[]string, seen map[string]struct{}, value string) {
	if value == "" {
		return
	}
	if _, exists := seen[value]; exists {
		return
	}
	seen[value] = struct{}{}
	*result = append(*result, value)
}
