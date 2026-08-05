package scaffold

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
	"unicode"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const CreateSymfonyScaffoldCommand = "shopware/symfony/scaffold/create"
const CreateShopwareScaffoldCommand = "shopware/scaffold/create"

var (
	scaffoldFileNamePattern = regexp.MustCompile(
		`^[A-Za-z0-9_.-]+$`,
	)
)

type Provider struct {
	root     string
	phpIndex *php.PHPIndex
	console  *console.Index
}

func NewProvider(
	root string,
	phpIndex *php.PHPIndex,
	consoleIndex *console.Index,
) *Provider {
	return &Provider{
		root:     filepath.Clean(root),
		phpIndex: phpIndex,
		console:  consoleIndex,
	}
}

func (p *Provider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		CreateSymfonyScaffoldCommand:  p.create,
		CreateShopwareScaffoldCommand: p.createShopware,
	}
}

type Request struct {
	Kind         string `json:"kind"`
	DirectoryURI string `json:"directoryUri"`
	Name         string `json:"name"`
}

type Response struct {
	FileURI   string `json:"fileUri"`
	Content   string `json:"content"`
	Language  string `json:"language"`
	ClassName string `json:"className,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

func (p *Provider) create(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if p == nil || p.phpIndex == nil {
		return nil, fmt.Errorf("symfony scaffold generator is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var params Request
	if raw == nil {
		return nil, fmt.Errorf("missing scaffold request")
	}
	if err := json.Unmarshal(*raw, &params); err != nil {
		return nil, fmt.Errorf("invalid scaffold request: %w", err)
	}
	directory, err := uriutil.Path(params.DirectoryURI)
	if err != nil {
		return nil, fmt.Errorf("resolve scaffold directory: %w", err)
	}
	directory, err = p.validatedDirectory(directory)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(params.Kind))
	if !isPHPScaffold(kind) && !isServiceScaffold(kind) {
		return nil, fmt.Errorf(
			"unsupported Symfony scaffold kind %q",
			kind,
		)
	}
	if isServiceScaffold(kind) {
		return p.serviceScaffold(directory, kind, params.Name)
	}
	className := applyScaffoldSuffix(
		strings.TrimSpace(params.Name),
		scaffoldClassSuffix(kind),
	)
	if !validPHPClassName(className) {
		return nil, fmt.Errorf("invalid PHP class name %q", params.Name)
	}
	namespace, err := namespaceForDirectory(
		p.phpIndex.Project(),
		directory,
	)
	if err != nil {
		return nil, err
	}
	content, err := p.phpScaffold(kind, namespace, className)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(directory, className+".php")
	if err := ensureScaffoldTargetAvailable(target); err != nil {
		return nil, err
	}
	parsed := phpparser.Parse(content)
	if len(parsed.Errors) != 0 {
		return nil, fmt.Errorf("generated PHP scaffold is invalid")
	}
	return Response{
		FileURI:   uriutil.FileURI(target),
		Content:   content,
		Language:  "php",
		ClassName: className,
		Namespace: namespace,
	}, nil
}

func (p *Provider) validatedDirectory(path string) (string, error) {
	root, err := filepath.Abs(p.root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	directory, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve scaffold directory: %w", err)
	}
	canonicalRoot := resolvedDirectoryPath(root)
	canonicalDirectory := resolvedDirectoryPath(directory)
	relative, err := filepath.Rel(canonicalRoot, canonicalDirectory)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"scaffold directory %q is outside the workspace",
			path,
		)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("inspect scaffold directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("scaffold target is not a directory")
	}
	return directory, nil
}

func resolvedDirectoryPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func namespaceForDirectory(
	model *project.Model,
	directory string,
) (string, error) {
	if model == nil {
		return "", fmt.Errorf("no Composer project model is available")
	}
	type candidate struct {
		namespace string
		root      string
	}
	var candidates []candidate
	canonicalDirectory := resolvedDirectoryPath(directory)
	mappings, err := model.PSR4MappingsForDirectory(directory)
	if err != nil {
		return "", err
	}
	for _, mapping := range mappings {
		root := resolvedDirectoryPath(filepath.Clean(mapping.Root))
		relative, err := filepath.Rel(root, canonicalDirectory)
		if err != nil || relative == ".." ||
			strings.HasPrefix(
				relative,
				".."+string(filepath.Separator),
			) {
			continue
		}
		candidates = append(candidates, candidate{
			namespace: strings.Trim(mapping.Namespace, `\`),
			root:      root,
		})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"no Composer PSR-4 namespace maps to %s",
			directory,
		)
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if len(candidates[left].root) != len(candidates[right].root) {
			return len(candidates[left].root) >
				len(candidates[right].root)
		}
		return candidates[left].namespace < candidates[right].namespace
	})
	selected := candidates[0]
	relative, _ := filepath.Rel(selected.root, canonicalDirectory)
	namespace := selected.namespace
	if relative != "." && relative != "" {
		namespace += `\` + strings.ReplaceAll(
			filepath.ToSlash(relative),
			"/",
			`\`,
		)
	}
	return strings.Trim(namespace, `\`), nil
}

func (p *Provider) phpScaffold(
	kind,
	namespace,
	className string,
) (string, error) {
	switch kind {
	case "command":
		return p.commandTemplate(namespace, className), nil
	case "controller":
		return p.controllerTemplate(namespace, className), nil
	case "form":
		return formTemplate(namespace, className), nil
	case "twig-extension":
		return p.twigExtensionTemplate(namespace, className), nil
	case "compiler-pass":
		return compilerPassScaffoldTemplate(namespace, className), nil
	case "kernel-test":
		return kernelTestTemplate(namespace, className), nil
	case "web-test":
		return webTestTemplate(namespace, className), nil
	default:
		return "", fmt.Errorf("unsupported Symfony scaffold kind %q", kind)
	}
}

func (p *Provider) commandTemplate(
	namespace,
	className string,
) string {
	commandName := p.commandPrefix(namespace) + ":" +
		snakeCase(strings.TrimSuffix(className, "Command"))
	switch p.commandTemplateStyle(namespace) {
	case "invokable":
		return phpHeader(namespace) +
			`use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand(name: '` + commandName + `', description: 'Add a description')]
