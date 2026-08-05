package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAdminTypeFileRetainsStructuralDeclarations(t *testing.T) {
	path := "/project/Resources/app/administration/src/component/index.ts"
	source := `
import type { External as Imported } from './types';

interface Base<T> {
    id: T;
}

interface Row extends Base<string> {
    /** Whether this row is currently visible. */
    visible: boolean;
    /**
     * Label information rendered by the row.
     * @default undefined
     */
    nested?: { label: string };
}

type Result<T> = Imported & {
    value: T;
};

interface OpenRow {
    fixed: string;
    [key: string]: unknown;
}
`
	file := parseAdminTypeFile(path, source, nil)
	require.Len(t, file.Imports, 1)
	assert.Equal(t, "Imported", file.Imports[0].LocalName)
	assert.Equal(t, "External", file.Imports[0].ImportedName)
	require.Len(t, file.Declarations, 4)

	row, found := adminTypeDeclarationNamed(file, "Row")
	require.True(t, found)
	assert.Equal(t, []string{"Base<string>"}, row.Extends)
	require.Len(t, row.Members, 2)
	assert.Equal(t, "nested", row.Members[0].Name)
	assert.Equal(t, "{ label: string }", row.Members[0].Type)
	assert.Equal(
		t, "Label information rendered by the row.",
		row.Members[0].Documentation,
	)
	assert.Equal(t, path, row.Members[0].DefinitionPath)
	assert.Positive(t, row.Members[0].DefinitionLine)
	nestedStart := uint32(strings.Index(source, "nested?:"))
	nestedLine, nestedCharacter := cst.NewLineIndex(source).PositionUTF16(
		nestedStart,
	)
	assert.Equal(t, AdminSourceRange{
		StartLine: int(nestedLine), StartCharacter: int(nestedCharacter),
		EndLine: int(nestedLine), EndCharacter: int(nestedCharacter) + len("nested"),
		Declaration: true, Identifier: true,
	}, row.Members[0].DefinitionRange)
	assert.Equal(
		t, "Whether this row is currently visible.",
		row.Members[1].Documentation,
	)

	result, found := adminTypeDeclarationNamed(file, "Result")
	require.True(t, found)
	assert.Equal(t, []string{"T"}, result.Parameters)
	assert.Contains(t, result.Alias, "Imported")
	assert.Contains(t, result.Alias, "value: T")
	openRow, found := adminTypeDeclarationNamed(file, "OpenRow")
	require.True(t, found)
	assert.True(t, openRow.Open)
	assert.Contains(t, vueMemberNames(openRow.Members), "fixed")
}

func TestVueTypeMembersAcceptCommaSeparatedObjectFields(t *testing.T) {
	members := VueTypeMembers(`{
		name?: string,
		distinguishableName?: string,
		meta: Record<string, { label: string, count: number }>,
		format: (value: string, options: { locale: string, currency: string }) => string
	}`)

	byName := make(map[string]TwigVueMember, len(members))
	for _, member := range members {
		byName[member.Name] = member
	}
	require.Len(t, byName, 4)
	assert.Equal(t, "string", byName["name"].Type)
	assert.Equal(t, "string", byName["distinguishableName"].Type)
	assert.Equal(
		t,
		"Record<string, { label: string, count: number }>",
		byName["meta"].Type,
	)
	assert.Equal(
		t,
		"(value: string, options: { locale: string, currency: string }) => string",
		byName["format"].Type,
	)
}

func TestAdminTypeUnionIncludesBranchSpecificObjectFields(t *testing.T) {
	idx, err := NewAdminComponentIndexer(filepath.Join(t.TempDir(), "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	shape, err := idx.ResolveVueType(
		"{ id: string; title: string } | "+
			"{ id: string; title: string; privilege: string }",
		"/project/component.ts",
	)
	require.NoError(t, err)
	assert.True(t, shape.Complete)
	privilege, found := twigVueMemberNamed(shape.Members, "privilege")
	require.True(t, found)
	assert.Equal(t, "string | undefined", privilege.Type)
}

func TestAdminTypeIndexResolvesLocalImportedInheritedAndGenericShapes(
	t *testing.T,
) {
	root := t.TempDir()
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	cache := filepath.Join(root, "cache")
	idx, err := NewAdminComponentIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	typesPath := filepath.Join(adminRoot, "component/row.types.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		typesPath,
		[]byte(`
export interface External<T> {
    id: T;
    child: { label: string; count: number };
}
`),
	)))
	componentPath := filepath.Join(adminRoot, "component/index.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		componentPath,
		[]byte(`
import type { External as BaseRow } from './row.types';
interface AddressRow extends BaseRow<string> {
    visible: boolean;
}
type OptionalAddressRow = AddressRow | null;
interface OpenAddressRow {
    visible: boolean;
    [key: string]: unknown;
}
type OpenAddressAlias = {
    label: string;
    [name: string]: unknown;
};
`),
	)))
	indexedFile, indexed, err := idx.adminTypeFile(componentPath)
	require.NoError(t, err)
	require.True(t, indexed)
	require.Len(t, indexedFile.Declarations, 4)
	require.Len(t, indexedFile.Imports, 1)

	directShape, err := idx.ResolveVueType("AddressRow", componentPath)
	require.NoError(t, err)
	require.True(t, directShape.Complete)
	shape, err := idx.ResolveVueType("OptionalAddressRow", componentPath)
	require.NoError(t, err)
	assert.True(t, shape.Complete)
	byName := make(map[string]TwigVueMember)
	for _, member := range shape.Members {
		byName[member.Name] = member
	}
	assert.Equal(t, "string", byName["id"].Type)
	assert.Equal(t, "{ label: string; count: number }", byName["child"].Type)
	assert.Equal(t, "boolean", byName["visible"].Type)
	assert.Equal(t, typesPath, byName["id"].DefinitionPath)
	assert.Equal(t, componentPath, byName["visible"].DefinitionPath)
	for _, openType := range []string{"OpenAddressRow", "OpenAddressAlias"} {
		openShape, openErr := idx.ResolveVueType(openType, componentPath)
		require.NoError(t, openErr)
		assert.False(t, openShape.Complete, openType)
		assert.NotEmpty(t, openShape.Members, openType)
	}

	child, err := idx.ResolveVueType(byName["child"].Type, typesPath)
	require.NoError(t, err)
	assert.True(t, child.Complete)
	require.Len(t, child.Members, 2)
	assert.ElementsMatch(t, []string{"count", "label"}, []string{
		child.Members[0].Name, child.Members[1].Name,
	})
}

