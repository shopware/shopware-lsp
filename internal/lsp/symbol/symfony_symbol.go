package symbol

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	maxCollectedSymbols  = 500
	maxServiceSymbols    = 150
	maxControllerSymbols = 150
)

type SymfonyWorkspaceSymbolProvider struct {
	services     *symfony.ServiceIndex
	routes       *symfony.RouteIndexer
	commands     *console.Index
	twig         *twig.TwigIndexer
	doctrine     *doctrine.Index
	components   *twigcomponent.Index
	translations *translation.Index
	php          *php.PHPIndex
}

func NewSymfonyWorkspaceSymbolProvider(
	services *symfony.ServiceIndex,
	routes *symfony.RouteIndexer,
	commands *console.Index,
	twigIndex *twig.TwigIndexer,
	doctrineIndex *doctrine.Index,
	components *twigcomponent.Index,
	translations *translation.Index,
	phpIndex *php.PHPIndex,
) *SymfonyWorkspaceSymbolProvider {
	return &SymfonyWorkspaceSymbolProvider{
		services:     services,
		routes:       routes,
		commands:     commands,
		twig:         twigIndex,
		doctrine:     doctrineIndex,
		components:   components,
		translations: translations,
		php:          phpIndex,
	}
}

type candidate struct {
	name          string
	container     string
	path          string
	rng           cst.TextRange
	line          int
	kind          protocol.SymbolKind
	score         int
	locationRange *protocol.Range
}

func (p *SymfonyWorkspaceSymbolProvider) WorkspaceSymbols(
	ctx context.Context,
	query string,
) ([]protocol.SymbolInformation, error) {
	if p == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	var candidates []candidate
	add := func(current candidate) {
		score, found := symbolMatchScore(
			query,
			current.name,
			current.container,
		)
		if !found || current.name == "" || current.path == "" {
			return
		}
		current.score = score
		candidates = append(candidates, current)
	}

	if err := p.collectServices(ctx, query, add); err != nil {
		return nil, err
	}
	if err := p.collectRoutes(ctx, query, add); err != nil {
		return nil, err
	}
	if err := p.collectControllers(ctx, query, add); err != nil {
		return nil, err
	}
	if err := p.collectCommands(ctx, add); err != nil {
		return nil, err
	}
	if err := p.collectTwig(ctx, query, add); err != nil {
		return nil, err
	}
	if err := p.collectDoctrine(ctx, add); err != nil {
		return nil, err
	}
	if err := p.collectComponents(ctx, add); err != nil {
		return nil, err
	}
	if err := p.collectTranslations(ctx, add); err != nil {
		return nil, err
	}

	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].score != candidates[right].score {
			return candidates[left].score < candidates[right].score
		}
		leftName := strings.ToLower(candidates[left].name)
		rightName := strings.ToLower(candidates[right].name)
		if leftName != rightName {
			return leftName < rightName
		}
		if candidates[left].container != candidates[right].container {
			return candidates[left].container <
				candidates[right].container
		}
		if candidates[left].path != candidates[right].path {
			return candidates[left].path < candidates[right].path
		}
		return candidates[left].rng.Start < candidates[right].rng.Start
	})
	if len(candidates) > maxCollectedSymbols {
		candidates = candidates[:maxCollectedSymbols]
	}

	result := make([]protocol.SymbolInformation, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	sources := make(map[string]*cst.LineIndex)
	for _, current := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		location, found := symbolLocation(current, sources)
		if !found {
			continue
		}
		key := strings.ToLower(current.name) + "\x00" +
			current.container + "\x00" + location.URI + "\x00" +
			current.rng.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, protocol.SymbolInformation{
			Name:          current.name,
			Kind:          current.kind,
			Location:      location,
			ContainerName: current.container,
		})
	}
	return result, nil
}

