package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigExtensionAttributeCodeActionRecognizesRegistries(t *testing.T) {
	for _, fixture := range []struct {
		name         string
		registry     string
		registration string
		target       string
	}{
		{
			name:         "filter",
			registry:     "getFilters",
			registration: "TwigFilter",
			target:       "filterValue",
		},
		{
			name:         "function",
			registry:     "getFunctions",
			registration: "TwigFunction",
			target:       "functionValue",
		},
		{
			name:         "test",
			registry:     "getTests",
			registration: "TwigTest",
			target:       "testValue",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\` + fixture.registration + `;
class MyExtension extends AbstractExtension
{
    public function ` + fixture.registry + `(): array
    {
        return [
            new ` + fixture.registration + `('twig_name', [$this, '` +
				fixture.target + `']),
        ];
    }

    public function ` + fixture.target + `(mixed $value): mixed
    {
        return $value;
    }
}`
			actions := twigExtensionAttributeFixture(t).
				GetCodeActions(
					context.Background(),
					commandInvokeParameterRequest(
						source,
						"MyExtension",
					),
				)
			require.Len(t, actions, 1)
			assert.Equal(
				t,
				"Migrate to TwigExtension attributes",
				actions[0].Title,
			)
			assert.Equal(
				t,
				protocol.CodeActionRefactorRewrite,
				actions[0].Kind,
			)
		})
	}
}

func TestTwigExtensionAttributeCodeActionMigratesMixedRegistries(t *testing.T) {
	source := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\Environment;
use Twig\TwigFilter;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [
            new TwigFilter('filter_one', [$this, 'filterOne']),
            new TwigFilter('filter_two', [$this, 'filterTwo']),
        ];
    }

    public function getFunctions(): array
    {
        return [
            new TwigFunction('with_environment', $this->withEnvironment(...), [
                'needs_environment' => true,
                'needs_context' => true,
                'is_safe' => ['html'],
            ]),
        ];
    }

    public function filterOne(mixed $value): mixed { return $value; }
    public function filterTwo(mixed $value): mixed { return $value; }
    public function withEnvironment(
        Environment $environment,
        array $context,
        mixed $value,
    ): mixed {
        return $value;
    }
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.Contains(t, result, "use Twig\\Attribute\\AsTwigFilter;")
	assert.Contains(t, result, "use Twig\\Attribute\\AsTwigFunction;")
	assert.Contains(t, result, "#[AsTwigFilter('filter_one')]")
	assert.Contains(t, result, "#[AsTwigFilter('filter_two')]")
	assert.Contains(
		t,
		result,
		"#[AsTwigFunction('with_environment', needsEnvironment: true, "+
			"needsContext: true, isSafe: ['html'])]",
	)
	assert.NotContains(t, result, "function getFilters")
	assert.NotContains(t, result, "function getFunctions")
	assert.NotContains(t, result, "extends AbstractExtension")
	assert.NotContains(
		t,
		result,
		"use Twig\\Extension\\AbstractExtension;",
	)
	assert.Contains(t, result, "function filterOne")
	assert.Contains(t, result, "function withEnvironment")
}

func TestTwigExtensionAttributeCodeActionPreservesPartialRegistry(t *testing.T) {
	source := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('local', [$this, 'local']),
            new TwigFunction('runtime', [Runtime::class, 'runtime']),
        ];
    }

    public function local(mixed $value): mixed { return $value; }
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.Contains(t, result, "#[AsTwigFunction('local')]")
	assert.Contains(t, result, "function getFunctions")
	assert.Contains(t, result, "new TwigFunction('runtime'")
	assert.NotContains(t, result, "new TwigFunction('local'")
	assert.Contains(t, result, "extends AbstractExtension")
	assert.Contains(
		t,
		result,
		"use Twig\\Extension\\AbstractExtension;",
	)
}

func TestTwigExtensionAttributeCodeActionPreservesComplexOptions(t *testing.T) {
	source := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
use Twig\DeprecatedCallableInfo;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('safe', [$this, 'safe'], [
                'is_safe_callback' => [self::class, 'checkSafe'],
                'deprecation_info' => new DeprecatedCallableInfo('pkg', '1.0'),
            ]),
        ];
    }

    public function safe(mixed $value): mixed { return $value; }
    public static function checkSafe(): array { return []; }
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.Contains(
		t,
		result,
		"isSafeCallback: [self::class, 'checkSafe']",
	)
	assert.Contains(
		t,
		result,
		"deprecationInfo: new DeprecatedCallableInfo('pkg', '1.0')",
	)
}

