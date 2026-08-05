package codeaction

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	generateSymfonyServiceCommand = "shopware/symfony/service/generate"
	collectSymfonyServicesCommand = "shopware/symfony/analytics/services/" +
		"generateDefinitions"
	createCompilerPassCommand    = "shopware/symfony/compilerPass/create"
	generateSymfonyServiceAction = "shopware.symfony.generateService"
	createCompilerPassAction     = "shopware.symfony.createCompilerPass"

	bundleInterfaceClass  = "Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface"
	containerBuilderClass = "Symfony\\Component\\DependencyInjection\\" +
		"ContainerBuilder"
)

var phpClassIdentifierPattern = regexp.MustCompile(
	`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`,
)

// SymfonyGeneratorProvider ports the reference plugin's interactive service
// and compiler-pass generators. Code actions hand off editor input to a client
// command; the corresponding server requests keep inference and source
// rewriting editor-independent.
type SymfonyGeneratorProvider struct {
	phpIndex     *php.PHPIndex
	serviceIndex *symfony.ServiceIndex
}

func NewSymfonyGeneratorProvider(
	phpIndex *php.PHPIndex,
	serviceIndex *symfony.ServiceIndex,
) *SymfonyGeneratorProvider {
	return &SymfonyGeneratorProvider{
		phpIndex:     phpIndex,
		serviceIndex: serviceIndex,
	}
}

func (p *SymfonyGeneratorProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *SymfonyGeneratorProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.CodeActionParams == nil ||
		request.Document == nil || request.Root == nil ||
		request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	class := phpquery.ClassAt(request.Node)
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return nil
	}
	className := phpClassFullyQualifiedName(request.Root, class)
	if className == "" {
		return nil
	}

	var actions []protocol.CodeAction
	if phpquery.MethodAt(request.Node) != nil {
		actions = append(actions, protocol.CodeAction{
			Title: "Generate Symfony service",
			Kind:  protocol.CodeActionRefactorRewrite,
			Command: &protocol.CommandAction{
				Title:     "Generate Symfony service",
				Command:   generateSymfonyServiceAction,
				Arguments: []any{request.TextDocument.URI, className},
			},
		})
	}
	path := request.Document.URI
	if resolved, err := uriutil.Path(request.Document.URI); err == nil {
		path = resolved
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	if snapshot.IsSubtypeOf(
		className,
		bundleInterfaceClass,
	) {
		actions = append(actions, protocol.CodeAction{
			Title: "Symfony: Create CompilerPass",
			Kind:  protocol.CodeActionRefactorRewrite,
			Command: &protocol.CommandAction{
				Title:     "Symfony: Create CompilerPass",
				Command:   createCompilerPassAction,
				Arguments: []any{request.TextDocument.URI, className},
			},
		})
	}
	return actions
}

func (p *SymfonyGeneratorProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		generateSymfonyServiceCommand: p.generateService,
		collectSymfonyServicesCommand: p.collectServiceDefinitions,
		createCompilerPassCommand:     p.createCompilerPass,
	}
}

type symfonyServiceGenerationRequest struct {
	ClassName string `json:"className"`
	Output    string `json:"output"`
	ClassAsID bool   `json:"classAsId"`
	ServiceID string `json:"serviceId"`
	FileURI   string `json:"fileUri"`
	Source    string `json:"source"`
	Version   int    `json:"version"`
}

type symfonyServiceGenerationResponse struct {
	Content  string `json:"content"`
	Language string `json:"language"`
}

type generatedServiceMethod struct {
	name      string
	arguments []string
}

type SymfonyServiceDefinitionCollectionRequest struct {
	ClassNames string `json:"classNames"`
	Output     string `json:"output"`
	ClassAsID  bool   `json:"classAsId"`
}

type SymfonyServiceParameterSuggestion struct {
	Method    string   `json:"method"`
	Parameter string   `json:"parameter"`
	Type      string   `json:"type,omitempty"`
	Services  []string `json:"services"`
}

type SymfonyServiceDefinitionCollectionEntry struct {
	ClassName   string                              `json:"className"`
	Content     string                              `json:"content,omitempty"`
	Language    string                              `json:"language,omitempty"`
	Suggestions []SymfonyServiceParameterSuggestion `json:"suggestions,omitempty"`
	Error       string                              `json:"error,omitempty"`
}

type SymfonyServiceDefinitionCollectionResponse struct {
	Definitions []SymfonyServiceDefinitionCollectionEntry `json:"definitions"`
}