func (p *SymfonyWorkspaceSymbolProvider) collectControllers(
	ctx context.Context,
	query string,
	add func(candidate),
) error {
	if p.php == nil {
		return nil
	}
	snapshot := p.php.SemanticSnapshot()
	count := 0
	for _, class := range p.php.ClassSymbols() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if class.Kind != semantic.ClassSymbol ||
			!isSymfonyControllerClass(class) {
			continue
		}
		for _, member := range snapshot.MembersOf(class.ID) {
			if member.Kind != semantic.MethodSymbol ||
				member.Visibility != semantic.Public ||
				strings.HasPrefix(member.Name, "__") &&
					member.Name != "__invoke" {
				continue
			}
			name := strings.TrimPrefix(class.FullyQualified, "\\")
			if member.Name != "__invoke" {
				name += "::" + member.Name
			}
			container := "Symfony controller · " +
				strings.TrimPrefix(class.FullyQualified, "\\")
			if _, found := symbolMatchScore(
				query,
				name,
				container,
			); !found {
				continue
			}
			add(candidate{
				name:      name,
				container: container,
				path:      member.Path,
				rng:       member.SelectionRange,
				kind:      protocol.SymbolMethod,
			})
			count++
			if count >= maxControllerSymbols {
				return nil
			}
		}
	}
	if p.routes == nil {
		return nil
	}
	routes, err := p.routes.GetRoutes()
	if err != nil {
		return err
	}
	for _, route := range routes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reference, found := symfony.ParseControllerReference(route.Controller)
		if !found {
			continue
		}
		resolution, resolveErr := symfony.ResolveControllerReference(
			reference,
			p.services,
			p.php,
		)
		if resolveErr != nil {
			return resolveErr
		}
		if !resolution.MethodFound {
			continue
		}
		container := "Symfony controller · " +
			resolution.Method.FullyQualified
		if _, matches := symbolMatchScore(
			query,
			route.Controller,
			container,
		); !matches {
			continue
		}
		add(candidate{
			name:      route.Controller,
			container: container,
			path:      resolution.Method.Path,
			rng:       resolution.Method.SelectionRange,
			kind:      protocol.SymbolMethod,
		})
	}
	return nil
}

func isSymfonyControllerClass(class semantic.Symbol) bool {
	name := strings.TrimPrefix(class.FullyQualified, "\\")
	if !strings.HasSuffix(
		strings.ToLower(name),
		"controller",
	) || !strings.Contains(
		strings.ToLower(name),
		"\\controller\\",
	) {
		return false
	}
	path := strings.ToLower(filepath.ToSlash(class.Path))
	return !strings.Contains(path, "/test/") &&
		!strings.Contains(path, "/tests/")
}

func (p *SymfonyWorkspaceSymbolProvider) collectServices(
	ctx context.Context,
	query string,
	add func(candidate),
) error {
	if p.services == nil {
		return nil
	}
	names, err := p.services.GetAllServices()
	if err != nil {
		return err
	}
	count := 0
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, found := symbolMatchScore(query, name, "Symfony service"); !found {
			continue
		}
		service, found, serviceErr := p.services.GetServiceByID(name)
		if serviceErr != nil {
			return serviceErr
		}
		if !found || service.Path == "" {
			continue
		}
		container := "Symfony service"
		if service.Class != "" {
			container += " · " + service.Class
		}
		add(candidate{
			name:      name,
			container: container,
			path:      service.Path,
			line:      service.Line,
			kind:      protocol.SymbolObject,
		})
		count++
		if count >= maxServiceSymbols {
			break
		}
	}
	parameters, err := p.services.GetAllParameters()
	if err != nil {
		return err
	}
	for _, parameter := range parameters {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		add(candidate{
			name:      parameter.Name,
			container: "Symfony parameter",
			path:      parameter.Path,
			line:      parameter.Line,
			kind:      protocol.SymbolConstant,
		})
	}
	return nil
}

func (p *SymfonyWorkspaceSymbolProvider) collectRoutes(
	ctx context.Context,
	query string,
	add func(candidate),
) error {
	if p.routes == nil {
		return nil
	}
	routes, err := p.routes.GetRoutes()
	if err != nil {
		return err
	}
	pathSearch := strings.Contains(query, "/")
	pathMatcher := symfony.NewRoutePathSearchMatcher(query)
	for _, route := range routes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		container := "Symfony route · " + routeEndpointSymbolDetail(route)
		add(candidate{
			name:      route.Name,
			container: container,
			path:      route.FilePath,
			line:      route.Line,
			kind:      protocol.SymbolMethod,
		})
		if pathSearch && pathMatcher.Matches(route.Path) {
			add(candidate{
				name: query,
				container: "Symfony route URL · " +
					route.Path + " · " + route.Name,
				path: route.FilePath,
				line: route.Line,
				kind: protocol.SymbolMethod,
			})
		}
	}
	return nil
}