func TestAdminTypeIndexResolvesShopwareEntitySchemaAndCollection(t *testing.T) {
	root := t.TempDir()
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	schemaPath := filepath.Join(adminRoot, "entity-schema-definition.d.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		schemaPath,
		[]byte(`
declare namespace EntitySchema {
    interface product {
        id: string;
        name: string;
        manufacturer?: Entity<'product_manufacturer'>;
    }
    interface product_manufacturer {
        id: string;
        name: string;
    }
}
`),
	)))
	augmentationPath := filepath.Join(adminRoot, "plugin/entity-extension.d.ts")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		augmentationPath,
		[]byte(`
declare namespace EntitySchema {
    interface product { customSearchKeyword?: string; }
}
`),
	)))

	shape, err := idx.ResolveVueType(
		"Entity<'product'>", filepath.Join(adminRoot, "component/index.ts"),
	)
	require.NoError(t, err)
	require.True(t, shape.Complete)
	byName := make(map[string]TwigVueMember, len(shape.Members))
	for _, member := range shape.Members {
		byName[member.Name] = member
	}
	assert.Equal(t, "string", byName["name"].Type)
	assert.Equal(
		t, "Entity<'product_manufacturer'>", byName["manufacturer"].Type,
	)
	assert.Equal(t, "string", byName["customSearchKeyword"].Type)
	assert.Equal(t, "() => string", byName["getEntityName"].Type)
	assert.Equal(t, schemaPath, byName["manufacturer"].DefinitionPath)
	assert.Equal(t, augmentationPath, byName["customSearchKeyword"].DefinitionPath)
	propShape, err := idx.ResolveVueType(
		"Object as PropType<Entity<'product'>>",
		filepath.Join(adminRoot, "component/index.ts"),
	)
	require.NoError(t, err)
	require.True(t, propShape.Complete)
	assert.Contains(t, vueMemberNames(propShape.Members), "manufacturer")

	manufacturer, err := idx.ResolveVueType(
		byName["manufacturer"].Type, byName["manufacturer"].DefinitionPath,
	)
	require.NoError(t, err)
	require.True(t, manufacturer.Complete)
	assert.Contains(t, vueMemberNames(manufacturer.Members), "name")

	collection, err := idx.ResolveVueType(
		"EntitySchema.EntityCollection<'product'>", schemaPath,
	)
	require.NoError(t, err)
	assert.False(t, collection.Complete)
	assert.Contains(t, vueMemberNames(collection.Members), "total")
	assert.Contains(t, vueMemberNames(collection.Members), "filter")
	collectionFilter, found := twigVueMemberNamed(collection.Members, "filter")
	require.True(t, found)
	assert.Equal(
		t,
		"(predicate: Function) => Array<Entity<'product'>>",
		collectionFilter.Type,
	)
	collectionFind, found := twigVueMemberNamed(collection.Members, "find")
	require.True(t, found)
	assert.Equal(
		t,
		"(predicate: Function) => Entity<'product'> | undefined",
		collectionFind.Type,
	)
	assert.Equal(
		t, "Entity<'product'>",
		VueIterableElementType("EntityCollection<'product'> | null"),
	)
	stringShape, err := idx.ResolveVueType("string", schemaPath)
	require.NoError(t, err)
	assert.False(t, stringShape.Complete)
	assert.Contains(t, vueMemberNames(stringShape.Members), "length")
	assert.Contains(t, vueMemberNames(stringShape.Members), "trim")
	arrayShape, err := idx.ResolveVueType("Entity<'product'>[]", schemaPath)
	require.NoError(t, err)
	assert.False(t, arrayShape.Complete)
	assert.Contains(t, vueMemberNames(arrayShape.Members), "length")
	assert.Contains(t, vueMemberNames(arrayShape.Members), "find")
	recordShape, err := idx.ResolveVueType(
		"Record<string, Entity<'product'>>", schemaPath,
	)
	require.NoError(t, err)
	assert.False(t, recordShape.Complete)
	assert.Empty(t, recordShape.Members)
	functionShape, err := idx.ResolveVueType("Function", schemaPath)
	require.NoError(t, err)
	assert.False(t, functionShape.Complete)
	assert.Contains(t, vueMemberNames(functionShape.Members), "call")
	for _, openType := range []string{
		"unknown", "any", "object", "Object", "Date", "RegExp",
		"null", "undefined", "void", "never",
	} {
		openShape, openErr := idx.ResolveVueType(openType, schemaPath)
		require.NoError(t, openErr)
		assert.False(
			t, openShape.Complete,
			"%s must not drive closed-world markup diagnostics",
			openType,
		)
	}
}
