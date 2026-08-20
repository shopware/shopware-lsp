package phpsemantic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/languagelevel"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestProviderMemberFeatures(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
class Service {
    /** Returns the display value. */
    public function value(string $prefix = ''): string { return $prefix . 'ok'; }
}
class Consumer {
    public function __construct(private Service $service) {}
    public function run(): string { return $this->service->value(); }
}`
	path := filepath.Join(t.TempDir(), "Member.php")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	provider := New(idx)

	callOffset := byteOffset(source, "value();")
	callContext := syntaxContext(document, callOffset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		1,
		callContext.Node,
		callContext.Root,
	)

	hoverRequest := &lsp.HoverRequest{
		HoverParams:   hoverParams(document.URI, document.LineIndex, callOffset),
		SyntaxContext: callContext,
	}
	hover, err := provider.GetHover(ctx, hoverRequest)
	require.NoError(t, err)
	require.Contains(t, hover.Contents.Value, "function value")
	require.Contains(t, hover.Contents.Value, "Returns the display value")

	definitionRequest := &lsp.DefinitionRequest{
		DefinitionParams: definitionParams(document.URI, document.LineIndex, callOffset),
		SyntaxContext:    callContext,
	}
	definitions := provider.GetDefinition(ctx, definitionRequest)
	require.Len(t, definitions, 1)
	require.Equal(t, document.URI, definitions[0].URI)
	require.Equal(t, 4, definitions[0].Range.Start.Line)

	referenceRequest := &lsp.ReferenceRequest{
		ReferenceParams: referenceParams(document.URI, document.LineIndex, callOffset, true),
		SyntaxContext:   callContext,
	}
	references, err := provider.GetReferences(ctx, referenceRequest)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(references), 2)

	completionRequest := &lsp.CompletionRequest{
		CompletionParams: completionParams(document.URI, document.LineIndex, callOffset),
		SyntaxContext:    callContext,
	}
	completions := provider.GetCompletions(ctx, completionRequest)
	require.Contains(t, completionLabels(completions), "value")

	signatureRequest := &lsp.SignatureHelpRequest{
		SignatureHelpParams: signatureParams(
			document.URI,
			document.LineIndex,
			callOffset+uint32(len("value(")),
		),
		SyntaxContext: callContext,
	}
	signature, err := provider.GetSignatureHelp(ctx, signatureRequest)
	require.NoError(t, err)
	require.NotNil(t, signature)
	require.Len(t, signature.Signatures, 1)
	require.Contains(t, signature.Signatures[0].Label, "$prefix = ...")

	renameRequest := &lsp.RenameRequest{
		RenameParams:  renameParams(document.URI, document.LineIndex, callOffset, "displayValue"),
		SyntaxContext: callContext,
	}
	edit, err := provider.Rename(ctx, renameRequest)
	require.NoError(t, err)
	require.Len(t, edit.Changes[document.URI], 2)
	for _, textEdit := range edit.Changes[document.URI] {
		require.Equal(t, "displayValue", textEdit.NewText)
	}
}

func TestProviderCompletesPhpStormExpectedArguments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	metaPath := filepath.Join(root, ".phpstorm.meta.php")
	metaSource := `<?php
namespace PHPSTORM_META;
registerArgumentsSet('modes', 'fast', \MODE_SAFE);
expectedArguments(\configure(), 0, argumentsSet('modes'));
`
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(
		t,
		idx.Index(indexer.NewParsedFile(metaPath, []byte(metaSource))),
	)

	source := `<?php configure('fa');`
	path := filepath.Join(root, "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "fa'") + len("fa"))
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		syntax.Node,
		syntax.Root,
	)
	items := New(idx).GetCompletions(
		ctx,
		&lsp.CompletionRequest{
			CompletionParams: completionParams(
				document.URI,
				document.LineIndex,
				offset,
			),
			SyntaxContext: syntax,
		},
	)
	require.ElementsMatch(t, []string{"fast", "\\MODE_SAFE"}, completionLabels(items))
	for _, item := range items {
		if item.Label != "fast" {
			continue
		}
		edit, ok := item.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "'fast'", edit.NewText)
		start := document.LineIndex.OffsetUTF16(
			uint32(edit.Range.Start.Line),
			uint32(edit.Range.Start.Character),
		)
		end := document.LineIndex.OffsetUTF16(
			uint32(edit.Range.End.Line),
			uint32(edit.Range.End.Character),
		)
		require.Equal(t, "'fa'", string(document.Text[start:end]))
		return
	}
	t.Fatal("fast completion not found")
}

func TestProviderCompletesFrameworkArgumentsFromMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	declarations := `<?php
namespace Symfony\Contracts\Service;
interface ServiceProviderInterface { public function get(string $id): object; }
namespace Shopware\Core\Framework\DependencyInjection;
final class Container implements \Symfony\Contracts\Service\ServiceProviderInterface {
    public function get(string $id): object {}
}
namespace Shopware\Core\Framework\Adapter;
final class Factory { public static function create(string $type): object {} }
`
	meta := `<?php