func (p *SymfonyGeneratorProvider) generateService(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if p == nil || p.phpIndex == nil || p.serviceIndex == nil {
		return nil, fmt.Errorf("symfony service generator is unavailable")
	}
	var params symfonyServiceGenerationRequest
	if err := decodeSymfonyGeneratorRequest(raw, &params); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	className := strings.Trim(params.ClassName, "\\ ")
	snapshot := p.phpIndex.SemanticSnapshot()
	if params.Source != "" {
		parsed := phpparser.Parse(params.Source)
		if parsed.Tree == nil || parsed.Tree.Root == nil {
			return nil, fmt.Errorf("parse service source")
		}
		path := params.FileURI
		if resolved, resolveErr := uriutil.Path(params.FileURI); resolveErr == nil {
			path = resolved
		}
		document := p.phpIndex.AnalyzeDocument(
			path,
			params.Version,
			parsed.Tree.Root,
		)
		snapshot = snapshot.WithDocument(document)
	}
	classes := snapshot.Classes(className)
	if len(classes) == 0 || classes[0].Kind != semantic.ClassSymbol {
		return nil, fmt.Errorf("PHP class %q was not found", className)
	}
	output := strings.ToLower(strings.TrimSpace(params.Output))
	switch output {
	case "yaml", "xml", "fluent", "php-array":
	default:
		return nil, fmt.Errorf("unsupported service output format %q", params.Output)
	}
	serviceID := strings.TrimSpace(params.ServiceID)
	if params.ClassAsID {
		serviceID = className
	} else if serviceID == "" {
		serviceID = defaultSymfonyServiceID(className)
	}

	methods, err := p.generatedServiceMethods(ctx, snapshot, className)
	if err != nil {
		return nil, err
	}
	tags := symfony.SuggestedServiceTags(
		className,
		snapshot,
	)
	content := renderSymfonyServiceDefinition(
		output,
		className,
		serviceID,
		params.ClassAsID,
		methods,
		tags,
		p.phpIndex,
	)
	languageID := "php"
	if output == "yaml" || output == "xml" {
		languageID = output
	}
	return symfonyServiceGenerationResponse{
		Content:  content,
		Language: languageID,
	}, nil
}

func (p *SymfonyGeneratorProvider) collectServiceDefinitions(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request SymfonyServiceDefinitionCollectionRequest
	if err := decodeSymfonyGeneratorRequest(raw, &request); err != nil {
		return nil, err
	}
	return p.CollectServiceDefinitions(ctx, request)
}