final class ` + className + `
{
    public function __invoke(SymfonyStyle $io): int
    {
        return Command::SUCCESS;
    }
}
`
	case "attribute":
		return phpHeader(namespace) +
			`use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand(name: '` + commandName + `', description: 'Add a description')]
final class ` + className + ` extends Command
{
    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        return Command::SUCCESS;
    }
}
`
	case "property":
		return phpHeader(namespace) +
			`use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

final class ` + className + ` extends Command
{
    protected static $defaultName = '` + commandName + `';
    protected static $defaultDescription = 'Add a description';

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        return Command::SUCCESS;
    }
}
`
	default:
		return phpHeader(namespace) +
			`use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

final class ` + className + ` extends Command
{
    protected function configure(): void
    {
        $this
            ->setName('` + commandName + `')
            ->setDescription('Add a description');
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        return Command::SUCCESS;
    }
}
`
	}
}

func (p *Provider) commandTemplateStyle(namespace string) string {
	if _, found := p.phpIndex.FindClass(
		"Symfony\\Component\\Console\\Command\\InvokableCommand",
	); found && !p.namespaceUsesExecuteCommands(namespace) {
		return "invokable"
	}
	if _, found := p.phpIndex.FindClass(
		"Symfony\\Component\\Console\\Attribute\\AsCommand",
	); found {
		return "attribute"
	}
	for _, property := range p.phpIndex.Properties(
		"Symfony\\Component\\Console\\Command\\Command",
	) {
		if strings.EqualFold(strings.TrimPrefix(property.Name, "$"), "defaultName") {
			return "property"
		}
	}
	return "configure"
}

func (p *Provider) namespaceUsesExecuteCommands(namespace string) bool {
	prefix := strings.ToLower(strings.Trim(namespace, `\`) + `\`)
	snapshot := p.phpIndex.SemanticSnapshot()
	for _, class := range p.phpIndex.ClassSymbols() {
		if !strings.HasPrefix(
			strings.ToLower(class.FullyQualified),
			prefix,
		) {
			continue
		}
		if !snapshot.IsSubtypeOf(
			class.FullyQualified,
			"Symfony\\Component\\Console\\Command\\Command",
		) {
			continue
		}
		for _, method := range p.phpIndex.Methods(class.FullyQualified) {
			if strings.EqualFold(method.Name, "execute") &&
				method.Container == class.ID {
				return true
			}
		}
	}
	return false
}

func (p *Provider) commandPrefix(namespace string) string {
	counts := make(map[string]int)
	if p.console != nil {
		commands, _ := p.console.GetCommands()
		namespacePrefix := strings.ToLower(
			strings.Trim(namespace, `\`) + `\`,
		)
		for _, command := range commands {
			if !strings.HasPrefix(
				strings.ToLower(strings.Trim(command.Class, `\`)),
				namespacePrefix,
			) {
				continue
			}
			if separator := strings.Index(command.Name, ":"); separator > 0 {
				counts[command.Name[:separator]]++
			}
		}
	}
	best, bestCount := "", 0
	for prefix, count := range counts {
		if count > bestCount ||
			count == bestCount && (best == "" || prefix < best) {
			best, bestCount = prefix, count
		}
	}
	if best != "" {
		return best
	}
	return "app"
}

func (p *Provider) controllerTemplate(
	namespace,
	className string,
) string {
	base := strings.TrimSuffix(className, "Controller")
	path := "/" + strings.ReplaceAll(snakeCase(base), "_", "-")
	template := snakeCase(base)
	routeNamespace := "Annotation"
	routeSyntax := `/**
     * @Route("` + path + `")
     */`
	if _, found := p.phpIndex.FindClass(
		"Symfony\\Component\\Routing\\Attribute\\Route",
	); found {
		routeNamespace = "Attribute"
		routeSyntax = "#[Route('" + path + "')]"
	}
	return phpHeader(namespace) +
		`use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\` + routeNamespace + `\Route;