namespace PHPSTORM_META;
registerArgumentsSet('shopware.services', 'product.repository', 'event_dispatcher');
expectedArguments(
    \Symfony\Contracts\Service\ServiceProviderInterface::get(),
    0,
    argumentsSet('shopware.services'),
);
expectedArguments(
    \Shopware\Core\Framework\Adapter\Factory::create(),
    0,
    'rule',
    'flow',
);
`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, "Framework.php"),
		[]byte(declarations),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, ".phpstorm.meta.php"),
		[]byte(meta),
	)))

	for _, fixture := range []struct {
		name     string
		source   string
		needle   string
		expected []string
	}{
		{
			name: "Symfony service provider implemented by Shopware container",
			source: `<?php
use Shopware\Core\Framework\DependencyInjection\Container;
function run(Container $container): void { $container->get('prod'); }
`,
			needle:   "prod",
			expected: []string{"product.repository", "event_dispatcher"},
		},
		{
			name: "Shopware factory",
			source: `<?php
use Shopware\Core\Framework\Adapter\Factory;
Factory::create('ru');
`,
			needle:   "ru",
			expected: []string{"rule", "flow"},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(root, strings.ReplaceAll(fixture.name, " ", "_")+".php")
			document := lsp.NewTextDocument(uriutil.FileURI(path), fixture.source, 1)
			offset := uint32(strings.LastIndex(fixture.source, fixture.needle) + len(fixture.needle))
			syntax := syntaxContext(document, offset)
			ctx := idx.AddDocumentContext(
				context.Background(),
				path,
				document.Version,
				syntax.Node,
				syntax.Root,
			)
			items := New(idx).GetCompletions(ctx, &lsp.CompletionRequest{
				CompletionParams: completionParams(
					document.URI,
					document.LineIndex,
					offset,
				),
				SyntaxContext: syntax,
			})
			require.ElementsMatch(t, fixture.expected, completionLabels(items))
		})
	}
}

func TestProviderCompletesExpectedValuesAttribute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	declarationPath := filepath.Join(root, "Functions.php")
	declaration := `<?php
namespace App;
final class Modes {
    public const SAFE = 1;
    public const FAST = 2;
}
function configure(
    #[\JetBrains\PhpStorm\ExpectedValues(
        values: ['auto', 7],
        valuesFromClass: Modes::class,
    )]
    string|int $mode,
): void {}
`
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(
		t,
		idx.Index(indexer.NewParsedFile(declarationPath, []byte(declaration))),
	)

	usage := `<?php namespace App; configure('au');`
	path := filepath.Join(root, "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), usage, 1)
	offset := uint32(strings.Index(usage, "au'") + len("au"))
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		syntax.Node,
		syntax.Root,
	)
	items := New(idx).GetCompletions(
		ctx,
		&lsp.CompletionRequest{
			CompletionParams: completionParams(
				document.URI,
				document.LineIndex,
				offset,
			),
			SyntaxContext: syntax,
		},
	)
	require.ElementsMatch(
		t,
		[]string{"auto", "7", "\\App\\Modes::SAFE", "\\App\\Modes::FAST"},
		completionLabels(items),
	)
	for _, item := range items {
		if item.Label != "auto" {
			continue
		}
		edit, ok := item.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "'auto'", edit.NewText)
		return
	}
	t.Fatal("auto completion not found")
}

func TestProviderCompletesExpectedValuesFromGeneratedStubs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"require":{"php":"^8.3"}}`),
		0o600,
	))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.ConfigureProject(root))

	source := `<?php pathinfo('/tmp/file', PATH);`
	path := filepath.Join(root, "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "PATH)") + len("PATH"))
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		syntax.Node,
		syntax.Root,
	)
	items := New(idx).GetCompletions(
		ctx,
		&lsp.CompletionRequest{
			CompletionParams: completionParams(
				document.URI,
				document.LineIndex,
				offset,
			),
			SyntaxContext: syntax,
		},
	)
	labels := completionLabels(items)
	for _, expected := range []string{
		"PATHINFO_DIRNAME",
		"PATHINFO_BASENAME",
		"PATHINFO_EXTENSION",
		"PATHINFO_FILENAME",
	} {
		require.Contains(t, labels, expected)
	}
}

