package phprewrite

import (
	"testing"

	"github.com/stretchr/testify/require"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
)

func TestClassInheritanceEditsCompose(t *testing.T) {
	t.Parallel()
	source := `<?php

final class Demo implements OldContract, KeepContract {}`
	editor, root := testEditor(t, source)
	class := phpquery.Classes(root)[0]
	require.NoError(t, editor.SetExtends(class, "AbstractDemo"))
	removed, err := editor.RemoveImplements(class, "OldContract")
	require.NoError(t, err)
	require.True(t, removed)
	require.NoError(t, editor.AddImplements(class, "ExtraContract"))
	require.Equal(t, `<?php

final class Demo extends AbstractDemo implements KeepContract, ExtraContract {}`, applyTestEditor(t, source, editor))
}

func TestSetExtendsReplacesExistingParent(t *testing.T) {
	t.Parallel()
	source := `<?php class Demo extends Legacy {}`
	editor, root := testEditor(t, source)
	require.NoError(t, editor.SetExtends(phpquery.Classes(root)[0], "Modern"))
	require.Equal(t, `<?php class Demo extends Modern {}`, applyTestEditor(t, source, editor))
}

func TestRemoveExtendsAndAddAttribute(t *testing.T) {
	t.Parallel()
	source := `<?php
/** Handler. */
class Demo extends Legacy implements Existing {}`
	editor, root := testEditor(t, source)
	class := phpquery.Classes(root)[0]
	removed, err := editor.RemoveExtends(class)
	require.NoError(t, err)
	require.True(t, removed)
	require.NoError(t, editor.AddAttribute(class, "AsHandler"))
	require.Equal(t, `<?php
/** Handler. */
#[AsHandler]
class Demo implements Existing {}`, applyTestEditor(t, source, editor))
}

func TestRemoveOnlyImplementedInterface(t *testing.T) {
	t.Parallel()
	source := `<?php class Demo implements Contract {}`
	editor, root := testEditor(t, source)
	removed, err := editor.RemoveImplements(phpquery.Classes(root)[0], "Contract")
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, `<?php class Demo {}`, applyTestEditor(t, source, editor))
}

func TestInheritanceEditRejectsCommentedClause(t *testing.T) {
	t.Parallel()
	source := `<?php class Demo implements /* keep */ Contract {}`
	editor, root := testEditor(t, source)
	class := phpquery.Classes(root)[0]
	require.Error(t, editor.AddImplements(class, "Other"))
	_, err := editor.RemoveImplements(class, "Contract")
	require.Error(t, err)
}

func TestInsertClassMemberFormatsEmptyAndPopulatedClasses(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		source := `<?php final class Demo {}`
		editor, root := testEditor(t, source)
		require.NoError(t, editor.InsertClassMember(phpquery.Classes(root)[0], `
public function value(): string
{
    return 'value';
}`))
		require.Equal(t, `<?php final class Demo {
    public function value(): string
    {
        return 'value';
    }
}`, applyTestEditor(t, source, editor))
	})

	t.Run("existing indentation", func(t *testing.T) {
		t.Parallel()
		source := `<?php
final class Demo
{
	private string $name;
}`
		editor, root := testEditor(t, source)
		require.NoError(t, editor.InsertClassMember(phpquery.Classes(root)[0], `public function name(): string
{
    return $this->name;
}`))
		require.Equal(t, `<?php
final class Demo
{
	private string $name;
	public function name(): string
	{
	    return $this->name;
	}
}`, applyTestEditor(t, source, editor))
	})
}

func TestRemoveClassMemberIncludesOwnedPHPDoc(t *testing.T) {
	t.Parallel()
	source := `<?php
final class Demo
{
    private string $keep;

    /** Remove this method. */
    public function legacy(): void
    {
    }

    public function keep(): void
    {
    }
}`
	editor, root := testEditor(t, source)
	methods := phpquery.Methods(phpquery.Classes(root)[0])
	require.Len(t, methods, 2)
	require.NoError(t, editor.RemoveClassMember(methods[0]))
	require.Equal(t, `<?php
final class Demo
{
    private string $keep;

    public function keep(): void
    {
    }
}`, applyTestEditor(t, source, editor))
}
