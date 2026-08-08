package twig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

const maximumExtendsDepth = 10

// UpstreamResolution describes the statically resolvable parents of one
// overridden block. ParentResolved distinguishes a missing block from a
// missing checkout: removal diagnostics are only sound in the former case.
type UpstreamResolution struct {
	Candidates     []TwigBlockHash
	ChainPaths     []string
	ParentResolved bool
}

type VersioningService struct {
	root            string
	index           *TwigIndexer
	fallbackVersion string
}

func NewVersioningService(
	root string,
	index *TwigIndexer,
	fallbackVersion string,
) *VersioningService {
	return &VersioningService{
		root:            filepath.Clean(root),
		index:           index,
		fallbackVersion: strings.TrimSpace(fallbackVersion),
	}
}

func (s *VersioningService) Resolve(
	file TwigFile,
	blockName string,
) (UpstreamResolution, error) {
	if s == nil || s.index == nil || blockName == "" {
		return UpstreamResolution{}, nil
	}
	chain, parentResolved, err := s.extendsChain(file)
	if err != nil {
		return UpstreamResolution{}, err
	}
	hashes, err := s.index.GetTwigBlockHashes(blockName)
	if err != nil {
		return UpstreamResolution{}, err
	}
	return s.resolveBlock(file, hashes, chain, parentResolved), nil
}

// ResolveBlocks resolves every declared block while reading the template's
// extends chain only once. Diagnostics use this path because a single override
// commonly declares many blocks from the same upstream templates.
func (s *VersioningService) ResolveBlocks(
	file TwigFile,
) (map[string]UpstreamResolution, error) {
	result := make(map[string]UpstreamResolution, len(file.Blocks))
	if s == nil || s.index == nil || len(file.Blocks) == 0 {
		return result, nil
	}
	chain, parentResolved, err := s.extendsChain(file)
	if err != nil {
		return nil, err
	}
	for blockName := range file.Blocks {
		hashes, hashErr := s.index.GetTwigBlockHashes(blockName)
		if hashErr != nil {
			return nil, hashErr
		}
		result[blockName] = s.resolveBlock(
			file, hashes, chain, parentResolved,
		)
	}
	return result, nil
}

func (s *VersioningService) resolveBlock(
	file TwigFile,
	hashes []TwigBlockHash,
	chain []string,
	parentResolved bool,
) UpstreamResolution {
	resolution := UpstreamResolution{
		ChainPaths:     append([]string(nil), chain...),
		ParentResolved: parentResolved,
	}
	for _, path := range chain {
		for _, hash := range hashes {
			if samePath(hash.AbsolutePath, path) {
				resolution.Candidates = append(resolution.Candidates, hash)
				break
			}
		}
	}
	if len(resolution.Candidates) != 0 {
		return resolution
	}

	currentViewPath := templateViewPath(file.Path)
	for _, hash := range hashes {
		if samePath(hash.AbsolutePath, file.Path) ||
			hash.HasVersioningComment ||
			templateViewPath(hash.RelativePath) != currentViewPath {
			continue
		}
		resolution.Candidates = append(resolution.Candidates, hash)
	}
	sort.SliceStable(resolution.Candidates, func(i, j int) bool {
		left, right := resolution.Candidates[i], resolution.Candidates[j]
		if IsStorefrontTemplate(left.AbsolutePath) != IsStorefrontTemplate(right.AbsolutePath) {
			return IsStorefrontTemplate(left.AbsolutePath)
		}
		if IsUpstreamTemplate(left.AbsolutePath) != IsUpstreamTemplate(right.AbsolutePath) {
			return IsUpstreamTemplate(left.AbsolutePath)
		}
		return filepath.ToSlash(left.AbsolutePath) < filepath.ToSlash(right.AbsolutePath)
	})
	return resolution
}