func TestProviderDiagnostics(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
class Consumer {
    public function run(): void {
        $value = new MissingType();
        echo $missing;
    }
}`
	path := filepath.Join(t.TempDir(), "Diagnostics.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	var messages []string
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	require.Contains(t, messages, "Undefined class MissingType")
	require.Contains(t, messages, "Undefined variable $missing")
}

func TestProviderReportsPHPDocFinalExtension(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	declarationPath := filepath.Join(root, "ExtensionPoint.php")
	declaration := `<?php
namespace Vendor;
/** @final */
class ExtensionPoint {}
/** @final */
final class ClosedExtensionPoint {}
`
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		declarationPath,
		[]byte(declaration),
	)))

	usage := `<?php
namespace App;
use Vendor\ExtensionPoint;
use Vendor\ClosedExtensionPoint;

/** @phpstan-ignore class.extendsFinal */
class Suppressed extends ExtensionPoint {}

class Invalid extends ExtensionPoint {}

class NativeInvalid extends ClosedExtensionPoint {}

function consume(ExtensionPoint $extension): void {}
`
	usagePath := filepath.Join(root, "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(usagePath), usage, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var inheritance []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.inheritance" {
			inheritance = append(inheritance, diagnostic)
		}
	}
	require.Len(t, inheritance, 2)
	var problem *lsp.Problem
	nativeErrors := 0
	for index := range inheritance {
		switch inheritance[index].Severity {
		case protocol.DiagnosticSeverityWarning:
			problem = &inheritance[index]
		case protocol.DiagnosticSeverityError:
			nativeErrors++
		}
	}
	require.NotNil(t, problem)
	require.Equal(t, 1, nativeErrors)
	require.Equal(t, protocol.DiagnosticSeverityWarning, problem.Severity)
	require.Equal(
		t,
		"Class Vendor\\ExtensionPoint is marked @final and should not be extended",
		problem.Message,
	)
	require.Equal(
		t,
		"ExtensionPoint",
		usage[problem.Range.Start:problem.Range.End],
	)
	require.Equal(t, 8, problemStartLine(document, *problem))
}

func TestProviderDiagnosesOnlyExplicitlyUnavailablePHPExtensions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"require":{"php":"^8.3"}}`),
		0o600,
	))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.ConfigureProjectWithExtensions(
		root,
		nil,
		[]string{"dom", "curl"},
	))

	source := `<?php
$document = new DOMDocument();
$handle = curl_init();
$optional = new Imagick();
`
	path := filepath.Join(root, "Runtime.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	var extensions []string
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.extension" {
			extensions = append(extensions, diagnostic.Message)
		}
		if diagnostic.ID == "php.undefined" {
			require.NotContains(t, diagnostic.Message, "Imagick")
		}
	}
	require.ElementsMatch(t, []string{
		"DOMDocument requires disabled PHP extension ext-dom",
		"curl_init requires disabled PHP extension ext-curl",
	}, extensions)
}

func TestProviderReportsStructuredDeprecatedAttribute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	declarationPath := filepath.Join(root, "Legacy.php")
	declaration := `<?php
namespace App;
#[\JetBrains\PhpStorm\Deprecated(
    reason: 'It loses precision',
    replacement: 'modern()',
    since: '2.0',
)]
function legacy(): int { return 1; }
`
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(
		t,
		idx.Index(indexer.NewParsedFile(declarationPath, []byte(declaration))),
	)

	usage := `<?php namespace App; $value = legacy();`
	usagePath := filepath.Join(root, "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(usagePath), usage, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	var deprecated *lsp.Problem
	for index := range diagnostics {
		if diagnostics[index].ID == "php.deprecated" {
			deprecated = &diagnostics[index]
			break
		}
	}
	require.NotNil(t, deprecated)
	require.Equal(
		t,
		"legacy is deprecated since 2.0: It loses precision; use modern()",
		deprecated.Message,
	)

	offset := byteOffset(usage, "legacy()")
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		usagePath,
		document.Version,
		syntax.Node,
		syntax.Root,
	)
	hover, err := New(idx).GetHover(ctx, &lsp.HoverRequest{
		HoverParams:   hoverParams(document.URI, document.LineIndex, offset),
		SyntaxContext: syntax,
	})
	require.NoError(t, err)
	require.NotNil(t, hover)
	require.Contains(t, hover.Contents.Value, "**Deprecated** since 2.0")
	require.Contains(t, hover.Contents.Value, "It loses precision")
	require.Contains(t, hover.Contents.Value, "Replacement: `modern()`")
}