func routeEndpointSymbolDetail(route symfony.Route) string {
	var methods []string
	seen := make(map[string]struct{}, len(route.Methods))
	for _, method := range route.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			continue
		}
		if _, duplicate := seen[method]; duplicate {
			continue
		}
		seen[method] = struct{}{}
		methods = append(methods, method)
	}
	methodLabel := "ANY"
	if len(methods) != 0 {
		methodLabel = strings.Join(methods, "|")
	}
	detail := methodLabel
	if route.Path != "" {
		detail += " " + route.Path
	}
	if controller := shortRouteController(route.Controller); controller != "" {
		detail += " · " + controller
	}
	return detail
}

func shortRouteController(controller string) string {
	controller = strings.TrimSpace(controller)
	if controller == "" {
		return ""
	}
	if parts := strings.SplitN(controller, "::", 2); len(parts) == 2 {
		return shortRouteControllerClass(parts[0]) + ":" + parts[1]
	}
	if offset := strings.LastIndex(controller, ":"); offset > 0 &&
		offset < len(controller)-1 {
		return shortRouteControllerClass(controller[:offset]) +
			":" + controller[offset+1:]
	}
	return shortRouteControllerClass(controller)
}

func shortRouteControllerClass(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\\"))
	if offset := strings.LastIndex(value, "\\"); offset >= 0 &&
		offset < len(value)-1 {
		return value[offset+1:]
	}
	return value
}

func (p *SymfonyWorkspaceSymbolProvider) collectCommands(
	ctx context.Context,
	add func(candidate),
) error {
	if p.commands == nil {
		return nil
	}
	commands, err := p.commands.GetCommands()
	if err != nil {
		return err
	}
	for _, command := range commands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		container := "Symfony command"
		if command.Description != "" {
			container += " · " + command.Description
		}
		add(candidate{
			name:      command.Name,
			container: container,
			path:      command.File,
			rng:       command.Range,
			kind:      protocol.SymbolFunction,
		})
	}
	return nil
}

func (p *SymfonyWorkspaceSymbolProvider) collectTwig(
	ctx context.Context,
	query string,
	add func(candidate),
) error {
	if p.twig == nil {
		return nil
	}
	templates, err := p.twig.GetAllTemplateFiles()
	if err != nil {
		return err
	}
	for _, name := range templates {
		if _, found := symbolMatchScore(
			query,
			name,
			"Twig template",
		); !found {
			continue
		}
		files, fileErr := p.twig.GetTwigFilesByRelPath(name)
		if fileErr != nil {
			return fileErr
		}
		for _, file := range files {
			add(candidate{
				name:      name,
				container: "Twig template",
				path:      file.Path,
				kind:      protocol.SymbolFile,
			})
		}
	}
	blocks, err := p.twig.GetAllTemplateBlocks()
	if err != nil {
		return err
	}
	for _, block := range blocks {
		add(candidate{
			name:      block.Name,
			container: "Twig block",
			path:      block.FilePath,
			rng:       block.Range,
			line:      block.Line,
			kind:      protocol.SymbolField,
		})
	}
	macros, err := p.twig.GetAllMacros()
	if err != nil {
		return err
	}
	for _, macro := range macros {
		add(candidate{
			name:      macro.Name,
			container: "Twig macro · " + macro.Signature(),
			path:      macro.FilePath,
			rng:       macro.NameRange,
			kind:      protocol.SymbolFunction,
		})
	}
	functions, err := p.twig.GetAllTwigFunctions()
	if err != nil {
		return err
	}
	for _, function := range functions {
		add(candidate{
			name:      function.Name,
			container: "Twig function",
			path:      function.FilePath,
			line:      function.Line,
			kind:      protocol.SymbolFunction,
		})
	}
	filters, err := p.twig.GetAllTwigFilters()
	if err != nil {
		return err
	}
	for _, filter := range filters {
		add(candidate{
			name:      filter.Name,
			container: "Twig filter",
			path:      filter.FilePath,
			line:      filter.Line,
			kind:      protocol.SymbolFunction,
		})
	}
	return ctx.Err()
}