final class ` + className + ` extends AbstractController
{
    ` + routeSyntax + `
    public function index(): Response
    {
        return $this->render('` + template + `/index.html.twig');
    }
}
`
}

func (p *Provider) twigExtensionTemplate(
	namespace,
	className string,
) string {
	if _, found := p.phpIndex.FindClass(
		"Twig\\Attribute\\AsTwigFunction",
	); found {
		return phpHeader(namespace) +
			`use Twig\Attribute\AsTwigFilter;
use Twig\Attribute\AsTwigFunction;

final class ` + className + `
{
    #[AsTwigFilter('my_filter')]
    public function myFilter(string $value): string
    {
        return $value;
    }

    #[AsTwigFunction('my_function')]
    public function myFunction(): string
    {
        return 'Hello from Twig';
    }
}
`
	}
	return phpHeader(namespace) +
		`use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;

final class ` + className + ` extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('my_function', $this->myFunction(...)),
        ];
    }

    public function getFilters(): array
    {
        return [
            new TwigFilter('my_filter', $this->myFilter(...)),
        ];
    }

    public function myFunction(): string
    {
        return 'Hello from Twig';
    }

    public function myFilter(string $value): string
    {
        return $value;
    }
}
`
}

func (p *Provider) serviceScaffold(
	directory,
	kind,
	name string,
) (Response, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "services"
	}
	if !scaffoldFileNamePattern.MatchString(name) {
		return Response{}, fmt.Errorf("invalid service configuration name %q", name)
	}
	extension := strings.TrimPrefix(kind, "services-")
	if extension == "yaml" {
		extension = "yaml"
	}
	if strings.EqualFold(filepath.Ext(name), "."+extension) {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	target := filepath.Join(directory, name+"."+extension)
	if err := ensureScaffoldTargetAvailable(target); err != nil {
		return Response{}, err
	}
	namespace, sourceRoot, err := primaryProjectPSR4(
		p.phpIndex.Project(),
		directory,
	)
	if err != nil {
		return Response{}, err
	}
	relativeSource := "../src/"
	if sourceRoot != "" {
		if relative, err := filepath.Rel(directory, sourceRoot); err == nil {
			relativeSource = filepath.ToSlash(relative)
			if relativeSource == "." {
				relativeSource = "./"
			} else {
				relativeSource = strings.TrimSuffix(relativeSource, "/") + "/"
			}
		}
	}
	var content string
	switch extension {
	case "yaml":
		content = yamlServicesTemplate(namespace, relativeSource)
		if len(yamlparser.Parse(content).Errors) != 0 {
			return Response{}, fmt.Errorf(
				"generated YAML service scaffold is invalid",
			)
		}
	case "xml":
		content = xmlServicesTemplate(namespace, relativeSource)
		if len(xmlparser.Parse(content).Errors) != 0 {
			return Response{}, fmt.Errorf(
				"generated XML service scaffold is invalid",
			)
		}
	case "php":
		content = phpServicesTemplate(namespace, relativeSource)
		if len(phpparser.Parse(content).Errors) != 0 {
			return Response{}, fmt.Errorf(
				"generated PHP service scaffold is invalid",
			)
		}
	default:
		return Response{}, fmt.Errorf(
			"unsupported service scaffold kind %q",
			kind,
		)
	}
	return Response{
		FileURI:  uriutil.FileURI(target),
		Content:  content,
		Language: extension,
	}, nil
}

func primaryProjectPSR4(
	model *project.Model,
	directory string,
) (string, string, error) {
	if model == nil {
		return "App\\", "", nil
	}
	type entry struct {
		namespace string
		path      string
	}
	var entries []entry
	mappings, err := model.PSR4MappingsForDirectory(directory)
	if err != nil {
		return "", "", err
	}
	for _, mapping := range mappings {
		namespace := strings.Trim(mapping.Namespace, `\`)
		if namespace != "" {
			namespace += `\`
		}
		entries = append(entries, entry{
			namespace: namespace,
			path:      filepath.Clean(mapping.Root),
		})
	}
	sort.SliceStable(entries, func(left, right int) bool {
		leftTest := strings.Contains(
			strings.ToLower(entries[left].path),
			"test",
		)
		rightTest := strings.Contains(
			strings.ToLower(entries[right].path),
			"test",
		)
		if leftTest != rightTest {
			return !leftTest
		}
		if len(entries[left].path) != len(entries[right].path) {
			return len(entries[left].path) < len(entries[right].path)
		}
		return entries[left].namespace < entries[right].namespace
	})
	if len(entries) == 0 {
		return "App\\", "", nil
	}
	return entries[0].namespace, entries[0].path, nil
}

func ensureScaffoldTargetAvailable(target string) error {
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("scaffold file already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect scaffold target: %w", err)
	}
	return nil
}

func isServiceScaffold(kind string) bool {
	switch kind {
	case "services-yaml", "services-xml", "services-php":
		return true
	default:
		return false
	}
}

func isPHPScaffold(kind string) bool {
	switch kind {
	case "command", "controller", "form", "twig-extension",
		"compiler-pass", "kernel-test", "web-test":
		return true
	default:
		return false
	}
}

func scaffoldClassSuffix(kind string) string {
	switch kind {
	case "command":
		return "Command"
	case "controller":
		return "Controller"
	case "kernel-test", "web-test":
		return "Test"
	default:
		return ""
	}
}

func applyScaffoldSuffix(name, suffix string) string {
	if suffix != "" && !strings.HasSuffix(
		strings.ToLower(name),
		strings.ToLower(suffix),
	) {
		return name + suffix
	}
	return name
}

func validPHPClassName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		current := name[index]
		if current >= 0x80 ||
			current == '_' ||
			current >= 'A' && current <= 'Z' ||
			current >= 'a' && current <= 'z' ||
			index > 0 && current >= '0' && current <= '9' {
			continue
		}
		return false
	}
	return true
}

func phpHeader(namespace string) string {
	header := `<?php