func (p *SymfonyGeneratorProvider) CollectServiceDefinitions(
	ctx context.Context,
	request SymfonyServiceDefinitionCollectionRequest,
) (SymfonyServiceDefinitionCollectionResponse, error) {
	if p == nil || p.phpIndex == nil || p.serviceIndex == nil {
		return SymfonyServiceDefinitionCollectionResponse{},
			fmt.Errorf("symfony service generator is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return SymfonyServiceDefinitionCollectionResponse{}, err
	}
	output := strings.ToLower(strings.TrimSpace(request.Output))
	switch output {
	case "yaml", "xml", "fluent", "php-array":
	default:
		return SymfonyServiceDefinitionCollectionResponse{}, fmt.Errorf(
			"unsupported service output format %q",
			request.Output,
		)
	}
	classNames := uniqueSymfonyGeneratorClassNames(request.ClassNames)
	if len(classNames) == 0 {
		return SymfonyServiceDefinitionCollectionResponse{},
			fmt.Errorf("classNames is required")
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	response := SymfonyServiceDefinitionCollectionResponse{
		Definitions: make(
			[]SymfonyServiceDefinitionCollectionEntry,
			0,
			len(classNames),
		),
	}
	for _, className := range classNames {
		if err := ctx.Err(); err != nil {
			return SymfonyServiceDefinitionCollectionResponse{}, err
		}
		entry := SymfonyServiceDefinitionCollectionEntry{
			ClassName: className,
			Language:  generatedServiceLanguage(output),
		}
		classes := snapshot.Classes(className)
		if len(classes) == 0 ||
			classes[0].Kind != semantic.ClassSymbol {
			entry.Error = fmt.Sprintf(
				"PHP class %q was not found",
				className,
			)
			response.Definitions = append(response.Definitions, entry)
			continue
		}
		methods, err := p.generatedServiceMethods(
			ctx,
			snapshot,
			className,
		)
		if err != nil {
			return SymfonyServiceDefinitionCollectionResponse{}, err
		}
		entry.Suggestions, err = p.generatedServiceSuggestions(
			ctx,
			snapshot,
			className,
		)
		if err != nil {
			return SymfonyServiceDefinitionCollectionResponse{}, err
		}
		serviceID := defaultSymfonyServiceID(className)
		if request.ClassAsID {
			serviceID = className
		}
		entry.Content = renderSymfonyServiceDefinition(
			output,
			className,
			serviceID,
			request.ClassAsID,
			methods,
			symfony.SuggestedServiceTags(className, snapshot),
			p.phpIndex,
		)
		entry.Content = appendServiceSuggestionComments(
			entry.Content,
			output,
			entry.Suggestions,
		)
		response.Definitions = append(response.Definitions, entry)
	}
	return response, nil
}

func (p *SymfonyGeneratorProvider) generatedServiceMethods(
	ctx context.Context,
	snapshot *semantic.Snapshot,
	className string,
) ([]generatedServiceMethod, error) {
	var methods []generatedServiceMethod
	members := (phpresolver.MemberResolver{
		Snapshot: snapshot,
	}).All(types.Named(strings.Trim(className, "\\")))
	for _, member := range members {
		method := member.Symbol
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if method.Kind != semantic.MethodSymbol ||
			method.Visibility != semantic.Public ||
			method.Flags.Has(semantic.StaticFlag) ||
			len(method.Parameters) == 0 {
			continue
		}
		generated := generatedServiceMethod{
			name:      method.Name,
			arguments: make([]string, 0, len(method.Parameters)),
		}
		hasService := false
		for _, parameter := range method.Parameters {
			serviceID, err := p.serviceForType(snapshot, parameter.Type)
			if err != nil {
				return nil, err
			}
			if serviceID == "" {
				serviceID = "?"
			} else {
				hasService = true
			}
			generated.arguments = append(generated.arguments, serviceID)
		}
		if hasService {
			methods = append(methods, generated)
		}
	}
	sort.SliceStable(methods, func(left, right int) bool {
		leftConstructor := strings.EqualFold(methods[left].name, "__construct")
		rightConstructor := strings.EqualFold(methods[right].name, "__construct")
		if leftConstructor != rightConstructor {
			return leftConstructor
		}
		return strings.ToLower(methods[left].name) <
			strings.ToLower(methods[right].name)
	})
	return methods, nil
}

func (p *SymfonyGeneratorProvider) serviceForType(
	snapshot *semantic.Snapshot,
	value types.Type,
) (string, error) {
	services, err := p.servicesForType(snapshot, value)
	if err != nil || len(services) == 0 {
		return "", err
	}
	return services[0], nil
}

func (p *SymfonyGeneratorProvider) servicesForType(
	snapshot *semantic.Snapshot,
	value types.Type,
) ([]string, error) {
	var typeNames []string
	collectServiceTypeNames(value, &typeNames)
	var services []symfony.Service
	seen := make(map[string]struct{})
	for _, typeName := range typeNames {
		matches, err := p.serviceIndex.GetServicesByType(typeName)
		if err != nil {
			return nil, err
		}
		for _, service := range matches {
			key := strings.ToLower(service.ID)
			if _, exists := seen[key]; exists {
				continue
			}
			className := strings.Trim(service.Class, "\\ ")
			if className == "" && strings.Contains(service.ID, "\\") {
				className = strings.Trim(service.ID, "\\ ")
			}
			if className == "" ||
				!snapshot.Relations().IsAssignableTo(
					types.Named(className),
					value,
				) {
				continue
			}
			seen[key] = struct{}{}
			services = append(services, service)
		}
	}
	sort.SliceStable(services, func(left, right int) bool {
		leftClassID := strings.Contains(services[left].ID, "\\")
		rightClassID := strings.Contains(services[right].ID, "\\")
		if leftClassID != rightClassID {
			return leftClassID
		}
		return strings.ToLower(services[left].ID) <
			strings.ToLower(services[right].ID)
	})
	if len(services) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(services))
	for _, service := range services {
		result = append(result, service.ID)
	}
	return result, nil
}

func (p *SymfonyGeneratorProvider) generatedServiceSuggestions(
	ctx context.Context,
	snapshot *semantic.Snapshot,
	className string,
) ([]SymfonyServiceParameterSuggestion, error) {
	var result []SymfonyServiceParameterSuggestion
	members := (phpresolver.MemberResolver{
		Snapshot: snapshot,
	}).All(types.Named(strings.Trim(className, "\\")))
	for _, member := range members {
		method := member.Symbol
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if method.Kind != semantic.MethodSymbol ||
			method.Visibility != semantic.Public ||
			method.Flags.Has(semantic.StaticFlag) {
			continue
		}
		for _, parameter := range method.Parameters {
			services, err := p.servicesForType(
				snapshot,
				parameter.Type,
			)
			if err != nil {
				return nil, err
			}
			if len(services) <= 1 {
				continue
			}
			if len(services) > 15 {
				services = services[:15]
			}
			result = append(
				result,
				SymfonyServiceParameterSuggestion{
					Method: method.Name,
					Parameter: strings.TrimPrefix(
						parameter.Name,
						"$",
					),
					Type:     parameter.Type.String(),
					Services: append([]string(nil), services...),
				},
			)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftConstructor := strings.EqualFold(
			result[left].Method,
			"__construct",
		)
		rightConstructor := strings.EqualFold(
			result[right].Method,
			"__construct",
		)
		if leftConstructor != rightConstructor {
			return leftConstructor
		}
		if !strings.EqualFold(
			result[left].Method,
			result[right].Method,
		) {
			return strings.ToLower(result[left].Method) <
				strings.ToLower(result[right].Method)
		}
		return strings.ToLower(result[left].Parameter) <
			strings.ToLower(result[right].Parameter)
	})
	return result, nil
}

func uniqueSymfonyGeneratorClassNames(value string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, candidate := range strings.Split(value, ",") {
		className := strings.Trim(candidate, "\\ \t\r\n")
		key := strings.ToLower(className)
		if className == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, className)
	}
	return result
}

