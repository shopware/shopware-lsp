package phprewrite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassReferenceReusesExistingAliasedAndGroupedImports(t *testing.T) {
	t.Parallel()
	source := `<?php

namespace App;

use Vendor\Package\{Existing as Current, Other};

final class Demo {}`
	editor, _ := testEditor(t, source)

	reference, err := editor.ClassReference(`Vendor\Package\Existing`)
	require.NoError(t, err)
	require.Equal(t, "Current", reference)
	reference, err = editor.ClassReference(`Vendor\Package\Other`)
	require.NoError(t, err)
	require.Equal(t, "Other", reference)
	require.Equal(t, source, applyTestEditor(t, source, editor))
}

func TestClassReferenceAddsMultipleImportsOnce(t *testing.T) {
	t.Parallel()
	source := `<?php

namespace App;

final class Demo {}`
	editor, _ := testEditor(t, source)

	one, err := editor.ClassReference(`Vendor\One`)
	require.NoError(t, err)
	two, err := editor.ClassReference(`Other\Two`)
	require.NoError(t, err)
	again, err := editor.ClassReference(`Vendor\One`)
	require.NoError(t, err)
	require.Equal(t, "One", one)
	require.Equal(t, "Two", two)
	require.Equal(t, "One", again)
	require.Equal(t, `<?php

namespace App;

use Vendor\One;
use Other\Two;

final class Demo {}`, applyTestEditor(t, source, editor))
}

func TestClassReferenceFallsBackForAliasAndLocalClassConflicts(t *testing.T) {
	t.Parallel()
	source := `<?php

namespace App;

use Existing\Service;

final class Result {}
final class Demo {}`
	editor, _ := testEditor(t, source)

	reference, err := editor.ClassReference(`Vendor\Service`)
	require.NoError(t, err)
	require.Equal(t, `\Vendor\Service`, reference)
	reference, err = editor.ClassReference(`Vendor\Result`)
	require.NoError(t, err)
	require.Equal(t, `\Vendor\Result`, reference)
	require.Equal(t, source, applyTestEditor(t, source, editor))
}

func TestClassReferenceUsesSameNamespaceWithoutImport(t *testing.T) {
	t.Parallel()
	source := `<?php namespace App\Domain; final class Demo {}`
	editor, _ := testEditor(t, source)

	reference, err := editor.ClassReference(`App\Domain\Service`)
	require.NoError(t, err)
	require.Equal(t, "Service", reference)
	require.Equal(t, source, applyTestEditor(t, source, editor))
}