declare(strict_types=1);

`
	if namespace == "" {
		return header
	}
	return header + `namespace ` + namespace + `;

`
}

func formTemplate(namespace, className string) string {
	return phpHeader(namespace) +
		`use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\OptionsResolver\OptionsResolver;

final class ` + className + ` extends AbstractType
{
    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
    }

    public function configureOptions(OptionsResolver $resolver): void
    {
    }
}
`
}

func compilerPassScaffoldTemplate(namespace, className string) string {
	return phpHeader(namespace) +
		`use Symfony\Component\DependencyInjection\Compiler\CompilerPassInterface;
use Symfony\Component\DependencyInjection\ContainerBuilder;

final class ` + className + ` implements CompilerPassInterface
{
    public function process(ContainerBuilder $container): void
    {
    }
}
`
}

func kernelTestTemplate(namespace, className string) string {
	return phpHeader(namespace) +
		`use Symfony\Bundle\FrameworkBundle\Test\KernelTestCase;

final class ` + className + ` extends KernelTestCase
{
    public function testSomething(): void
    {
        self::bootKernel();

        $container = static::getContainer();

        self::assertNotNull($container);
    }
}
`
}

func webTestTemplate(namespace, className string) string {
	return phpHeader(namespace) +
		`use Symfony\Bundle\FrameworkBundle\Test\WebTestCase;