func TestProviderAllowsSubclassLateStaticMembers(t *testing.T) {
	t.Parallel()
	source := `<?php
abstract class Extension {
    public static function name(): string { return static::NAME; }
}
final class ClosedExtension {
    public static function name(): string { return static::MISSING; }
}`
	path := filepath.Join(t.TempDir(), "LateStatic.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	var undefined []string
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.undefined" {
			undefined = append(undefined, diagnostic.Message)
		}
	}
	require.Equal(t, []string{
		"Undefined class constant MISSING on ClosedExtension",
	}, undefined)
}

func TestProviderHonorsMethodExistsGuards(t *testing.T) {
	t.Parallel()
	source := `<?php
class Entity {}
function guardedReturn(Entity $entity): void {
    if (!method_exists($entity, 'dynamic')) {
        return;
    }
    $entity->dynamic();
}
function guardedBranch(Entity $entity): void {
    if ($entity && method_exists($entity, 'dynamic')) {
        $entity->dynamic();
    }
}
function guardedTernary(Entity $entity): mixed {
    return method_exists($entity, 'dynamic') ? $entity->dynamic() : null;
}
/** @param list<Entity> $entities */
function guardedContinue(array $entities): void {
    foreach ($entities as $entity) {
        if (!\method_exists($entity, 'dynamic')) {
            continue;
        }
        $entity->dynamic();
    }
}
function unguarded(Entity $entity): void {
    $entity->missing();
}
function reassigned(Entity $entity): void {
    if (!method_exists($entity, 'dynamic')) {
        return;
    }
    $entity = new Entity();
    $entity->dynamic();
}`
	path := filepath.Join(t.TempDir(), "MethodExists.php")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	var undefined []string
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.undefined" {
			undefined = append(undefined, diagnostic.Message)
		}
	}
	require.ElementsMatch(t, []string{
		"Undefined method missing on Entity",
		"Undefined method dynamic on Entity",
	}, undefined)
}

func TestProviderHonorsClassExistenceGuards(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
use Optional\Package\Contract;
use Optional\Package\Runtime;
use Tideways\Profiler;
function guardedReturn(): void {
    if (!class_exists(Runtime::class)) {
        return;
    }
    Runtime::start();
}
function guardedInterface(): void {
    if (!interface_exists(Contract::class)) {
        return;
    }
    Contract::VERSION;
}
function guardedString(): void {
    if (!class_exists('Tideways\Profiler')) {
        return;
    }
    Profiler::createSpan('request');
}
function guardedBranch(): void {
    if (class_exists(Runtime::class)) {
        Runtime::start();
    }
}
function guardedTernary(): mixed {
    return class_exists(Runtime::class) ? Runtime::start() : null;
}
function unguarded(): void {
    Runtime::missing();
}`
	path := filepath.Join(t.TempDir(), "ClassExists.php")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	var undefined []string
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.undefined" {
			undefined = append(undefined, diagnostic.Message)
		}
	}
	require.Equal(t, []string{"Undefined class Runtime"}, undefined)
}

func TestProviderResolvesTraitMethodAliases(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
trait Values {
    public function value(string $input): int { return strlen($input); }
}
class Consumer {
    use Values {
        Values::value as private aliasedValue;
    }
    public function run(): int { return $this->aliasedValue('value'); }
}`
	path := filepath.Join(t.TempDir(), "TraitAlias.php")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	provider := New(idx)
	diagnostics, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.undefined", diagnostic.ID, diagnostic.Message)
		require.NotEqual(t, "php.arguments", diagnostic.ID, diagnostic.Message)
		require.NotEqual(t, "php.returnType", diagnostic.ID, diagnostic.Message)
	}

	offset := byteOffset(source, "aliasedValue('value')")
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		1,
		syntax.Node,
		syntax.Root,
	)
	definitions := provider.GetDefinition(ctx, &lsp.DefinitionRequest{
		DefinitionParams: definitionParams(
			document.URI,
			document.LineIndex,
			offset,
		),
		SyntaxContext: syntax,
	})
	require.Len(t, definitions, 1)
	require.Equal(t, 3, definitions[0].Range.Start.Line)
}