func generatedServiceLanguage(output string) string {
	if output == "yaml" || output == "xml" {
		return output
	}
	return "php"
}

func appendServiceSuggestionComments(
	content,
	output string,
	suggestions []SymfonyServiceParameterSuggestion,
) string {
	if len(suggestions) == 0 {
		return content
	}
	prefix, suffix := "# ", ""
	switch output {
	case "xml":
		prefix, suffix = "<!-- ", " -->"
	case "fluent", "php-array":
		prefix = "// "
	}
	lines := []string{
		prefix + "Possible services per parameter:" + suffix,
	}
	for _, suggestion := range suggestions {
		services := make([]string, 0, len(suggestion.Services))
		for _, service := range suggestion.Services {
			if strings.Contains(service, ",") {
				service = `"` + service + `"`
			}
			services = append(services, service)
		}
		label := "$" + suggestion.Parameter
		if !strings.EqualFold(suggestion.Method, "__construct") {
			label += " (" + suggestion.Method + ")"
		}
		line := fmt.Sprintf(
			"%s [%s] => %s",
			label,
			firstNonEmptyGeneratorType(suggestion.Type),
			strings.Join(services, ", "),
		)
		lines = append(lines, prefix+line+suffix)
	}
	return content + "\n\n" + strings.Join(lines, "\n")
}

func firstNonEmptyGeneratorType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "mixed"
	}
	return value
}

func collectServiceTypeNames(value types.Type, result *[]string) {
	switch value.Kind() {
	case types.ObjectKind:
		if name := strings.Trim(value.Name(), "\\ "); name != "" {
			*result = append(*result, name)
		}
	case types.UnionKind:
		for _, member := range value.Arguments() {
			collectServiceTypeNames(member, result)
		}
	case types.IntersectionKind:
		for _, member := range value.Arguments() {
			collectServiceTypeNames(member, result)
		}
	}
}

func defaultSymfonyServiceID(className string) string {
	return strings.ToLower(strings.ReplaceAll(
		strings.Trim(className, "\\ "),
		"\\",
		"_",
	))
}

func renderSymfonyServiceDefinition(
	output,
	className,
	serviceID string,
	classAsID bool,
	methods []generatedServiceMethod,
	tags []string,
	phpIndex *php.PHPIndex,
) string {
	switch output {
	case "xml":
		return renderXMLServiceDefinition(
			className,
			serviceID,
			classAsID,
			methods,
			tags,
		)
	case "fluent":
		return renderFluentServiceDefinition(
			className,
			serviceID,
			classAsID,
			methods,
			tags,
			phpIndex,
		)
	case "php-array":
		return renderPHPArrayServiceDefinition(
			className,
			serviceID,
			classAsID,
			methods,
			tags,
			phpIndex,
		)
	default:
		return renderYAMLServiceDefinition(
			className,
			serviceID,
			classAsID,
			methods,
			tags,
		)
	}
}

