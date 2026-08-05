package definition

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *TwigDefinitionProvider) twigSeeDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || request == nil || request.Root == nil ||
		request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := twig.SeeReferenceAt(request.Root, offset)
	if !found {
		return nil
	}
	target := reference.Target
	var locations []protocol.Location
	if p.twigIndexer != nil &&
		strings.HasSuffix(strings.ToLower(target), ".twig") {
		files, _ := p.twigIndexer.GetTwigFilesByRelPath(target)
		for _, file := range files {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(file.Path),
			})
		}
	}
	if p.phpIndex != nil {
		className, member, phpTarget :=
			twig.SeePHPClassAndMember(target)
		if phpTarget {
			if member != "" {
				for _, method := range p.phpIndex.FindMethods(
					className,
					member,
				) {
					locations = append(
						locations,
						phpSymbolLocation(method),
					)
				}
			} else if class, classFound :=
				p.phpIndex.FindClass(className); classFound {
				locations = append(
					locations,
					phpSymbolLocation(class),
				)
			}
		}
	}
	if relative, relativeFound := p.twigSeeRelativeFile(
		request.TextDocument.URI,
		target,
	); relativeFound {
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(relative),
		})
	}
	return uniqueComponentLocations(locations)
}

func (p *TwigDefinitionProvider) twigSeeRelativeFile(
	documentURI,
	target string,
) (string, bool) {
	if p.projectRoot == "" || target == "" ||
		strings.HasPrefix(target, "@") {
		return "", false
	}
	current, err := uriutil.Path(documentURI)
	if err != nil {
		return "", false
	}
	target = filepath.FromSlash(strings.ReplaceAll(target, `\`, "/"))
	if filepath.IsAbs(target) {
		return "", false
	}
	candidate := filepath.Clean(filepath.Join(filepath.Dir(current), target))
	relative, err := filepath.Rel(p.projectRoot, candidate)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return candidate, true
}