func TestProviderSuppressesDeprecationsInsideDeclaringClass(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
/** @deprecated */
class Legacy {
    /** @deprecated */
    public string $value = '';
    /** @deprecated */
    public function old(): void {}
    public function compatibilityImplementation(): void {
        $this->old();
        $this->value = self::class;
    }
}
/** @deprecated */
class LegacyBridge {
    public function forward(Legacy $legacy): void {
        $legacy->old();
    }
}
class Bridge {
    /** @deprecated */
    public function forward(Legacy $legacy): void {
        $legacy->old();
    }
}
class Consumer {
    public function run(Legacy $legacy): void {
        $legacy->old();
        $legacy->value = Legacy::class;
    }
}`
	path := filepath.Join(t.TempDir(), "Deprecations.php")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	consumerLine := strings.Count(
		source[:strings.Index(source, "class Consumer")],
		"\n",
	)
	deprecated := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.ID != "php.deprecated" {
			continue
		}
		deprecated++
		require.GreaterOrEqual(
			t,
			problemStartLine(document, diagnostic),
			consumerLine,
			"deprecations inside Legacy's own body must be suppressed",
		)
	}
	require.Positive(t, deprecated, "consumer usages must remain deprecated")
}

func TestProviderHonorsTargetedPHPStanDeprecationIgnores(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @deprecated */
function legacy(): void {}
function consume(): void {
    /** @phpstan-ignore function.deprecated */
    legacy();
    /** @phpstan-ignore arguments.count */
    legacy();
}`
	path := filepath.Join(t.TempDir(), "DeprecationIgnores.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var deprecated []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.deprecated" {
			deprecated = append(deprecated, diagnostic)
		}
	}
	require.Len(t, deprecated, 1)
	require.Equal(t, 7, problemStartLine(document, deprecated[0]))
}

func TestProviderScopesAnonymousClassMethods(t *testing.T) {
	t.Parallel()
	source := `<?php
function listen(): object {
    $prefix = 'event';
    return new class($prefix) {
        public function __construct(private string $prefix) {}
        public function handle(object $event): string {
            return $this->prefix . $event::class;
        }
    };
}`
	path := filepath.Join(t.TempDir(), "Anonymous.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.undefined", diagnostic.ID, diagnostic.Message)
	}
}

func TestProviderResolvesStaticPropertyNamesAsMembers(t *testing.T) {
	t.Parallel()
	source := `<?php
class Registry {
    /** @var list<string> */
    private static array $values = [];

    public static function add(string $value): void {
        static::$values[] = $value;
    }

    /** @return list<string> */
    public static function all(): array {
        return self::$values;
    }
}`
	path := filepath.Join(t.TempDir(), "StaticProperties.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.undefined", diagnostic.ID, diagnostic.Message)
		require.NotEqual(t, "php.returnType", diagnostic.ID, diagnostic.Message)
	}
}

func TestProviderScopesTopLevelForeachVariables(t *testing.T) {
	t.Parallel()
	source := `<?php
$matches = [['first.php']];
foreach (array_unique($matches[0]) as $match) {
    echo $match;
}`
	path := filepath.Join(t.TempDir(), "TopLevelForeach.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.undefined", diagnostic.ID, diagnostic.Message)
	}
}