final class ` + className + ` extends WebTestCase
{
    public function testSomething(): void
    {
        $client = static::createClient();
        $client->request('GET', '/');

        self::assertResponseIsSuccessful();
    }
}
`
}

func yamlServicesTemplate(namespace, source string) string {
	namespaceKey := namespace
	if namespaceKey == "" {
		namespaceKey = "''"
	}
	return `services:
  _defaults:
    autowire: true
    autoconfigure: true

  ` + namespaceKey + `:
    resource: '` + strings.ReplaceAll(source, `'`, `''`) + `'
    exclude:
      - '` + strings.ReplaceAll(source, `'`, `''`) + `DependencyInjection/'
      - '` + strings.ReplaceAll(source, `'`, `''`) + `Entity/'
      - '` + strings.ReplaceAll(source, `'`, `''`) + `Kernel.php'
`
}

func xmlServicesTemplate(namespace, source string) string {
	return `<?xml version="1.0" encoding="UTF-8" ?>
<container xmlns="http://symfony.com/schema/dic/services"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xsi:schemaLocation="http://symfony.com/schema/dic/services
        https://symfony.com/schema/dic/services/services-1.0.xsd">
    <services>
        <defaults autowire="true" autoconfigure="true"/>
        <prototype namespace="` + html.EscapeString(namespace) +
		`" resource="` + html.EscapeString(source) +
		`" exclude="` + html.EscapeString(source) +
		`{DependencyInjection,Entity,Kernel.php}"/>
    </services>
</container>
`
}

func phpServicesTemplate(namespace, source string) string {
	escapedNamespace := strings.ReplaceAll(namespace, `\`, `\\`)
	escapedSource := strings.ReplaceAll(source, `'`, `\'`)
	return `<?php

declare(strict_types=1);

namespace Symfony\Component\DependencyInjection\Loader\Configurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services()
        ->defaults()
        ->autowire()
        ->autoconfigure();

    $services->load('` + escapedNamespace + `', '` + escapedSource + `')
        ->exclude('` + escapedSource + `{DependencyInjection,Entity,Kernel.php}');
};
`
}

func snakeCase(value string) string {
	characters := []rune(value)
	var result strings.Builder
	for index, current := range characters {
		if unicode.IsUpper(current) && index > 0 &&
			(unicode.IsLower(characters[index-1]) ||
				unicode.IsDigit(characters[index-1]) ||
				index+1 < len(characters) &&
					unicode.IsLower(characters[index+1])) {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToLower(current))
	}
	return strings.Trim(result.String(), "_")
}

var _ lsp.CommandProvider = (*Provider)(nil)
