package snippet

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/tidwall/pretty"
	"github.com/tidwall/sjson"
)

type SnippetCommandProvider struct {
	snippetIndexer *SnippetIndexer
	host           CommandHost
}

type CommandHost interface {
	IndexFiles(context.Context, []string) error
	PublishDiagnostics(context.Context, []string)
}

func NewSnippetCommandProvider(snippetIndexer *SnippetIndexer, host CommandHost) *SnippetCommandProvider {
	return &SnippetCommandProvider{snippetIndexer: snippetIndexer, host: host}
}

func (s *SnippetCommandProvider) GetCommands(ctx context.Context) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		"shopware/snippet/storefront/getPossibleSnippetFiles": s.getPossibleSnippets,
		"shopware/snippet/storefront/create":                  s.createSnippet,
		"shopware/snippet/storefront/all":                     s.allSnippets,
		"shopware/snippet/admin/all":                          s.allAdminSnippets,
		"shopware/snippet/admin/getPossibleSnippetFiles":      s.getPossibleAdminSnippets,
		"shopware/snippet/admin/create":                       s.createAdminSnippet,
	}
}

func (s *SnippetCommandProvider) allSnippets(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	type snippetItem struct {
		Key  string `json:"key"`
		Text string `json:"text"`
		File string `json:"file"`
	}

	var allSnippets = make(map[string]snippetItem)

	storefrontSnippets, err := s.snippetIndexer.GetAllFrontendSnippets()
	if err != nil {
		return nil, fmt.Errorf("failed to get storefront snippets: %w", err)
	}

	for _, snippet := range storefrontSnippets {
		if _, ok := allSnippets[snippet.Key]; !ok {
			allSnippets[snippet.Key] = snippetItem{
				Key:  snippet.Key,
				Text: snippet.Text,
				File: snippet.File,
			}
		}

		fileName := filepath.Base(snippet.File)
		if strings.Contains(fileName, "en_GB") {
			// Prefer en_GB snippets
			allSnippets[snippet.Key] = snippetItem{
				Key:  snippet.Key,
				Text: snippet.Text,
				File: snippet.File,
			}
		}
	}

	var allSnippetsList []snippetItem
	for _, snippet := range allSnippets {
		allSnippetsList = append(allSnippetsList, snippet)
	}

	slices.SortFunc(allSnippetsList, func(a, b snippetItem) int {
		return strings.Compare(a.Key, b.Key)
	})

	return allSnippetsList, nil
}

func (s *SnippetCommandProvider) allAdminSnippets(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	type snippetItem struct {
		Key  string `json:"key"`
		Text string `json:"text"`
		File string `json:"file"`
	}

	var allSnippets = make(map[string]snippetItem)

	adminSnippets, err := s.snippetIndexer.GetAllAdminSnippets()
	if err != nil {
		return nil, fmt.Errorf("failed to get admin snippets: %w", err)
	}

	for _, snippet := range adminSnippets {
		if _, ok := allSnippets[snippet.Key]; !ok {
			allSnippets[snippet.Key] = snippetItem{
				Key:  snippet.Key,
				Text: snippet.Text,
				File: snippet.File,
			}
		}

		fileName := filepath.Base(snippet.File)
		// Prefer en-GB snippets
		if strings.Contains(fileName, "en-GB") || strings.Contains(fileName, "en.json") {
			allSnippets[snippet.Key] = snippetItem{
				Key:  snippet.Key,
				Text: snippet.Text,
				File: snippet.File,
			}
		}
	}

	var allSnippetsList []snippetItem
	for _, snippet := range allSnippets {
		allSnippetsList = append(allSnippetsList, snippet)
	}

	slices.SortFunc(allSnippetsList, func(a, b snippetItem) int {
		return strings.Compare(a.Key, b.Key)
	})

	return allSnippetsList, nil
}