func TestPHPDocClassAssistantContracts(t *testing.T) {
	cacheDir := t.TempDir()
	path := filepath.Join(cacheDir, "Assistant.php")
	source := `<?php
namespace App;
class TargetClass {}
interface TargetInterface {}
trait TargetTrait {}
enum TargetEnum {}

/** @param string $name #Class */
function takes_class(string $name): void {}

/** @param string $name #Interface */
function takes_interface(string $name): void {}

/** @param string $name #ClassInterface */
function takes_class_or_interface(string $name): void {}
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	idx, err := php.NewPHPIndex(filepath.Join(cacheDir, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	provider := New(idx)

	for _, fixture := range []struct {
		name       string
		function   string
		expected   []string
		prohibited []string
	}{
		{
			name:       "class",
			function:   "takes_class",
			expected:   []string{"App\\TargetClass"},
			prohibited: []string{"App\\TargetInterface", "App\\TargetTrait", "App\\TargetEnum"},
		},
		{
			name:       "interface",
			function:   "takes_interface",
			expected:   []string{"App\\TargetInterface"},
			prohibited: []string{"App\\TargetClass", "App\\TargetTrait", "App\\TargetEnum"},
		},
		{
			name:     "class or interface",
			function: "takes_class_or_interface",
			expected: []string{"App\\TargetClass", "App\\TargetInterface"},
			prohibited: []string{
				"App\\TargetTrait",
				"App\\TargetEnum",
			},
		},
	} {
		t.Run(fixture.name+" completion", func(t *testing.T) {
			partial := "App\\Tar"
			usage := "<?php namespace App; " + fixture.function +
				"('" + partial + "');"
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(cacheDir, "Usage.php")),
				usage,
				1,
			)
			offset := uint32(strings.Index(usage, partial) + len(partial))
			syntax := syntaxContext(document, offset)
			ctx := idx.AddDocumentContext(
				context.Background(),
				filepath.Join(cacheDir, "Usage.php"),
				document.Version,
				syntax.Node,
				syntax.Root,
			)
			items := provider.GetCompletions(
				ctx,
				&lsp.CompletionRequest{
					CompletionParams: completionParams(
						document.URI,
						document.LineIndex,
						offset,
					),
					SyntaxContext: syntax,
				},
			)
			labels := completionLabels(items)
			for _, expected := range fixture.expected {
				require.Contains(t, labels, expected)
			}
			for _, prohibited := range fixture.prohibited {
				require.NotContains(t, labels, prohibited)
			}
			for _, item := range items {
				if item.Label != fixture.expected[0] {
					continue
				}
				edit, ok := item.TextEdit.(protocol.TextEdit)
				require.True(t, ok)
				require.Equal(t, fixture.expected[0], edit.NewText)
				start := document.LineIndex.OffsetUTF16(
					uint32(edit.Range.Start.Line),
					uint32(edit.Range.Start.Character),
				)
				end := document.LineIndex.OffsetUTF16(
					uint32(edit.Range.End.Line),
					uint32(edit.Range.End.Character),
				)
				require.Equal(t, partial, string(document.Text[start:end]))
				break
			}
		})
	}

	for _, fixture := range []struct {
		name        string
		function    string
		target      string
		hasLocation bool
	}{
		{
			name:        "class",
			function:    "takes_class",
			target:      "App\\TargetClass",
			hasLocation: true,
		},
		{
			name:        "interface",
			function:    "takes_interface",
			target:      "App\\TargetInterface",
			hasLocation: true,
		},
		{
			name:        "combined interface",
			function:    "takes_class_or_interface",
			target:      "App\\TargetInterface",
			hasLocation: true,
		},
		{
			name:     "class excludes interface",
			function: "takes_class",
			target:   "App\\TargetInterface",
		},
	} {
		t.Run(fixture.name+" definition", func(t *testing.T) {
			usage := "<?php namespace App; " + fixture.function +
				"('" + fixture.target + "');"
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(cacheDir, "Usage.php")),
				usage,
				1,
			)
			offset := uint32(strings.Index(usage, fixture.target) + 3)
			syntax := syntaxContext(document, offset)
			ctx := idx.AddDocumentContext(
				context.Background(),
				filepath.Join(cacheDir, "Usage.php"),
				document.Version,
				syntax.Node,
				syntax.Root,
			)
			locations := provider.GetDefinition(
				ctx,
				&lsp.DefinitionRequest{
					DefinitionParams: definitionParams(
						document.URI,
						document.LineIndex,
						offset,
					),
					SyntaxContext: syntax,
				},
			)
			if !fixture.hasLocation {
				require.Empty(t, locations)
				return
			}
			require.Len(t, locations, 1)
			require.Equal(t, uriutil.FileURI(path), locations[0].URI)
		})
	}
}

func TestProviderMemberAndVisibilityDiagnostics(t *testing.T) {
	t.Parallel()
	source := `<?php
class Service {
    public private(set) string $value = '';
    private function secret(): void {}
}
class DynamicService {
    public function __call(string $name, array $arguments): mixed {}
}
class Consumer {
    public function run(Service $service, DynamicService $dynamic): void {
        $service->missing();
        $service->secret();
        $service->value = 'changed';
        echo $service->value;
        $dynamic->anything();
    }
}`
	path := filepath.Join(t.TempDir(), "Members.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var messages []string
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	require.Contains(t, messages, "Undefined method missing on Service")
	require.Contains(t, messages, "Cannot access private member secret")
	require.Contains(t, messages, "Cannot access private member value")
	for _, message := range messages {
		require.NotContains(t, message, "anything")
	}
}

func TestProviderAllowsProtectedTraitMemberFromSubclass(t *testing.T) {
	t.Parallel()
	source := `<?php
trait Helpers {
    protected function helper(): void {}
}
class Base {
    use Helpers;
}
class Child extends Base {
    public function run(): void {
        $this->helper();
    }
}
class Consumer {
    public function run(Child $child): void {
        $child->helper();
    }
}`
	path := filepath.Join(t.TempDir(), "TraitVisibility.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var visibilityDiagnostics []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.visibility" {
			visibilityDiagnostics = append(visibilityDiagnostics, diagnostic)
		}
	}
	require.Len(t, visibilityDiagnostics, 1)
	require.Equal(
		t,
		"Cannot access protected member helper",
		visibilityDiagnostics[0].Message,
	)
	require.Equal(t, 14, problemStartLine(document, visibilityDiagnostics[0]))
}

func TestProviderAllowsPrivateTraitMemberOnlyInUsingClass(t *testing.T) {
	t.Parallel()
	source := `<?php
trait PrivateHelpers {
    private function helper(): void {}
}
class UsesHelpers {
    use PrivateHelpers;

    public function run(): void {
        $this->helper();
    }
}
class Child extends UsesHelpers {
    public function runChild(): void {
        $this->helper();
    }
}`
	path := filepath.Join(t.TempDir(), "PrivateTraitVisibility.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var visibilityDiagnostics []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.visibility" {
			visibilityDiagnostics = append(visibilityDiagnostics, diagnostic)
		}
	}
	require.Len(t, visibilityDiagnostics, 1)
	require.Equal(
		t,
		"Cannot access private member helper",
		visibilityDiagnostics[0].Message,
	)
	require.Equal(t, 13, problemStartLine(document, visibilityDiagnostics[0]))
}

func TestProviderHonorsClosureBindVisibilityScope(t *testing.T) {
	t.Parallel()
	source := `<?php
class Secret {
    private string $value = 'secret';
}
function read(Secret $secret): string {
    $reader = Closure::bind(
        fn () => $secret->value,
        null,
        Secret::class,
    );
    $invalid = $secret->value;
    return $reader();
}`
	path := filepath.Join(t.TempDir(), "ClosureBindVisibility.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var visibilityDiagnostics []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == "php.visibility" {
			visibilityDiagnostics = append(visibilityDiagnostics, diagnostic)
		}
	}
	require.Len(t, visibilityDiagnostics, 1)
	require.Equal(
		t,
		"Cannot access private member value",
		visibilityDiagnostics[0].Message,
	)
	require.Equal(t, 10, problemStartLine(document, visibilityDiagnostics[0]))
}

func TestProviderAllowsInaccessiblePropertyWriteThroughMagicSetter(t *testing.T) {
	t.Parallel()
	source := `<?php
class Entity {
    protected ?string $createdAt = null;

    public function __set(string $name, mixed $value): void {
        $this->$name = $value;
    }
}
function hydrate(Entity $entity): void {
    $entity->createdAt = 'now';
}`
	path := filepath.Join(t.TempDir(), "MagicSetter.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.visibility", diagnostic.ID, diagnostic.Message)
	}
}

func TestProviderTreatsMissingSelfMembersAsTraitRequirements(t *testing.T) {
	t.Parallel()
	source := `<?php
class Service {}
trait ConsumerRequirements {
    public function run(Service $service): void {
        $this->providedMethod();
        echo $this->providedProperty;
        $service->missing();
    }
}`
	path := filepath.Join(t.TempDir(), "TraitRequirements.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	var messages []string
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	require.NotContains(
		t,
		messages,
		"Undefined method providedMethod on ConsumerRequirements",
	)
	require.NotContains(
		t,
		messages,
		"Undefined property $providedProperty on ConsumerRequirements",
	)
	require.Contains(t, messages, "Undefined method missing on Service")
}

func TestProviderDoesNotDiagnoseDynamicMemberNamesAsLiterals(t *testing.T) {
	t.Parallel()
	source := `<?php
class Config {
    public function setName(string $value): void {}
}
function assign(Config $config, string $setter): void {
    $config->$setter('value');
}`
	path := filepath.Join(t.TempDir(), "DynamicMember.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.undefined", diagnostic.ID, diagnostic.Message)
	}
}

func TestProviderAllowsRuntimeDynamicProperties(t *testing.T) {
	t.Parallel()
	source := `<?php
#[\AllowDynamicProperties]
class DynamicBase {}
class DynamicChild extends DynamicBase {}
function assign(\stdClass $plain, DynamicChild $custom): void {
    $plain->format = 'json';
    $custom->value = 1;
}`
	root := t.TempDir()
	path := filepath.Join(root, "DynamicProperties.php")
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.ConfigureProject(root))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)

	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.undefined", diagnostic.ID, diagnostic.Message)
	}
}

func TestProviderPHPVersionDiagnosticsUseComposerLanguageLevel(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		composer string
		expected []languagelevel.Feature
	}{
		{
			name:     "require supports syntax",
			composer: `{"require":{"php":"^8.4"}}`,
		},
		{
			name: "platform overrides require",
			composer: `{"require":{"php":"^8.4"},` +
				`"config":{"platform":{"php":"8.1"}}}`,
			expected: []languagelevel.Feature{
				languagelevel.ReadonlyClasses,
				languagelevel.TypedClassConstants,
			},
		},
	} {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "composer.json"),
				[]byte(fixture.composer),
				0o600,
			))
			idx, err := php.NewPHPIndex(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, idx.Close()) })
			require.NoError(t, idx.ConfigureProject(root))
			source := `<?php
readonly class Subject {
    public const string KIND = 'subject';
}`
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(root, "Subject.php")),
				source,
				1,
			)
			diagnostics, err := New(idx).Analyze(context.Background(), document)
			require.NoError(t, err)
			var actual []languagelevel.Feature
			for _, diagnostic := range diagnostics {
				if diagnostic.ID != "php.version" {
					continue
				}
				data, ok := diagnostic.Payload.(map[string]any)
				require.True(t, ok)
				feature, ok := data["feature"].(languagelevel.Feature)
				require.True(t, ok)
				actual = append(actual, feature)
				require.NotEmpty(t, data["minimumPHP"])
				require.NotEmpty(t, data["configuredPHP"])
			}
			require.ElementsMatch(t, fixture.expected, actual)
		})
	}
}

func TestProviderPHPVersionDiagnosticsHonorSpecificSuppressions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"require":{"php":"8.1"}}`),
		0o600,
	))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.ConfigureProject(root))
	source := `<?php
/** @noinspection PhpVersionInspection */
readonly class Subject {}
`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "Subject.php")),
		source,
		1,
	)
	diagnostics, err := New(idx).Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, diagnostic := range diagnostics {
		require.NotEqual(t, "php.version", diagnostic.ID, diagnostic.Message)
	}
}