func renderYAMLServiceDefinition(
	className,
	serviceID string,
	classAsID bool,
	methods []generatedServiceMethod,
	tags []string,
) string {
	var lines []string
	lines = append(lines, yamlServiceKey(serviceID)+":")
	if !classAsID {
		lines = append(lines, "    class: "+className)
	}
	for _, method := range methods {
		arguments := make([]string, 0, len(method.arguments))
		for _, argument := range method.arguments {
			arguments = append(arguments, "'@"+
				strings.ReplaceAll(argument, "'", "''")+"'")
		}
		if strings.EqualFold(method.name, "__construct") {
			lines = append(lines, "    arguments: ["+
				strings.Join(arguments, ", ")+"]")
		} else {
			if !slicesContain(lines, "    calls:") {
				lines = append(lines, "    calls:")
			}
			lines = append(lines, "        - ["+method.name+", ["+
				strings.Join(arguments, ", ")+"]]")
		}
	}
	if len(tags) != 0 {
		lines = append(lines, "    tags:")
		for _, tag := range tags {
			lines = append(lines, "        - { name: "+tag+" }")
		}
	}
	if len(lines) == 1 {
		lines[0] += " ~"
	}
	return strings.Join(lines, "\n")
}

func yamlServiceKey(value string) string {
	if value != "" {
		safe := true
		for _, char := range value {
			switch {
			case char >= 'a' && char <= 'z',
				char >= 'A' && char <= 'Z',
				char >= '0' && char <= '9',
				strings.ContainsRune(`_.\-`, char):
			default:
				safe = false
			}
			if !safe {
				break
			}
		}
		if safe {
			return value
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func slicesContain(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func renderXMLServiceDefinition(
	className,
	serviceID string,
	classAsID bool,
	methods []generatedServiceMethod,
	tags []string,
) string {
	var builder strings.Builder
	builder.WriteString(`<service id="`)
	builder.WriteString(html.EscapeString(serviceID))
	builder.WriteByte('"')
	if !classAsID {
		builder.WriteString(` class="`)
		builder.WriteString(html.EscapeString(className))
		builder.WriteByte('"')
	}
	if len(methods) == 0 && len(tags) == 0 {
		builder.WriteString("/>")
		return builder.String()
	}
	builder.WriteString(">\n")
	for _, method := range methods {
		if strings.EqualFold(method.name, "__construct") {
			for _, argument := range method.arguments {
				builder.WriteString(`  <argument type="service" id="`)
				builder.WriteString(html.EscapeString(argument))
				builder.WriteString("\"/>\n")
			}
			continue
		}
		builder.WriteString(`  <call method="`)
		builder.WriteString(html.EscapeString(method.name))
		builder.WriteString("\">\n")
		for _, argument := range method.arguments {
			builder.WriteString(`    <argument type="service" id="`)
			builder.WriteString(html.EscapeString(argument))
			builder.WriteString("\"/>\n")
		}
		builder.WriteString("  </call>\n")
	}
	for _, tag := range tags {
		builder.WriteString(`  <tag name="`)
		builder.WriteString(html.EscapeString(tag))
		builder.WriteString("\"/>\n")
	}
	builder.WriteString("</service>")
	return builder.String()
}

func renderFluentServiceDefinition(
	className,
	serviceID string,
	classAsID bool,
	methods []generatedServiceMethod,
	tags []string,
	phpIndex *php.PHPIndex,
) string {
	id := "'" + escapePHPSingleQuoted(serviceID) + "'"
	if classAsID {
		id = `\` + className + "::class"
	}
	first := "$services->set(" + id
	if !classAsID {
		first += `, \` + className + "::class"
	}
	first += ")"
	lines := []string{first}
	for _, method := range methods {
		if strings.EqualFold(method.name, "__construct") {
			lines = append(lines, "    ->args([")
		} else {
			lines = append(lines, "    ->call('"+
				escapePHPSingleQuoted(method.name)+"', [")
		}
		for _, argument := range method.arguments {
			lines = append(lines, "        "+
				phpServiceArgument(argument, phpIndex)+",")
		}
		lines = append(lines, "    ])")
	}
	for _, tag := range tags {
		lines = append(lines, "    ->tag('"+
			escapePHPSingleQuoted(tag)+"')")
	}
	lines[len(lines)-1] += ";"
	return strings.Join(lines, "\n")
}

func renderPHPArrayServiceDefinition(
	className,
	serviceID string,
	classAsID bool,
	methods []generatedServiceMethod,
	tags []string,
	phpIndex *php.PHPIndex,
) string {
	id := "'" + escapePHPSingleQuoted(serviceID) + "'"
	if classAsID {
		id = `\` + className + "::class"
	}
	lines := []string{id + " => ["}
	if !classAsID {
		lines = append(lines, `    'class' => \`+className+"::class,")
	}
	for _, method := range methods {
		if strings.EqualFold(method.name, "__construct") {
			lines = append(lines, "    'arguments' => [")
			for _, argument := range method.arguments {
				lines = append(lines, "        "+
					phpServiceArgument(argument, phpIndex)+",")
			}
			lines = append(lines, "    ],")
			continue
		}
		if !slicesContain(lines, "    'calls' => [") {
			lines = append(lines, "    'calls' => [")
		}
		lines = append(lines, "        ['"+
			escapePHPSingleQuoted(method.name)+"', [")
		for _, argument := range method.arguments {
			lines = append(lines, "            "+
				phpServiceArgument(argument, phpIndex)+",")
		}
		lines = append(lines, "        ]],")
	}
	if slicesContain(lines, "    'calls' => [") {
		lines = append(lines, "    ],")
	}
	if len(tags) != 0 {
		lines = append(lines, "    'tags' => [")
		for _, tag := range tags {
			lines = append(lines, "        '"+
				escapePHPSingleQuoted(tag)+"',")
		}
		lines = append(lines, "    ],")
	}
	lines = append(lines, "],")
	return strings.Join(lines, "\n")
}

func phpServiceArgument(value string, phpIndex *php.PHPIndex) string {
	if phpIndex != nil && strings.Contains(value, "\\") {
		if _, found := phpIndex.FindClass(value); found {
			return `\` + strings.Trim(value, "\\") + "::class"
		}
	}
	return "service('" + escapePHPSingleQuoted(value) + "')"
}

type compilerPassCreationRequest struct {
	BundleURI   string `json:"bundleUri"`
	BundleClass string `json:"bundleClass"`
	ClassName   string `json:"className"`
	Source      string `json:"source"`
	Version     int    `json:"version"`
}

type compilerPassCreationResponse struct {
	FileURI       string `json:"fileUri"`
	FileContent   string `json:"fileContent"`
	BundleContent string `json:"bundleContent"`
}

func (p *SymfonyGeneratorProvider) createCompilerPass(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if p == nil || p.phpIndex == nil {
		return nil, fmt.Errorf("compiler-pass generator is unavailable")
	}
	var params compilerPassCreationRequest
	if err := decodeSymfonyGeneratorRequest(raw, &params); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params.BundleClass = strings.Trim(params.BundleClass, "\\ ")
	params.ClassName = strings.TrimSpace(params.ClassName)
	if !phpClassIdentifierPattern.MatchString(params.ClassName) {
		return nil, fmt.Errorf("invalid compiler-pass class name %q", params.ClassName)
	}
	parsedBundle := phpparser.Parse(params.Source)
	if parsedBundle.Tree == nil || parsedBundle.Tree.Root == nil {
		return nil, fmt.Errorf("parse bundle source")
	}
	bundlePath, err := uriutil.Path(params.BundleURI)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle URI: %w", err)
	}
	bundleDocument := p.phpIndex.AnalyzeDocument(
		bundlePath,
		params.Version,
		parsedBundle.Tree.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(bundleDocument)
	if !snapshot.IsSubtypeOf(
		params.BundleClass,
		bundleInterfaceClass,
	) {
		return nil, fmt.Errorf("%s is not a Symfony bundle", params.BundleClass)
	}
	relativeDirectory := filepath.Join("DependencyInjection", "Compiler")
	compilerDirectory := filepath.Join(
		filepath.Dir(bundlePath),
		relativeDirectory,
	)
	legacyDirectory := filepath.Join(
		filepath.Dir(bundlePath),
		"DependencyInjection",
		"CompilerPass",
	)
	compilerInfo, compilerErr := os.Stat(compilerDirectory)
	legacyInfo, legacyErr := os.Stat(legacyDirectory)
	if compilerErr != nil && !os.IsNotExist(compilerErr) {
		return nil, fmt.Errorf("inspect compiler directory: %w", compilerErr)
	}
	if legacyErr != nil && !os.IsNotExist(legacyErr) {
		return nil, fmt.Errorf("inspect legacy compiler directory: %w", legacyErr)
	}
	if (compilerErr != nil || !compilerInfo.IsDir()) &&
		legacyErr == nil && legacyInfo.IsDir() {
		relativeDirectory = filepath.Join(
			"DependencyInjection",
			"CompilerPass",
		)
	}
	targetPath := filepath.Join(
		filepath.Dir(bundlePath),
		relativeDirectory,
		params.ClassName+".php",
	)
	for _, candidate := range []string{
		targetPath,
		filepath.Join(
			filepath.Dir(bundlePath),
			"DependencyInjection",
			"Compiler",
			params.ClassName+".php",
		),
		filepath.Join(
			filepath.Dir(bundlePath),
			"DependencyInjection",
			"CompilerPass",
			params.ClassName+".php",
		),
	} {
		if _, statErr := os.Stat(candidate); statErr == nil {
			return nil, fmt.Errorf("compiler-pass file already exists: %s", candidate)
		} else if !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("inspect compiler-pass path: %w", statErr)
		}
	}
	bundleNamespace := namespaceOfPHPClass(params.BundleClass)
	compilerNamespace := bundleNamespace + `\` +
		strings.ReplaceAll(relativeDirectory, string(filepath.Separator), `\`)
	compilerClass := strings.Trim(
		compilerNamespace+`\`+params.ClassName,
		`\`,
	)
	updated, err := addCompilerPassToBundle(
		params.BundleURI,
		params.Source,
		params.Version,
		params.BundleClass,
		compilerClass,
		params.ClassName,
	)
	if err != nil {
		return nil, err
	}
	return compilerPassCreationResponse{
		FileURI:       uriutil.FileURI(targetPath),
		FileContent:   compilerPassTemplate(compilerNamespace, params.ClassName),
		BundleContent: updated,
	}, nil
}

func decodeSymfonyGeneratorRequest(
	raw *json.RawMessage,
	target interface{},
) error {
	if raw == nil {
		return fmt.Errorf("missing generator request")
	}
	if err := json.Unmarshal(*raw, target); err != nil {
		return fmt.Errorf("invalid generator request: %w", err)
	}
	return nil
}

func addCompilerPassToBundle(
	uri,
	source string,
	version int,
	bundleClass,
	compilerClass,
	compilerShortName string,
) (string, error) {
	parsed := phpparser.Parse(source)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return "", fmt.Errorf("parse bundle source")
	}
	root := parsed.Tree.Root
	var class *phpsyntax.Node
	for _, candidate := range phpquery.Classes(root) {
		if candidate.Kind() == phpsyntax.PhpClassDeclaration &&
			strings.EqualFold(
				phpClassFullyQualifiedName(root, candidate),
				bundleClass,
			) {
			class = candidate
			break
		}
	}
	if class == nil {
		return "", fmt.Errorf("bundle class %q was not found in the document", bundleClass)
	}
	document := lsp.NewTextDocument(uri, source, version)
	request := &lsp.CodeActionRequest{
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        language.PHP,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            root,
		},
	}
	methods := phpMethodsByName(class)
	build := methods["build"]

	compilerQualifier, compilerImport := phpClassQualifier(
		request,
		compilerClass,
	)
	if compilerQualifier == "" {
		return "", fmt.Errorf("resolve compiler-pass class import")
	}
	var imports []string
	if compilerImport != nil {
		imports = append(imports, compilerClass)
	}
	containerQualifier := "ContainerBuilder"
	if build == nil {
		imports = nil
		containerQualifier, containerImport := phpClassQualifier(
			request,
			containerBuilderClass,
		)
		if containerQualifier == "" {
			return "", fmt.Errorf("resolve ContainerBuilder import")
		}
		if strings.EqualFold(containerQualifier, compilerQualifier) &&
			!strings.EqualFold(containerBuilderClass, compilerClass) {
			compilerQualifier = `\` + compilerClass
			compilerImport = nil
			imports = imports[:0]
		}
		if containerImport != nil {
			imports = append(imports, containerBuilderClass)
		}
		if compilerImport != nil {
			imports = append(imports, compilerClass)
		}
	}

	var replacements []phpSourceReplacement
	if len(imports) != 0 {
		sort.Strings(imports)
		replacements = append(replacements, phpSourceReplacement{
			start: phpImportInsertionOffset(root),
			end:   phpImportInsertionOffset(root),
			text:  phpImportBlock(root, imports),
		})
	}
	if build == nil {
		body := phpquery.ClassBody(class)
		if body == nil {
			return "", fmt.Errorf("bundle class has no body")
		}
		_, close := phpBlockDelimiters(body)
		if close == nil {
			return "", fmt.Errorf("bundle class has no closing brace")
		}
		classIndent := phpLineIndentation(
			source,
			class.RangeTrimmedTrivia().Start,
		)
		memberIndent := classIndent + "    "
		for _, method := range phpquery.Methods(class) {
			memberIndent = phpLineIndentation(
				source,
				method.RangeTrimmedTrivia().Start,
			)
			break
		}
		statementIndent := memberIndent + phpMemberIndentUnit(
			source,
			class,
			memberIndent,
		)
		methodText := memberIndent + "public function build(" +
			containerQualifier + " $container): void\n" +
			memberIndent + "{\n" +
			statementIndent + "parent::build($container);\n" +
			statementIndent + "$container->addCompilerPass(new " +
			compilerQualifier + "());\n" +
			memberIndent + "}"
		replacements = append(replacements, replacementBeforePHPCloseBrace(
			source,
			close.Range().Start,
			methodText,
			classIndent,
			true,
		))
	} else {
		block := phpquery.DirectChild(build, phpsyntax.PhpBlock)
		_, close := phpBlockDelimiters(block)
		parameters := phpquery.Parameters(build)
		if block == nil || close == nil || len(parameters) == 0 {
			return "", fmt.Errorf("bundle build method has no writable body")
		}
		containerVariable := phpquery.ParameterName(parameters[0])
		if containerVariable == "" {
			return "", fmt.Errorf("bundle build method has no container parameter")
		}
		if strings.Contains(
			strings.ToLower(build.Text()),
			strings.ToLower("new "+compilerShortName),
		) || strings.Contains(
			strings.ToLower(build.Text()),
			strings.ToLower("new \\"+compilerClass),
		) {
			return "", fmt.Errorf(
				"compiler pass %s is already registered",
				compilerShortName,
			)
		}
		methodIndent := phpLineIndentation(
			source,
			build.RangeTrimmedTrivia().Start,
		)
		statementIndent := methodIndent + phpMemberIndentUnit(
			source,
			class,
			methodIndent,
		)
		statement := statementIndent + containerVariable +
			"->addCompilerPass(new " + compilerQualifier + "());"
		replacements = append(replacements, replacementBeforePHPCloseBrace(
			source,
			close.Range().Start,
			statement,
			methodIndent,
			false,
		))
	}
	updated, ok := applyPHPSourceReplacements(source, replacements)
	if !ok {
		return "", fmt.Errorf("apply compiler-pass bundle edits")
	}
	reparsed := phpparser.Parse(updated)
	if len(parsed.Errors) == 0 && len(reparsed.Errors) != 0 {
		return "", fmt.Errorf("generated bundle source is not valid PHP")
	}
	return updated, nil
}

func replacementBeforePHPCloseBrace(
	source string,
	closeOffset uint32,
	content,
	closeIndent string,
	blankLine bool,
) phpSourceReplacement {
	start := closeOffset
	for start > 0 && source[start-1] != '\n' && source[start-1] != '\r' {
		start--
	}
	whitespaceOnly := strings.TrimSpace(source[start:closeOffset]) == ""
	prefix := ""
	if blankLine {
		prefix = "\n"
	}
	if whitespaceOnly {
		return phpSourceReplacement{
			start: start,
			end:   closeOffset,
			text:  prefix + content + "\n" + closeIndent,
		}
	}
	return phpSourceReplacement{
		start: closeOffset,
		end:   closeOffset,
		text:  "\n" + content + "\n" + closeIndent,
	}
}

func namespaceOfPHPClass(className string) string {
	className = strings.Trim(className, "\\ ")
	if index := strings.LastIndex(className, `\`); index >= 0 {
		return className[:index]
	}
	return ""
}

func compilerPassTemplate(namespace, className string) string {
	return `<?php

declare(strict_types=1);

namespace ` + strings.Trim(namespace, `\`) + `;

use Symfony\Component\DependencyInjection\Compiler\CompilerPassInterface;
use Symfony\Component\DependencyInjection\ContainerBuilder;

class ` + className + ` implements CompilerPassInterface
{
    public function process(ContainerBuilder $container): void
    {
    }
}
`
}

var _ lsp.ActionProvider = (*SymfonyGeneratorProvider)(nil)
var _ lsp.CommandProvider = (*SymfonyGeneratorProvider)(nil)