func (s *VersioningService) ResolveDocument(
	filePath,
	source,
	blockName string,
) (TwigFile, TwigBlock, UpstreamResolution, error) {
	file, err := ParseTwig(filePath, []byte(source))
	if err != nil {
		return TwigFile{}, TwigBlock{}, UpstreamResolution{}, err
	}
	block, found := file.Blocks[blockName]
	if !found {
		return *file, TwigBlock{}, UpstreamResolution{}, fmt.Errorf(
			"twig block %q is no longer available", blockName,
		)
	}
	resolution, err := s.Resolve(*file, blockName)
	return *file, block, resolution, err
}

func (s *VersioningService) VersionCommentEdit(
	filePath,
	source,
	blockName string,
) (cst.TextRange, string, error) {
	_, block, resolution, err := s.ResolveDocument(filePath, source, blockName)
	if err != nil {
		return cst.TextRange{}, "", err
	}
	if len(resolution.Candidates) == 0 {
		return cst.TextRange{}, "", fmt.Errorf(
			"upstream Twig block %q is unavailable", blockName,
		)
	}
	upstream := resolution.Candidates[0]
	comment := FormatVersionComment(
		upstream.Hash,
		s.VersionForPath(upstream.AbsolutePath),
	)
	if block.HasVersioningComment && block.VersionCommentRange != nil {
		return *block.VersionCommentRange, strings.TrimSuffix(comment, "\n"), nil
	}
	start := int(block.Range.Start)
	if start < 0 || start > len(source) {
		return cst.TextRange{}, "", errors.New("twig block range is outside the document")
	}
	lineStart := strings.LastIndex(source[:start], "\n") + 1
	indent := source[lineStart:start]
	if strings.Trim(indent, " \t") != "" {
		lineStart = start
		indent = ""
	}
	return cst.TextRange{
		Start: uint32(lineStart), End: uint32(lineStart),
	}, indent + comment, nil
}

func (s *VersioningService) OtherLocations(
	blockName,
	currentPath string,
) ([]TwigBlockHash, error) {
	if s == nil || s.index == nil {
		return nil, nil
	}
	hashes, err := s.index.GetTwigBlockHashes(blockName)
	if err != nil {
		return nil, err
	}
	result := make([]TwigBlockHash, 0, len(hashes))
	for _, hash := range hashes {
		if samePath(hash.AbsolutePath, currentPath) || hash.HasVersioningComment {
			continue
		}
		result = append(result, hash)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return filepath.ToSlash(result[i].AbsolutePath) < filepath.ToSlash(result[j].AbsolutePath)
	})
	return result, nil
}

func (s *VersioningService) BlockAtPath(
	blockName,
	path string,
) (TwigBlockHash, bool, error) {
	if s == nil || s.index == nil {
		return TwigBlockHash{}, false, nil
	}
	hashes, err := s.index.GetTwigBlockHashes(blockName)
	if err != nil {
		return TwigBlockHash{}, false, err
	}
	for _, hash := range hashes {
		if samePath(hash.AbsolutePath, path) {
			return hash, true, nil
		}
	}
	return TwigBlockHash{}, false, nil
}

func (s *VersioningService) VersionForPath(path string) string {
	if s == nil {
		return ""
	}
	path = filepath.Clean(path)
	if s.index != nil {
		s.index.dependenciesMu.RLock()
		phpIndex := s.index.phpIndex
		s.index.dependenciesMu.RUnlock()
		if phpIndex != nil && phpIndex.Project() != nil {
			var bestRoot string
			var version string
			for _, dependency := range phpIndex.Project().Dependencies {
				root := filepath.Clean(dependency.InstallPath)
				if pathInside(root, path) && len(root) > len(bestRoot) {
					bestRoot = root
					version = dependency.Version
				}
			}
			if version != "" {
				return version
			}
		}
	}
	searchRoot := packageSearchRoot(s.root, path)
	for directory := filepath.Dir(path); pathInside(searchRoot, directory); directory = filepath.Dir(directory) {
		content, err := os.ReadFile(filepath.Join(directory, "composer.json"))
		if err == nil {
			var composer struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(content, &composer) == nil && strings.TrimSpace(composer.Version) != "" {
				return strings.TrimSpace(composer.Version)
			}
			break
		}
		if samePath(directory, searchRoot) || filepath.Dir(directory) == directory {
			break
		}
	}
	if IsStorefrontTemplate(path) {
		return s.fallbackVersion
	}
	return ""
}