func (s *SnippetCommandProvider) getPossibleSnippets(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	var params struct {
		FileURI string `json:"fileUri"`
	}

	if err := json.Unmarshal(*args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments for getPossibleSnippets: %w", err)
	}

	// Convert URI to file path
	filePath, err := uriutil.Path(params.FileURI)
	if err != nil {
		return nil, fmt.Errorf("resolve document URI: %w", err)
	}

	// // Find Resources directory
	dirPath := filepath.Dir(filePath)
	resourcesFound := false

	for {
		if filepath.Base(dirPath) == "Resources" {
			resourcesFound = true
			break
		}

		parent := filepath.Dir(dirPath)
		if parent == dirPath {
			// We've reached the root directory
			break
		}
		dirPath = parent
	}

	if !resourcesFound {
		return nil, fmt.Errorf("resources directory not found in any parent directory of %s", filePath)
	}

	snippetDir := filepath.Join(dirPath, "snippet")

	// Find possible snippets
	possibleSnippets := findPossibleSnippets(snippetDir)

	// The user didn't created one yet, we create for him one
	if len(possibleSnippets) == 0 {
		if err := os.MkdirAll(filepath.Join(snippetDir, "en_GB"), os.ModePerm); err != nil {
			return nil, err
		}

		if err := os.WriteFile(filepath.Join(snippetDir, "en_GB", "storefront.en-GB.json"), []byte("{}"), os.ModePerm); err != nil {
			return nil, err
		}

		possibleSnippets = []SnippetFile{
			{
				Path:  filepath.Join(snippetDir, "en_GB", "storefront.en-GB.json"),
				Name:  "storefront.en-GB.json",
				Value: "",
			},
		}
	}

	// Return success message
	return map[string]interface{}{
		"paths": possibleSnippets,
	}, nil
}

func (s *SnippetCommandProvider) createSnippet(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	var params struct {
		FileURI    string        `json:"fileUri"`
		SnippetKey string        `json:"snippetKey"`
		Snippets   []SnippetFile `json:"snippets"`
	}

	if err := json.Unmarshal(*args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments for createSnippet: %w", err)
	}

	files := make([]string, len(params.Snippets))

	for _, snippet := range params.Snippets {
		fileContent, err := os.ReadFile(snippet.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", snippet.Path, err)
		}

		newFile, err := sjson.SetBytes(fileContent, params.SnippetKey, snippet.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to set snippet %s in file %s: %w", params.SnippetKey, snippet.Path, err)
		}

		if err := os.WriteFile(snippet.Path, pretty.Pretty(newFile), os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to write file %s: %w", snippet.Path, err)
		}

		files = append(files, snippet.Path)
	}

	if err := s.host.IndexFiles(ctx, files); err != nil {
		return nil, fmt.Errorf("failed to index files: %w", err)
	}

	s.host.PublishDiagnostics(ctx, []string{params.FileURI})

	return nil, nil
}

type SnippetFile struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (s *SnippetCommandProvider) getPossibleAdminSnippets(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	var params struct {
		FileURI string `json:"fileUri"`
	}

	if err := json.Unmarshal(*args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments for getPossibleAdminSnippets: %w", err)
	}

	// Convert URI to file path
	filePath, err := uriutil.Path(params.FileURI)
	if err != nil {
		return nil, fmt.Errorf("resolve document URI: %w", err)
	}

	// Find Resources/app/administration directory
	dirPath := filepath.Dir(filePath)
	administrationFound := false
	var resourcesDir string

	for {
		if filepath.Base(dirPath) == "administration" {
			parent := filepath.Dir(dirPath)
			if filepath.Base(parent) == "app" {
				grandparent := filepath.Dir(parent)
				if filepath.Base(grandparent) == "Resources" {
					administrationFound = true
					resourcesDir = grandparent
					break
				}
			}
		}

		parent := filepath.Dir(dirPath)
		if parent == dirPath {
			// We've reached the root directory
			break
		}
		dirPath = parent
	}

	if !administrationFound {
		return nil, fmt.Errorf("resources/app/administration directory not found in any parent directory of %s", filePath)
	}

	// Search entire administration/src directory for snippet files
	administrationSrcDir := filepath.Join(resourcesDir, "app", "administration", "src")

	// Find possible snippets anywhere under administration/src in snippet/ directories
	possibleSnippets := findPossibleAdminSnippets(administrationSrcDir)

	// The user didn't create one yet, we create for him one
	if len(possibleSnippets) == 0 {
		snippetDir := filepath.Join(administrationSrcDir, "snippet")
		if err := os.MkdirAll(snippetDir, os.ModePerm); err != nil {
			return nil, err
		}

		if err := os.WriteFile(filepath.Join(snippetDir, "en-GB.json"), []byte("{}"), os.ModePerm); err != nil {
			return nil, err
		}

		possibleSnippets = []SnippetFile{
			{
				Path:  filepath.Join(snippetDir, "en-GB.json"),
				Name:  "en-GB.json",
				Value: "",
			},
		}
	}

	// Return success message
	return map[string]interface{}{
		"paths": possibleSnippets,
	}, nil
}