func syntaxContext(document *lsp.TextDocument, offset uint32) lsp.SyntaxContext {
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	token := document.SyntaxTree.Root.TokenAtOffset(offset)
	return lsp.SyntaxContext{
		Document:        document,
		Language:        language.PHP,
		DocumentContent: document.Text,
		DocumentTree:    document.SyntaxTree,
		LineIndex:       document.LineIndex,
		Root:            document.SyntaxTree.Root,
		Token:           token,
		Node:            node,
	}
}

func byteOffset(source, needle string) uint32 {
	for index := 0; index+len(needle) <= len(source); index++ {
		if source[index:index+len(needle)] == needle {
			return uint32(index)
		}
	}
	return 0
}

func position(index *cst.LineIndex, offset uint32) (int, int) {
	line, character := index.PositionUTF16(offset)
	return int(line), int(character)
}

func problemStartLine(document *lsp.TextDocument, problem lsp.Problem) int {
	line, _ := document.LineIndex.PositionUTF16(problem.Range.Start)
	return int(line)
}

func hoverParams(uri string, index *cst.LineIndex, offset uint32) *protocol.HoverParams {
	params := &protocol.HoverParams{}
	params.TextDocument.URI = uri
	params.Position.Line, params.Position.Character = position(index, offset)
	return params
}