func (s *VersioningService) extendsChain(
	file TwigFile,
) ([]string, bool, error) {
	if file.ExtendsFile == "" || s == nil || s.index == nil {
		return nil, false, nil
	}
	visited := map[string]struct{}{filepath.Clean(file.Path): {}}
	current := file
	chain := make([]string, 0, maximumExtendsDepth)
	parentResolved := false
	for depth := 0; depth < maximumExtendsDepth && current.ExtendsFile != ""; depth++ {
		files, err := s.index.GetTwigFilesByRelPath(current.ExtendsFile)
		if err != nil {
			return nil, parentResolved, err
		}
		parent, found := chooseParentTemplate(files, current.ExtendsFile, current.Path, visited)
		if !found {
			break
		}
		if depth == 0 {
			parentResolved = true
		}
		clean := filepath.Clean(parent.Path)
		visited[clean] = struct{}{}
		chain = append(chain, parent.Path)
		current = parent
	}
	return chain, parentResolved, nil
}

func chooseParentTemplate(
	files []TwigFile,
	reference,
	currentPath string,
	visited map[string]struct{},
) (TwigFile, bool) {
	namespace := templateNamespace(reference)
	candidates := make([]TwigFile, 0, len(files))
	for _, file := range files {
		clean := filepath.Clean(file.Path)
		if samePath(clean, currentPath) {
			continue
		}
		if _, seen := visited[clean]; seen {
			continue
		}
		candidates = append(candidates, file)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		leftBundle := normalizedBundleName(left.BundleName) == normalizedBundleName(namespace)
		rightBundle := normalizedBundleName(right.BundleName) == normalizedBundleName(namespace)
		if leftBundle != rightBundle {
			return leftBundle
		}
		if strings.EqualFold(namespace, "Storefront") &&
			IsStorefrontTemplate(left.Path) != IsStorefrontTemplate(right.Path) {
			return IsStorefrontTemplate(left.Path)
		}
		if IsUpstreamTemplate(left.Path) != IsUpstreamTemplate(right.Path) {
			return IsUpstreamTemplate(left.Path)
		}
		return filepath.ToSlash(left.Path) < filepath.ToSlash(right.Path)
	})
	if len(candidates) == 0 {
		return TwigFile{}, false
	}
	return candidates[0], true
}

func templateNamespace(reference string) string {
	reference = strings.TrimPrefix(strings.TrimSpace(reference), "@")
	if index := strings.IndexByte(reference, '/'); index >= 0 {
		return reference[:index]
	}
	return ""
}

func normalizedBundleName(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("-", "", "_", "", "\\", "", "/", "").Replace(value)
}

func templateViewPath(value string) string {
	value = filepath.ToSlash(value)
	if marker := "/Resources/views/"; strings.Contains(value, marker) {
		return strings.TrimPrefix(value[strings.LastIndex(value, marker)+len(marker):], "/")
	}
	value = strings.TrimPrefix(value, "@")
	if index := strings.IndexByte(value, '/'); index >= 0 {
		value = value[index+1:]
	}
	return strings.TrimPrefix(value, "/")
}

func pathInside(root, candidate string) bool {
	root, candidate = filepath.Clean(root), filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func packageSearchRoot(workspaceRoot, path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	const marker = "/custom/plugins/"
	index := strings.Index(clean, marker)
	if index < 0 {
		return filepath.Clean(workspaceRoot)
	}
	remainder := clean[index+len(marker):]
	name := strings.SplitN(remainder, "/", 2)[0]
	if name == "" {
		return filepath.Clean(workspaceRoot)
	}
	return filepath.FromSlash(clean[:index+len(marker)] + name)
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
