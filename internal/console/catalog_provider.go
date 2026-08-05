package console

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/pathmatch"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ListCatalogCommand = "shopware/symfony/console/commands"

const catalogLimit = 2_000

type CatalogProvider struct {
	index *Index
	root  string
}

func NewCatalogProvider(index *Index, workspaceRoots ...string) *CatalogProvider {
	provider := &CatalogProvider{index: index}
	if len(workspaceRoots) != 0 {
		provider.root = filepath.Clean(workspaceRoots[0])
	}
	return provider
}

type CatalogRequest struct {
	Query    string `json:"query,omitempty"`
	FileGlob string `json:"fileGlob,omitempty"`
}

type CatalogInput struct {
	Name        string `json:"name"`
	Shortcut    string `json:"shortcut,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

type CatalogEntry struct {
	Name        string         `json:"name"`
	Canonical   string         `json:"canonical,omitempty"`
	Description string         `json:"description,omitempty"`
	Class       string         `json:"class,omitempty"`
	Method      string         `json:"method,omitempty"`
	FileURI     string         `json:"fileUri,omitempty"`
	FilePath    string         `json:"filePath,omitempty"`
	Arguments   []CatalogInput `json:"arguments,omitempty"`
	Options     []CatalogInput `json:"options,omitempty"`
}

func (p *CatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListCatalogCommand: p.list,
	}
}

func (p *CatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("symfony console catalog is unavailable")
	}
	var request CatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf("invalid console catalog request: %w", err)
		}
	}
	return p.CatalogWithRequest(ctx, request)
}

func (p *CatalogProvider) Catalog(
	ctx context.Context,
	query string,
) ([]CatalogEntry, error) {
	return p.CatalogWithRequest(ctx, CatalogRequest{Query: query})
}

func (p *CatalogProvider) CatalogWithRequest(
	ctx context.Context,
	request CatalogRequest,
) ([]CatalogEntry, error) {
	if p == nil || p.index == nil {
		return nil, fmt.Errorf("symfony console catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	commands, err := p.index.GetCommands()
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	fileGlob := strings.TrimSpace(request.FileGlob)
	entries := make(map[string]CatalogEntry)
	for _, command := range commands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if command.Name == "" || !catalogCommandMatches(command, query) {
			continue
		}
		filePath := catalogCommandFilePath(p.root, command.File)
		if fileGlob != "" &&
			(filePath == "" || !pathmatch.Ant(fileGlob, filePath)) {
			continue
		}
		key := strings.ToLower(command.Name)
		entry := catalogEntry(command, filePath)
		current, exists := entries[key]
		if !exists || catalogEntryScore(entry) > catalogEntryScore(current) {
			entries[key] = entry
		}
	}
	result := make([]CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	if len(result) > catalogLimit {
		result = result[:catalogLimit]
	}
	return result, nil
}

func catalogCommandMatches(command Command, query string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		command.Name,
		command.Canonical,
		command.Description,
		command.Class,
		command.Method,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func catalogEntry(command Command, filePath string) CatalogEntry {
	entry := CatalogEntry{
		Name:        command.Name,
		Canonical:   command.Canonical,
		Description: command.Description,
		Class:       command.Class,
		Method:      command.Method,
		FilePath:    filePath,
		Arguments:   catalogInputs(command.Arguments),
		Options:     catalogInputs(command.Options),
	}
	if command.File != "" {
		entry.FileURI = uriutil.FileURI(command.File)
	}
	return entry
}

func catalogCommandFilePath(root, path string) string {
	if root == "" || path == "" {
		return ""
	}
	relative, err := filepath.Rel(root, path)
	if err != nil ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

func catalogInputs(inputs []Input) []CatalogInput {
	if len(inputs) == 0 {
		return nil
	}
	result := make([]CatalogInput, 0, len(inputs))
	for _, input := range inputs {
		if input.Name == "" {
			continue
		}
		result = append(result, CatalogInput{
			Name:        input.Name,
			Shortcut:    input.Shortcut,
			Mode:        input.Mode,
			Description: input.Description,
			Default:     input.Default,
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func catalogEntryScore(entry CatalogEntry) int {
	score := 0
	if entry.FileURI != "" {
		score++
	}
	if entry.FilePath != "" {
		score++
	}
	if entry.Class != "" {
		score += 2
	}
	if entry.Method != "" {
		score++
	}
	if len(entry.Arguments)+len(entry.Options) != 0 {
		score++
	}
	return score
}

var _ lsp.CommandProvider = (*CatalogProvider)(nil)