func definitionParams(
	uri string,
	index *cst.LineIndex,
	offset uint32,
) *protocol.DefinitionParams {
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = uri
	params.Position.Line, params.Position.Character = position(index, offset)
	return params
}

func referenceParams(
	uri string,
	index *cst.LineIndex,
	offset uint32,
	includeDeclaration bool,
) *protocol.ReferenceParams {
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = uri
	params.Position.Line, params.Position.Character = position(index, offset)
	params.Context.IncludeDeclaration = includeDeclaration
	return params
}

func completionParams(
	uri string,
	index *cst.LineIndex,
	offset uint32,
) *protocol.CompletionParams {
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = uri
	params.Position.Line, params.Position.Character = position(index, offset)
	return params
}

func signatureParams(
	uri string,
	index *cst.LineIndex,
	offset uint32,
) *protocol.SignatureHelpParams {
	params := &protocol.SignatureHelpParams{}
	params.TextDocument.URI = uri
	params.Position.Line, params.Position.Character = position(index, offset)
	return params
}

func renameParams(
	uri string,
	index *cst.LineIndex,
	offset uint32,
	newName string,
) *protocol.RenameParams {
	params := &protocol.RenameParams{NewName: newName}
	params.TextDocument.URI = uri
	params.Position.Line, params.Position.Character = position(index, offset)
	return params
}

func completionLabels(items []protocol.CompletionItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Label)
	}
	return result
}