func TestTwigExtensionAttributeCodeActionReusesAttributeAlias(t *testing.T) {
	source := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\Attribute\AsTwigFilter as FilterAttribute;
use Twig\TwigFilter;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [
            new TwigFilter('filter_name', [$this, 'filterValue']),
        ];
    }

    public function filterValue(mixed $value): mixed { return $value; }
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.Contains(t, result, "#[FilterAttribute('filter_name')]")
	assert.Equal(t, 1, strings.Count(
		result,
		"use Twig\\Attribute\\AsTwigFilter as FilterAttribute;",
	))
}

func TestTwigExtensionAttributeCodeActionRejectsUnsafeTargets(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "non extension",
			source: `<?php
use Twig\TwigFunction;
class MyExtension
{
    public function getFunctions(): array
    {
        return [new TwigFunction('local', [$this, 'local'])];
    }
    public function local(): void {}
}`,
		},
		{
			name: "external callable only",
			source: `<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [new TwigFunction('runtime', [Runtime::class, 'run'])];
    }
}`,
		},
		{
			name: "missing target method",
			source: `<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [new TwigFunction('missing', [$this, 'missing'])];
    }
}`,
		},
		{
			name: "dynamic registry",
			source: `<?php
use Twig\Extension\AbstractExtension;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return $this->buildFunctions();
    }
}`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			actions := twigExtensionAttributeFixture(t).
				GetCodeActions(
					context.Background(),
					commandInvokeParameterRequest(
						fixture.source,
						"MyExtension",
					),
				)
			assert.Empty(t, actions)
		})
	}
}

func TestTwigExtensionAttributeCodeActionKeepsExtendsForRemainingRegistry(
	t *testing.T,
) {
	source := `<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [new TwigFilter('local', [$this, 'local'])];
    }

    public function getTests(): array
    {
        return [];
    }

    public function local(mixed $value): mixed { return $value; }
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.NotContains(t, result, "function getFilters")
	assert.Contains(t, result, "function getTests")
	assert.Contains(t, result, "extends AbstractExtension")
}

func TestTwigExtensionAttributeCodeActionSupportsIndirectExtension(t *testing.T) {
	source := `<?php
namespace App\Twig;
use Twig\TwigFilter;
class MyExtension extends BaseExtension
{
    public function getFilters(): array
    {
        return [new TwigFilter('local', [$this, 'local'])];
    }

    public function local(mixed $value): mixed { return $value; }
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.Contains(t, result, "#[AsTwigFilter('local')]")
	assert.NotContains(t, result, "function getFilters")
	assert.Contains(t, result, "extends BaseExtension")
}

func TestTwigExtensionAttributeCodeActionRetainsSharedAbstractImport(t *testing.T) {
	source := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [new TwigFilter('local', [$this, 'local'])];
    }

    public function local(mixed $value): mixed { return $value; }
}

class OtherExtension extends AbstractExtension
{
}`
	result := applyTwigExtensionAttributeAction(t, source)

	assert.NotContains(t, result, "class MyExtension extends")
	assert.Contains(t, result, "class OtherExtension extends AbstractExtension")
	assert.Contains(
		t,
		result,
		"use Twig\\Extension\\AbstractExtension;",
	)
}

func twigExtensionAttributeFixture(
	t *testing.T,
) *TwigExtensionAttributeCodeActionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/vendor/TwigExtensionStubs.php",
		[]byte(`<?php
namespace Twig\Extension;
abstract class AbstractExtension {}
namespace App\Twig;
abstract class BaseExtension extends \Twig\Extension\AbstractExtension {}
`),
	)))
	return NewTwigExtensionAttributeCodeActionProvider(phpIndex)
}

func applyTwigExtensionAttributeAction(
	t *testing.T,
	source string,
) string {
	t.Helper()
	request := commandInvokeParameterRequest(source, "MyExtension")
	actions := twigExtensionAttributeFixture(t).GetCodeActions(
		context.Background(),
		request,
	)
	require.Len(t, actions, 1)
	edits := actions[0].Edit.Changes[request.TextDocument.URI]
	require.Len(t, edits, 1)
	rewritten := lsp.NewTextDocument(
		request.Document.URI,
		edits[0].NewText,
		request.Document.Version+1,
	)
	require.Empty(t, rewritten.ParseErrors)
	return edits[0].NewText
}