func (p *SymfonyWorkspaceSymbolProvider) collectDoctrine(
	ctx context.Context,
	add func(candidate),
) error {
	if p.doctrine == nil {
		return nil
	}
	models, err := p.doctrine.Models()
	if err != nil {
		return err
	}
	for _, model := range models {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		add(candidate{
			name:      shortClassName(model.Class),
			container: "Doctrine " + model.Kind.String() + " · " + model.Class,
			path:      model.File,
			rng:       model.NameRange,
			kind:      protocol.SymbolClass,
		})
		if model.Table != "" {
			add(candidate{
				name:      model.Table,
				container: "Doctrine table · " + model.Class,
				path:      model.File,
				rng:       model.NameRange,
				kind:      protocol.SymbolStruct,
			})
		}
	}
	return nil
}

func (p *SymfonyWorkspaceSymbolProvider) collectComponents(
	ctx context.Context,
	add func(candidate),
) error {
	if p.components == nil {
		return nil
	}
	components, err := p.components.Components()
	if err != nil {
		return err
	}
	for _, component := range components {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		container := "Twig component"
		if component.Live {
			container = "Live component"
		}
		if component.Class != "" {
			container += " · " + component.Class
		}
		add(candidate{
			name:      component.Name,
			container: container,
			path:      component.File,
			rng:       component.NameRange,
			kind:      protocol.SymbolClass,
		})
	}
	return nil
}

func (p *SymfonyWorkspaceSymbolProvider) collectTranslations(
	ctx context.Context,
	add func(candidate),
) error {
	if p.translations == nil {
		return nil
	}
	messages, err := p.translations.Messages()
	if err != nil {
		return err
	}
	domainFiles := make(map[string]struct{})
	for _, message := range messages {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rng := protocol.Range{
			Start: protocol.Position{
				Line:      message.Line,
				Character: message.Character,
			},
			End: protocol.Position{
				Line:      message.EndLine,
				Character: message.EndCharacter,
			},
		}
		container := "Translation · " + message.Domain
		if message.Locale != "" {
			container += " · " + message.Locale
		}
		domainKey := strings.ToLower(message.Domain) + "\x00" +
			message.Locale + "\x00" + filepath.Clean(message.File)
		if message.Domain != "" {
			if _, exists := domainFiles[domainKey]; !exists {
				domainFiles[domainKey] = struct{}{}
				domainContainer := "Translation domain"
				if message.Locale != "" {
					domainContainer += " · " + message.Locale
				}
				add(candidate{
					name:          message.Domain,
					container:     domainContainer,
					path:          message.File,
					kind:          protocol.SymbolNamespace,
					locationRange: &rng,
				})
			}
		}
		add(candidate{
			name:          message.Key,
			container:     container,
			path:          message.File,
			kind:          protocol.SymbolString,
			locationRange: &rng,
		})
	}
	return nil
}

func symbolMatchScore(query string, values ...string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 3, true
	}
	best := 4
	for _, value := range values {
		value = strings.ToLower(value)
		switch {
		case value == query:
			best = min(best, 0)
		case strings.HasPrefix(value, query):
			best = min(best, 1)
		case strings.Contains(value, query):
			best = min(best, 2)
		}
	}
	return best, best < 4
}

func symbolLocation(
	current candidate,
	sources map[string]*cst.LineIndex,
) (protocol.Location, bool) {
	if current.path == "" {
		return protocol.Location{}, false
	}
	location := protocol.Location{URI: uriutil.FileURI(current.path)}
	if current.locationRange != nil {
		location.Range = *current.locationRange
		return location, true
	}
	if current.rng.Len() != 0 {
		lineIndex := sources[current.path]
		if lineIndex == nil {
			source, err := os.ReadFile(current.path)
			if err != nil {
				return protocol.Location{}, false
			}
			lineIndex = cst.NewLineIndex(string(source))
			sources[current.path] = lineIndex
		}
		startLine, startCharacter := lineIndex.PositionUTF16(
			current.rng.Start,
		)
		endLine, endCharacter := lineIndex.PositionUTF16(current.rng.End)
		location.Range = protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		}
		return location, true
	}
	if current.line > 0 {
		location.Range.Start.Line = current.line - 1
		location.Range.End.Line = current.line - 1
	}
	return location, true
}

func shortClassName(class string) string {
	class = strings.Trim(class, `\`)
	if index := strings.LastIndex(class, `\`); index >= 0 {
		return class[index+1:]
	}
	return filepath.Base(class)
}
