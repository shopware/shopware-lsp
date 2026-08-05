package suppression

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPHPStanDirectivesMatchSpecificDiagnostics(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @phpstan-ignore return.type, argument.type (known limitation) */
consume();
/** @phpstan-ignore function.deprecated */ legacy();
/** @phpstan-ignore arguments.count */ legacy();
// @phpstan-ignore-next-line
unknown();
safe(); // @phpstan-ignore-line - legacy blanket form
`
	set := Parse(source)
	offset := func(value string) uint32 {
		return uint32(strings.Index(source, value))
	}
	require.True(t, set.Suppresses(offset("consume();"), "php.returnType"))
	require.True(t, set.Suppresses(offset("consume();"), "php.arguments"))
	require.False(t, set.Suppresses(offset("consume();"), "php.undefined"))
	require.True(t, set.Suppresses(offset("legacy();"), "php.deprecated"))
	require.False(t, set.Suppresses(offset("legacy();"), "php.arguments"))
	secondLegacy := strings.LastIndex(source, "legacy();")
	require.True(t, set.Suppresses(uint32(secondLegacy), "php.arguments"))
	require.False(t, set.Suppresses(uint32(secondLegacy), "php.deprecated"))
	require.True(t, set.Suppresses(offset("unknown();"), "php.undefined"))
	require.True(t, set.Suppresses(offset("safe();"), "php.returnType"))
}

func TestBlockAndNoInspectionDirectivesTargetNextCodeLine(t *testing.T) {
	t.Parallel()
	source := `<?php
/**
 * @phpstan-ignore return.type
 */

function value(): string { return 1; }
/** @noinspection PhpDeprecationInspection */
legacy();
/** @noinspection PhpParamsInspection */
legacy();
`
	set := Parse(source)
	functionOffset := uint32(strings.Index(source, "function value"))
	require.True(t, set.Suppresses(functionOffset, "php.returnType"))
	require.False(t, set.Suppresses(functionOffset, "php.arguments"))
	firstLegacy := uint32(strings.Index(source, "legacy();"))
	require.True(t, set.Suppresses(firstLegacy, "php.deprecated"))
	require.False(t, set.Suppresses(firstLegacy, "php.arguments"))
	secondLegacy := uint32(strings.LastIndex(source, "legacy();"))
	require.True(t, set.Suppresses(secondLegacy, "php.arguments"))
	require.False(t, set.Suppresses(secondLegacy, "php.deprecated"))
}

func TestUnrelatedSuppressionDoesNotHideDiagnostic(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @phpstan-ignore shopware.domainException */
missing();
`
	set := Parse(source)
	offset := uint32(strings.Index(source, "missing"))
	require.False(t, set.Suppresses(offset, "php.undefined"))
	require.False(t, set.Suppresses(offset, "php.arguments"))
}

func TestNoInspectionLanguageLevelAliasTargetsVersionDiagnostics(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @noinspection PhpVersionInspection */
readonly class Subject {}
`
	set := Parse(source)
	offset := uint32(strings.Index(source, "readonly"))
	require.True(t, set.Suppresses(offset, "php.version"))
	require.False(t, set.Suppresses(offset, "php.undefined"))
}