func (s *SnippetCommandProvider) createAdminSnippet(ctx context.Context, args *json.RawMessage) (interface{}, error) {
	var params struct {
		FileURI    string        `json:"fileUri"`
		SnippetKey string        `json:"snippetKey"`
		Snippets   []SnippetFile `json:"snippets"`
	}

	if err := json.Unmarshal(*args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments for createAdminSnippet: %w", err)
	}

	files := make([]string, 0, len(params.Snippets))

	for _, snippet := range params.Snippets {
		fileContent, err := os.ReadFile(snippet.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", snippet.Path, err)
		}

		newFile, err := sjson.SetBytes(fileContent, params.SnippetKey, snippet.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to set snippet %s in file %s: %w", params.SnippetKey, snippet.Path, err)
		}

		if err := os.WriteFile(snippet.Path, pretty.Pretty(newFile), os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to write file %s: %w", snippet.Path, err)
		}

		files = append(files, snippet.Path)
	}

	if err := s.host.IndexFiles(ctx, files); err != nil {
		return nil, fmt.Errorf("failed to index files: %w", err)
	}

	s.host.PublishDiagnostics(ctx, []string{params.FileURI})

	return nil, nil
}

func findPossibleSnippets(dirPath string) []SnippetFile {
	var possibleSnippets []SnippetFile

	_ = filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if filepath.Ext(path) == ".json" {
			possibleSnippets = append(possibleSnippets, SnippetFile{
				Path:  path,
				Name:  filepath.Base(path),
				Value: "",
			})
		}

		return nil
	})

	// Sort so that en_GB files come first
	for i := 0; i < len(possibleSnippets); i++ {
		if strings.Contains(possibleSnippets[i].Path, "en_GB") {
			// Move this item to the beginning of the slice
			possibleSnippets = append([]SnippetFile{possibleSnippets[i]}, append(possibleSnippets[:i], possibleSnippets[i+1:]...)...)
		}
	}

	return possibleSnippets
}

func findPossibleAdminSnippets(dirPath string) []SnippetFile {
	var possibleSnippets []SnippetFile

	_ = filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		// Only include JSON files that are inside a "snippet" directory
		if filepath.Ext(path) == ".json" && filepath.Base(filepath.Dir(path)) == "snippet" {
			possibleSnippets = append(possibleSnippets, SnippetFile{
				Path:  path,
				Name:  filepath.Base(path),
				Value: "",
			})
		}

		return nil
	})

	// Sort so that en-GB files come first (admin uses hyphen, not underscore)
	for i := 0; i < len(possibleSnippets); i++ {
		if strings.Contains(possibleSnippets[i].Name, "en-GB") || possibleSnippets[i].Name == "en.json" {
			// Move this item to the beginning of the slice
			possibleSnippets = append([]SnippetFile{possibleSnippets[i]}, append(possibleSnippets[:i], possibleSnippets[i+1:]...)...)
		}
	}

	return possibleSnippets
}
