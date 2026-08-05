package refactor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigTemplateRenameProvider struct {
	index *twig.TwigIndexer
}

func NewTwigTemplateRenameProvider(
	index *twig.TwigIndexer,
) *TwigTemplateRenameProvider {
	return &TwigTemplateRenameProvider{index: index}
}

func (p *TwigTemplateRenameProvider) WillRenameFiles(
	_ context.Context,
	request *lsp.FileRenameRequest,
) (*protocol.WorkspaceEdit, error) {
	if p == nil || p.index == nil || request == nil {
		return nil, nil
	}
	overlays := templateReferenceOverlays(request.Documents)
	sources := make(map[string]*templateEditSource)
	for path, overlay := range overlays {
		sources[path] = &templateEditSource{
			uri:       overlay.document.URI,
			lineIndex: overlay.document.LineIndex,
		}
	}
	type editKey struct {
		uri        string
		start, end protocol.Position
	}
	edits := make(map[editKey]protocol.TextEdit)

	for _, rename := range request.Files {
		oldPath, oldErr := uriutil.Path(rename.OldURI)
		newPath, newErr := uriutil.Path(rename.NewURI)
		if oldErr != nil || newErr != nil ||
			!strings.EqualFold(filepath.Ext(oldPath), ".twig") ||
			!strings.EqualFold(filepath.Ext(newPath), ".twig") ||
			filepath.Clean(oldPath) == filepath.Clean(newPath) {
			continue
		}
		oldNames, err := p.unambiguousTemplateNames(oldPath)
		if err != nil {
			return nil, err
		}
		if len(oldNames) == 0 {
			continue
		}
		newNames := twig.TemplateNames(newPath)
		if len(newNames) == 0 {
			continue
		}

		references, err := p.index.GetTemplateReferences(oldNames...)
		if err != nil {
			return nil, err
		}
		var effective []twig.TemplateReference
		for _, reference := range references {
			if _, open := overlays[filepath.Clean(reference.FilePath)]; open {
				continue
			}
			effective = append(effective, reference)
		}
		for _, overlay := range overlays {
			for _, reference := range overlay.references {
				if templateNameIn(reference.Template, oldNames) {
					effective = append(effective, reference)
				}
			}
		}

		for _, reference := range effective {
			if !templateNameIn(reference.Template, oldNames) {
				continue
			}
			newTemplate := replacementTemplateName(
				oldPath,
				newPath,
				reference.Template,
				newNames,
			)
			if newTemplate == "" || newTemplate == reference.Template {
				continue
			}
			source, ok := sources[filepath.Clean(reference.FilePath)]
			if !ok {
				source = loadTemplateEditSource(reference.FilePath)
				if source == nil {
					continue
				}
				sources[filepath.Clean(reference.FilePath)] = source
			}
			rng := protocolRange(reference.Range, source.lineIndex)
			key := editKey{uri: source.uri, start: rng.Start, end: rng.End}
			if existing, duplicate := edits[key]; duplicate &&
				existing.NewText != newTemplate {
				return nil, fmt.Errorf(
					"conflicting template rename edits for %s at %d:%d",
					source.uri,
					rng.Start.Line,
					rng.Start.Character,
				)
			}
			edits[key] = protocol.TextEdit{
				Range:   rng,
				NewText: newTemplate,
			}
		}
	}

	if len(edits) == 0 {
		return nil, nil
	}
	changes := make(map[string][]protocol.TextEdit)
	for key, edit := range edits {
		changes[key.uri] = append(changes[key.uri], edit)
	}
	for uri := range changes {
		sort.Slice(changes[uri], func(left, right int) bool {
			leftRange := changes[uri][left].Range
			rightRange := changes[uri][right].Range
			if leftRange.Start.Line != rightRange.Start.Line {
				return leftRange.Start.Line < rightRange.Start.Line
			}
			return leftRange.Start.Character < rightRange.Start.Character
		})
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}

func (p *TwigTemplateRenameProvider) unambiguousTemplateNames(
	oldPath string,
) ([]string, error) {
	cleanOldPath := filepath.Clean(oldPath)
	var result []string
	for _, name := range twig.TemplateNames(oldPath) {
		files, err := p.index.GetTwigFilesByRelPath(name)
		if err != nil {
			return nil, err
		}
		paths := make(map[string]struct{})
		for _, file := range files {
			paths[filepath.Clean(file.Path)] = struct{}{}
		}
		if len(paths) != 1 {
			continue
		}
		if _, target := paths[cleanOldPath]; target {
			result = append(result, name)
		}
	}
	return result, nil
}

type templateReferenceOverlay struct {
	document   *lsp.TextDocument
	references []twig.TemplateReference
}

func templateReferenceOverlays(
	documents []*lsp.TextDocument,
) map[string]templateReferenceOverlay {
	result := make(map[string]templateReferenceOverlay)
	for _, document := range documents {
		if document == nil || document.SyntaxTree == nil ||
			document.SyntaxTree.Root == nil {
			continue
		}
		path, err := uriutil.Path(document.URI)
		if err != nil {
			continue
		}
		var references []twig.TemplateReference
		switch strings.ToLower(filepath.Ext(path)) {
		case ".twig":
			references = twig.TwigTemplateReferences(
				path,
				document.SyntaxTree.Root,
			)
		case ".php":
			references = twig.PHPTemplateReferences(
				path,
				document.SyntaxTree.Root,
			)
		default:
			continue
		}
		result[filepath.Clean(path)] = templateReferenceOverlay{
			document:   document,
			references: references,
		}
	}
	return result
}

type templateEditSource struct {
	uri       string
	lineIndex *cst.LineIndex
}

func loadTemplateEditSource(path string) *templateEditSource {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return &templateEditSource{
		uri:       uriutil.FileURI(path),
		lineIndex: cst.NewLineIndex(string(source)),
	}
}

func replacementTemplateName(
	oldPath,
	newPath,
	oldName string,
	newNames []string,
) string {
	if filepath.Clean(filepath.Dir(oldPath)) ==
		filepath.Clean(filepath.Dir(newPath)) {
		return renameTemplateBasename(oldName, filepath.Base(newPath))
	}
	return pickBestTemplateName(newNames, oldName)
}

func renameTemplateBasename(oldName, newFileName string) string {
	slash := strings.LastIndex(oldName, "/")
	colon := strings.LastIndex(oldName, ":")
	separator := max(slash, colon)
	if separator < 0 {
		return newFileName
	}
	return oldName[:separator+1] + newFileName
}

func pickBestTemplateName(newNames []string, oldName string) string {
	if len(newNames) == 0 {
		return ""
	}
	oldPrefix := templateNamespacePrefix(oldName)
	for _, name := range newNames {
		if templateNamespacePrefix(name) == oldPrefix {
			return name
		}
	}
	return newNames[0]
}

func templateNamespacePrefix(name string) string {
	if strings.HasPrefix(name, "@") {
		if slash := strings.Index(name, "/"); slash > 0 {
			return name[:slash]
		}
		return name
	}
	if colon := strings.Index(name, ":"); colon >= 0 {
		return name[:colon]
	}
	return ""
}

func templateNameIn(template string, names []string) bool {
	template = normalizeTemplateName(template)
	for _, name := range names {
		if template == normalizeTemplateName(name) {
			return true
		}
	}
	return false
}

func normalizeTemplateName(name string) string {
	return strings.TrimPrefix(
		filepath.ToSlash(strings.TrimSpace(name)),
		"/",
	)
}

func protocolRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}
