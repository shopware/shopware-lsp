package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigVueRootIdentifiersExcludeKeywordsAndArrowLocals(t *testing.T) {
	source := `<div v-for="row in rows.filter((candidate) => candidate.active)">{{ rows.map(({ name: label }) => label).length }} {{ outside }}</div>`
	root := twigparser.Parse(source).Tree.Root
	identifiers := TwigVueExpressionRootIdentifiers(root, []byte(source))
	names := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		names = append(names, identifier.Name)
	}
	assert.Equal(t, []string{"row", "rows", "rows", "outside"}, names)
	assert.NotContains(t, names, "in")
	assert.NotContains(t, names, "candidate")
	assert.NotContains(t, names, "label")
}

func TestTwigVueForBindingsAreLexicalAndShadowed(t *testing.T) {
	source := `<ul>
    <li v-for="(item, index) in items" :key="item.id">
        {{ item.name }} {{ index }}
        <span v-for="item of item.children">{{ item.name }}</span>
        {{ item.id }}
    </li>
</ul>
{{ item }}`
	root := twigparser.Parse(source).Tree.Root

	outerOffset := uint32(strings.Index(source, "item.name") + 2)
	outer, found := TwigVueBindingAtOffset(root, []byte(source), outerOffset)
	require.True(t, found)
	assert.Equal(t, TwigVueBindingFor, outer.Kind)
	assert.Equal(t, "item", outer.Name)
	assert.Equal(t, "items", outer.Iterable)

	indexOffset := uint32(strings.Index(source, "{{ index") + len("{{ in"))
	indexBinding, found := TwigVueBindingAtOffset(
		root, []byte(source), indexOffset,
	)
	require.True(t, found)
	assert.Equal(t, "index", indexBinding.Name)

	innerOffset := uint32(strings.LastIndex(source, "item.name") + 2)
	inner, found := TwigVueBindingAtOffset(root, []byte(source), innerOffset)
	require.True(t, found)
	assert.Equal(t, "item.children", inner.Iterable)
	assert.Less(t, inner.ScopeRange.Len(), outer.ScopeRange.Len())

	afterInnerOffset := uint32(strings.LastIndex(source, "item.id") + 2)
	afterInner, found := TwigVueBindingAtOffset(
		root, []byte(source), afterInnerOffset,
	)
	require.True(t, found)
	assert.Equal(t, outer.DeclarationRange, afterInner.DeclarationRange)

	outsideOffset := uint32(strings.LastIndex(source, "{{ item") + len("{{ it"))
	_, found = TwigVueBindingAtOffset(root, []byte(source), outsideOffset)
	assert.False(t, found)
}

func TestTwigVueForBindingIsNotVisibleInIterable(t *testing.T) {
	source := `<div v-for="item in item.children">{{ item }}</div>`
	root := twigparser.Parse(source).Tree.Root

	declarationOffset := uint32(strings.Index(source, "item in") + 1)
	declaration, found := TwigVueBindingAtOffset(
		root, []byte(source), declarationOffset,
	)
	require.True(t, found)
	assert.True(t, declaration.IsDeclarationOffset(declarationOffset))

	iterableOffset := uint32(strings.Index(source, "item.children") + 1)
	_, found = TwigVueBindingAtOffset(root, []byte(source), iterableOffset)
	assert.False(t, found)
}

func TestTwigVueEventBindingAndPayloadType(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "mt-switch",
		Events: []VueComponentEvent{{
			Name: "update:modelValue", Type: "(value: boolean) => any",
			FilePath: "/meteor/MtSwitch.d.ts", Line: 17,
		}},
	}))
	source := `<mt-switch @update:model-value="update($event)" />`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "$event") + 2)
	binding, err := index.ResolveTwigVueBinding(root, []byte(source), offset)
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.Equal(t, TwigVueBindingEvent, binding.Kind)
	assert.Equal(t, "boolean", binding.Type)
	assert.Equal(t, "/meteor/MtSwitch.d.ts", binding.DefinitionPath)
	assert.Equal(t, 17, binding.DefinitionLine)
}

func TestTwigVueForBindingGetsIterableElementType(t *testing.T) {
	rootDir := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(rootDir, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		rootDir, "Resources/app/administration/view.html.twig",
	)
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-view", TemplatePath: templatePath,
		Props: []VueComponentProp{
			{Name: "products", Type: "Array as PropType<Product[]>"},
			{Name: "productsById", Type: "Record<string, Product>"},
		},
		Members: []VueComponentMember{{
			Name: "thumbnailSizes", Kind: ComponentMemberData,
			Type: "Array<{ id: string }>",
			ElementMembers: []VueComponentElementMember{{
				Name: "deletable", Type: "boolean",
				FilePath: "/project/component.js", Line: 17,
			}},
		}, {
			Name: "sections", Kind: ComponentMemberComputed,
			Type:             "{ accessibility: Array<{ id: string }> }",
			OpenRuntimeShape: true,
		}},
	}))
	source := `<div v-for="(product, index) in products">{{ product.name }} {{ index }}</div>`
	twigRoot := twigparser.Parse(source).Tree.Root
	productOffset := uint32(strings.Index(source, "product.name") + 1)
	product, err := index.ResolveTwigVueBinding(
		twigRoot, []byte(source), productOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, product)
	assert.Equal(t, "Product", product.Type)
	assert.Equal(t, []string{"name"}, vueMemberNames(product.Members))
	indexOffset := uint32(strings.LastIndex(source, "{{ index") + len("{{ in"))
	iterationIndex, err := index.ResolveTwigVueBinding(
		twigRoot, []byte(source), indexOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, iterationIndex)
	assert.Equal(t, "number", iterationIndex.Type)

	augmentedSource := `<div v-for="size in thumbnailSizes">{{ size.deletable }}</div>`
	augmentedRoot := twigparser.Parse(augmentedSource).Tree.Root
	augmentedOffset := uint32(strings.Index(augmentedSource, "deletable") + 2)
	augmented, err := index.ResolveTwigVueMember(
		augmentedRoot, []byte(augmentedSource), augmentedOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, augmented)
	assert.True(t, augmented.MembersComplete)
	assert.True(t, augmented.MemberFound)
	assert.Equal(t, "boolean", augmented.Member.Type)
	assert.Equal(t, "/project/component.js", augmented.Member.DefinitionPath)
	assert.Equal(t, 17, augmented.Member.DefinitionLine)

	openSource := `<div v-for="section in sections.accessibility">{{ section.privilege }}</div>`
	openRoot := twigparser.Parse(openSource).Tree.Root
	openOffset := uint32(strings.LastIndex(openSource, "section.privilege") + 2)
	openBinding, err := index.ResolveTwigVueBinding(
		openRoot, []byte(openSource), openOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, openBinding)
	assert.False(t, openBinding.MembersComplete)

	recordSource := `<div v-for="(product, productId, index) in productsById">{{ product.name }} {{ productId.length }} {{ index.toFixed() }}</div>`
	recordRoot := twigparser.Parse(recordSource).Tree.Root
	productOffset = uint32(strings.Index(recordSource, "product.name") + 1)
	product, err = index.ResolveTwigVueBinding(
		recordRoot, []byte(recordSource), productOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, product)
	assert.Equal(t, "Product", product.Type)
	keyOffset := uint32(strings.Index(recordSource, "productId.length") + 1)
	key, err := index.ResolveTwigVueBinding(
		recordRoot, []byte(recordSource), keyOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, key)
	assert.Equal(t, "string", key.Type)
	recordIndexOffset := uint32(strings.Index(recordSource, "index.toFixed") + 1)
	recordIndex, err := index.ResolveTwigVueBinding(
		recordRoot, []byte(recordSource), recordIndexOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, recordIndex)
	assert.Equal(t, "number", recordIndex.Type)
}

func TestResolveTwigVueNestedNamedTypeMember(t *testing.T) {
	rootDir := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(rootDir, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		rootDir, "Resources/app/administration/src/component",
	)
	definitionPath := filepath.Join(adminRoot, "index.ts")
	templatePath := filepath.Join(adminRoot, "view.html.twig")
	require.NoError(t, index.Index(indexer.NewParsedFile(
		definitionPath,
		[]byte(`
interface Address { city: string; }
interface Manufacturer { name: string; address: Address; }
interface Option { label: string; }
interface Variant { options: Option[]; }
interface Product { id: string; manufacturer: Manufacturer; variants: Variant[]; }
`),
	)))
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-view", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Members: []VueComponentMember{{
			Name: "products", Kind: ComponentMemberComputed,
			Type: "Product[]", FilePath: definitionPath,
		}},
	}))
	source := `<div v-for="product in products">{{ product.manufacturer.address.city }} {{ product.manufacturer.address.city }}</div>`
	twigRoot := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "city") + 2)
	resolved, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), offset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.True(t, resolved.ReceiverFound)
	assert.True(t, resolved.MemberFound)
	assert.Equal(t, "city", resolved.Member.Name)
	assert.Equal(t, "string", resolved.Member.Type)
	assert.Equal(t, definitionPath, resolved.Member.DefinitionPath)
	assert.Equal(
		t,
		[]string{"manufacturer", "address", "city"},
		resolved.Access.MemberPath(),
	)
	references := TwigVueBindingMemberPathReferences(
		twigRoot, []byte(source), resolved.Binding,
		resolved.Access.MemberPath(),
	)
	assert.Len(t, references, 2)

	nestedLoopSource := `<div v-for="product in products"><div v-for="variant in product.variants"><span v-for="option in variant.options">{{ option.label }}</span></div></div>`
	nestedLoopRoot := twigparser.Parse(nestedLoopSource).Tree.Root
	nestedOffset := uint32(strings.Index(nestedLoopSource, "option.label") +
		len("option.la"))
	nested, err := index.ResolveTwigVueMember(
		nestedLoopRoot, []byte(nestedLoopSource), nestedOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, nested)
	assert.Equal(t, "Option", nested.Binding.Type)
	assert.True(t, nested.MemberFound)
	assert.Equal(t, "string", nested.Member.Type)
}

func TestResolveTwigVueComponentEntityMemberAndNestedEntityLoop(t *testing.T) {
	rootDir := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(rootDir, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		rootDir, "Resources/app/administration/src/component",
	)
	schemaPath := filepath.Join(filepath.Dir(adminRoot), "entity-schema-definition.d.ts")
	require.NoError(t, index.Index(indexer.NewParsedFile(
		schemaPath,
		[]byte(`
declare namespace EntitySchema {
    interface cms_page {
        id: string;
        name: string;
        sections: EntityCollection<'cms_section'>;
    }
    interface cms_section { id: string; name: string; }
}
`),
	)))
	definitionPath := filepath.Join(adminRoot, "index.ts")
	templatePath := filepath.Join(adminRoot, "view.html.twig")
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-cms-page", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Members: []VueComponentMember{
			{
				Name: "page", Kind: ComponentMemberProp,
				Type: "Entity<'cms_page'>", FilePath: definitionPath,
			},
			{
				Name: "getPage", Kind: ComponentMemberMethod,
				Type: "() => Entity<'cms_page'>", FilePath: definitionPath,
			},
			{
				Name: "sectionsById", Kind: ComponentMemberComputed,
				Type:     "Record<string, Entity<'cms_section'>>",
				FilePath: definitionPath,
			},
			{
				Name: "sectionGroups", Kind: ComponentMemberComputed,
				Type:     "Record<string, EntityCollection<'cms_section'>>",
				FilePath: definitionPath,
			},
			{
				Name: "currentGroup", Kind: ComponentMemberProp,
				Type: "string", FilePath: definitionPath,
			},
		},
	}))

	source := `<div>{{ page.name }} {{ page.sections.first().name }} {{ page.sections[0].name }} {{ sectionsById[currentGroup].name }} {{ getPage().name }}` +
		`<section v-for="section in page.sections">{{ section.name }}</section>` +
		`<section v-for="group in sectionGroups">{{ group[0].name }}</section>` +
		`<section v-for="filteredSection in page.sections.filter((section) => section.name)">{{ filteredSection.name }}</section>` +
		`<section v-for="mappedName in page.sections?.map((section) => section.name) ?? []">{{ mappedName.length }}</section>` +
		`<section v-for="mappedSection in page.sections.map((section) => ({ id: section.id, label: section.name }))">{{ mappedSection.label }}</section>` +
		`<section v-for="recordSection in Object.values(sectionsById)">{{ recordSection.name }}</section>` +
		`<section v-for="groupedSection in sectionGroups[currentGroup]">{{ groupedSection.name }}</section>` +
		`<section v-for="staticLabel in ['first', 'second']">{{ staticLabel.length }}</section>` +
		`</div>`
	twigRoot := twigparser.Parse(source).Tree.Root
	pageOffset := uint32(strings.Index(source, "page.name") + len("page.na"))
	resolved, err := index.ResolveTwigVueInstanceMember(
		twigRoot, []byte(source), pageOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.True(t, resolved.ReceiverFound)
	assert.True(t, resolved.MemberFound)
	assert.Equal(t, "page.name", resolved.QualifiedName())
	assert.Equal(t, "string", resolved.Member.Type)
	assert.Equal(t, schemaPath, resolved.Member.DefinitionPath)
	collectionCallOffset := uint32(strings.Index(
		source, "page.sections.first().name",
	) + len("page.sections.first().na"))
	collectionCall, err := index.ResolveTwigVueInstanceMember(
		twigRoot, []byte(source), collectionCallOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, collectionCall)
	require.True(t, collectionCall.MemberFound)
	assert.Equal(t, "string", collectionCall.Member.Type)
	assert.Equal(t, "Entity<'cms_section'> | null", collectionCall.ReceiverType)
	assert.Equal(
		t, "page.sections.first().name",
		collectionCall.QualifiedName(),
	)
	indexedCollectionOffset := uint32(strings.Index(
		source, "page.sections[0].name",
	) + len("page.sections[0].na"))
	indexedCollection, err := index.ResolveTwigVueInstanceMember(
		twigRoot, []byte(source), indexedCollectionOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, indexedCollection)
	require.True(t, indexedCollection.MemberFound)
	assert.Equal(t, "string", indexedCollection.Member.Type)
	assert.Equal(t, "Entity<'cms_section'>", indexedCollection.ReceiverType)
	assert.Equal(
		t, "page.sections[0].name", indexedCollection.QualifiedName(),
	)
	indexedRecordOffset := uint32(strings.Index(
		source, "sectionsById[currentGroup].name",
	) + len("sectionsById[currentGroup].na"))
	indexedRecord, err := index.ResolveTwigVueInstanceMember(
		twigRoot, []byte(source), indexedRecordOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, indexedRecord)
	require.True(t, indexedRecord.MemberFound, "resolved=%#v", indexedRecord)
	assert.Equal(t, "string", indexedRecord.Member.Type)
	assert.Equal(t, "Entity<'cms_section'>", indexedRecord.ReceiverType)
	assert.Equal(
		t, "sectionsById[currentGroup].name", indexedRecord.QualifiedName(),
	)
	rootCallOffset := uint32(strings.Index(source, "getPage().name") +
		len("getPage().na"))
	rootCall, err := index.ResolveTwigVueInstanceMember(
		twigRoot, []byte(source), rootCallOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, rootCall)
	require.True(t, rootCall.MemberFound)
	assert.Equal(t, "string", rootCall.Member.Type)
	assert.Equal(t, "getPage().name", rootCall.QualifiedName())
	groupItemOffset := uint32(strings.Index(source, "group[0].name") +
		len("group[0].na"))
	groupItem, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), groupItemOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, groupItem)
	require.True(t, groupItem.MemberFound)
	assert.Equal(t, "EntityCollection<'cms_section'>", groupItem.Binding.Type)
	assert.Equal(t, "Entity<'cms_section'>", groupItem.ReceiverType)
	assert.Equal(t, "string", groupItem.Member.Type)

	sectionOffset := uint32(strings.Index(source, "{{ section.name") +
		len("{{ section.na"))
	section, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), sectionOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, section)
	assert.Equal(t, "Entity<'cms_section'>", section.Binding.Type)
	assert.True(t, section.Binding.MembersComplete)
	assert.True(t, section.MemberFound)
	assert.Equal(t, schemaPath, section.Member.DefinitionPath)
	filteredOffset := uint32(strings.LastIndex(source, "filteredSection.name") +
		len("filteredSection.na"))
	filtered, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), filteredOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, filtered)
	assert.Equal(t, "Entity<'cms_section'>", filtered.Binding.Type)
	assert.True(t, filtered.MemberFound)
	assert.Equal(t, "string", filtered.Member.Type)
	assert.Equal(t, schemaPath, filtered.Member.DefinitionPath)
	mappedNameOffset := uint32(strings.LastIndex(source, "mappedName.length") +
		len("mappedName.len"))
	mappedName, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), mappedNameOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, mappedName)
	assert.Equal(t, "string", mappedName.Binding.Type)
	require.True(t, mappedName.MemberFound)
	assert.Equal(t, "number", mappedName.Member.Type)
	mappedSectionOffset := uint32(strings.LastIndex(source, "mappedSection.label") +
		len("mappedSection.la"))
	mappedSection, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), mappedSectionOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, mappedSection)
	assert.Equal(
		t, "{ id: string; label: string }", mappedSection.Binding.Type,
	)
	require.True(t, mappedSection.MemberFound)
	assert.Equal(t, "string", mappedSection.Member.Type)
	recordSectionOffset := uint32(strings.LastIndex(source, "recordSection.name") +
		len("recordSection.na"))
	recordSection, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), recordSectionOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, recordSection)
	assert.Equal(t, "Entity<'cms_section'>", recordSection.Binding.Type)
	require.True(t, recordSection.MemberFound)
	assert.Equal(t, "string", recordSection.Member.Type)
	groupedSectionOffset := uint32(strings.LastIndex(source, "groupedSection.name") +
		len("groupedSection.na"))
	groupedSection, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), groupedSectionOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, groupedSection)
	assert.Equal(t, "Entity<'cms_section'>", groupedSection.Binding.Type)
	require.True(t, groupedSection.MemberFound)
	assert.Equal(t, "string", groupedSection.Member.Type)
	staticLabelOffset := uint32(strings.LastIndex(source, "staticLabel.length") +
		len("staticLabel.len"))
	staticLabel, err := index.ResolveTwigVueMember(
		twigRoot, []byte(source), staticLabelOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, staticLabel)
	assert.Equal(t, "string", staticLabel.Binding.Type)
	require.True(t, staticLabel.MemberFound)
	assert.Equal(t, "number", staticLabel.Member.Type)

	shadowedSource := `<div v-for="page in pages">{{ page.name }}</div>`
	shadowedRoot := twigparser.Parse(shadowedSource).Tree.Root
	shadowedOffset := uint32(strings.Index(shadowedSource, "page.name") +
		len("page.na"))
	shadowed, err := index.ResolveTwigVueInstanceMember(
		shadowedRoot, []byte(shadowedSource), shadowedOffset, templatePath,
	)
	require.NoError(t, err)
	assert.Nil(t, shadowed)
}

func TestTwigVueBindingReferencesExcludePropertiesStringsAndShadowing(
	t *testing.T,
) {
	source := `<div v-for="item in items">
    {{ item.name }}
    <button @click="select(item, 'item', { item: item })"></button>
    <span v-for="item in children">{{ item }}</span>
</div>`
	root := twigparser.Parse(source).Tree.Root
	declarationOffset := uint32(strings.Index(source, "item in items") + 1)
	target, found := TwigVueBindingAtOffset(
		root, []byte(source), declarationOffset,
	)
	require.True(t, found)
	references := TwigVueBindingReferences(root, []byte(source), *target)
	var values []string
	for _, reference := range references {
		values = append(values, string([]byte(source)[reference.Start:reference.End]))
	}
	assert.Equal(t, []string{"item", "item", "item", "item"}, values)
	for _, reference := range references {
		assert.NotEqual(t, uint32(strings.Index(source, ".name")+1), reference.Start)
		assert.NotEqual(t, uint32(strings.Index(source, "'item'")+1), reference.Start)
		assert.NotEqual(t, uint32(strings.Index(source, "{ item:")+2), reference.Start)
		assert.NotEqual(t, uint32(strings.LastIndex(source, "{{ item")+3), reference.Start)
	}
}

func TestNativeVueEventPayloadTypes(t *testing.T) {
	assert.Equal(t, "MouseEvent", nativeEventPayloadType("click"))
	assert.Equal(t, "KeyboardEvent", nativeEventPayloadType("keyup"))
	assert.Equal(t, "FocusEvent", nativeEventPayloadType("blur"))
	assert.Equal(t, "boolean", eventPayloadType("(value: boolean) => void"))
	assert.Equal(t, "", eventPayloadType("() => void"))
	assert.Equal(t, "string", eventPayloadType("[id: string]"))
	assert.Equal(t, "Product", eventPayloadType("[Product, boolean]"))
	assert.Equal(
		t, "string",
		eventPayloadType("(event: 'close', reason: string)"),
	)
	assert.Equal(t, "any", eventPayloadType("(...args: any[]) => void"))
	assert.Equal(t, "UploadTask[]", eventPayloadType("UploadTask[]"))
	assert.Equal(t, "", eventPayloadType("void"))
}

func TestVueIterableElementType(t *testing.T) {
	for value, expected := range map[string]string{
		"Product[]":                                        "Product",
		"readonly Product[]":                               "Product",
		"Array<Product>":                                   "Product",
		"ReadonlyArray<Record<string, number>>":            "Record<string, number>",
		"Array as PropType<Array<Product>>":                "Product",
		"Array as unknown as PropType<ReadonlyArray<Row>>": "Row",
		"[Product, Category]":                              "Product | Category",
		"Record<string, Product>":                          "Product",
		"ReadonlyMap<string, Product>":                     "[string, Product]",
		"{ primary: Product; fallback: Category }":         "Category | Product",
		"number": "number",
		"string": "string",
		"Array":  "",
		"Object": "",
	} {
		assert.Equal(t, expected, VueIterableElementType(value), value)
	}
}

func TestVueIterableBindingTypeDistinguishesKeysAndIndexes(t *testing.T) {
	assert.Equal(t, "Product", VueIterableBindingType("Product[]", 0))
	assert.Equal(t, "number", VueIterableBindingType("Product[]", 1))
	assert.Equal(t, "Product", VueIterableBindingType(
		"Record<string, Product>", 0,
	))
	assert.Equal(t, "string", VueIterableBindingType(
		"Record<string, Product>", 1,
	))
	assert.Equal(t, "number", VueIterableBindingType(
		"Record<string, Product>", 2,
	))
	assert.Equal(t, "string", VueIterableBindingType(
		"{ primary: Product; fallback: Product }", 1,
	))
}

func TestTwigVueReferencesHandleTemplateLiteralInterpolations(t *testing.T) {
	source := `<div v-for="item in items" :key="` +
		"`literal-item-${item.id}-${`nested-${item.name}`}`" +
		`">{{ item }}</div>`
	root := twigparser.Parse(source).Tree.Root
	declarationOffset := uint32(strings.Index(source, "item in items") + 1)
	target, found := TwigVueBindingAtOffset(
		root, []byte(source), declarationOffset,
	)
	require.True(t, found)
	references := TwigVueBindingReferences(root, []byte(source), *target)
	assert.Len(t, references, 4)
	for _, expected := range []string{"${item.id}", "${item.name}", "{{ item }}"} {
		position := uint32(strings.Index(source, expected) + strings.Index(expected, "item"))
		assert.Contains(t, references, cst.TextRange{
			Start: position, End: position + uint32(len("item")),
		})
	}
	literalPosition := uint32(strings.Index(source, "literal-item") + len("literal-"))
	for _, reference := range references {
		assert.NotEqual(t, literalPosition, reference.Start)
	}
}

func TestTwigVueExpressionMemberAccessAndCompletionCursor(t *testing.T) {
	source := `<div :title="row?.title" :data-value="row.">{{ row.name }} {{ parent.row.name }} {{ products.last().name }} {{ getProduct?.().manufacturer.name }} {{ products.get(productId).name }} {{ products[0].name }} {{ productsById?.[productId].name }} {{ 'row.fake' }}</div>`
	root := twigparser.Parse(source).Tree.Root

	titleOffset := uint32(strings.Index(source, "row?.title") + len("row?.ti"))
	title, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), titleOffset,
	)
	require.True(t, found)
	assert.Equal(t, "row", title.Root)
	assert.Equal(t, "title", title.Member)

	completionOffset := uint32(strings.Index(source, `row."`) + len("row."))
	completion, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), completionOffset,
	)
	require.True(t, found)
	assert.Equal(t, "row", completion.Root)
	assert.Empty(t, completion.Member)
	assert.Equal(t, completionOffset, completion.MemberRange.Start)

	chainedOffset := uint32(strings.Index(source, "parent.row.name") + len("parent.row.na"))
	chained, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), chainedOffset,
	)
	require.True(t, found)
	assert.Equal(t, "parent", chained.Root)
	assert.Equal(t, "name", chained.Member)
	require.Len(t, chained.Receiver, 1)
	assert.Equal(t, "row", chained.Receiver[0].Name)
	assert.Equal(t, []string{"row", "name"}, chained.MemberPath())
	calledOffset := uint32(strings.Index(source, "products.last().name") +
		len("products.last().na"))
	called, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), calledOffset,
	)
	require.True(t, found)
	assert.Equal(t, "products", called.Root)
	require.Len(t, called.Receiver, 1)
	assert.Equal(t, "last", called.Receiver[0].Name)
	assert.True(t, called.Receiver[0].Called)
	assert.Equal(t, "products.last().name", called.QualifiedName())
	rootCallOffset := uint32(strings.Index(
		source, "getProduct?.().manufacturer.name",
	) + len("getProduct?.().manufacturer.na"))
	rootCall, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), rootCallOffset,
	)
	require.True(t, found)
	assert.True(t, rootCall.RootCalled)
	assert.Equal(
		t, "getProduct().manufacturer.name", rootCall.QualifiedName(),
	)
	argumentCallOffset := uint32(strings.Index(
		source, "products.get(productId).name",
	) + len("products.get(productId).na"))
	argumentCall, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), argumentCallOffset,
	)
	require.True(t, found)
	require.Len(t, argumentCall.Receiver, 1)
	assert.True(t, argumentCall.Receiver[0].Called)
	methodOffset := uint32(strings.Index(source, "last().name") + 2)
	method, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), methodOffset,
	)
	require.True(t, found)
	assert.Equal(t, "last", method.Member)
	assert.True(t, method.MemberCalled)
	computedOffset := uint32(strings.Index(source, "products[0].name") +
		len("products[0].na"))
	computed, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), computedOffset,
	)
	require.True(t, found)
	assert.Equal(t, "products", computed.Root)
	require.Len(t, computed.Receiver, 1)
	assert.True(t, computed.Receiver[0].Indexed)
	assert.Equal(t, "0", computed.Receiver[0].IndexExpression)
	assert.Equal(t, []string{"[0]", "name"}, computed.MemberPath())
	assert.Equal(t, "products[0].name", computed.QualifiedName())
	dynamicOffset := uint32(strings.Index(source, "productsById?.[productId].name") +
		len("productsById?.[productId].na"))
	dynamic, found := TwigVueExpressionMemberAtOffset(
		root, []byte(source), dynamicOffset,
	)
	require.True(t, found)
	assert.Equal(t, "productsById", dynamic.Root)
	require.Len(t, dynamic.Receiver, 1)
	assert.True(t, dynamic.Receiver[0].Indexed)
	assert.True(t, dynamic.Receiver[0].Optional)
	assert.Equal(t, "productId", dynamic.Receiver[0].IndexExpression)
	assert.Equal(t, "productsById?.[productId].name", dynamic.QualifiedName())
	literalOffset := uint32(strings.Index(source, "row.fake") + len("row.fa"))
	_, found = TwigVueExpressionMemberAtOffset(
		root, []byte(source), literalOffset,
	)
	assert.False(t, found)
}

func TestTwigVueCallAtOffsetTracksNestedArgumentsAndFilters(t *testing.T) {
	source := `<button @click="save(first, nested('x,y'), { tags: ['a', 'b'] }, ` +
		"`label-${format(value, 2)}`); value | default('fallback')\">Save</button>"
	root := twigparser.Parse(source).Tree.Root

	outerOffset := uint32(strings.Index(source, "'b'") + 1)
	outer, found := TwigVueCallAtOffset(root, []byte(source), outerOffset)
	require.True(t, found)
	assert.Equal(t, "save", outer.Name)
	assert.Equal(t, 2, outer.ActiveArgument)
	assert.False(t, outer.Filter)

	nestedOffset := uint32(strings.Index(source, "'x,y'") + 2)
	nested, found := TwigVueCallAtOffset(root, []byte(source), nestedOffset)
	require.True(t, found)
	assert.Equal(t, "nested", nested.Name)
	assert.Zero(t, nested.ActiveArgument)

	templateOffset := uint32(strings.Index(source, " 2)") + 1)
	templateCall, found := TwigVueCallAtOffset(
		root, []byte(source), templateOffset,
	)
	require.True(t, found)
	assert.Equal(t, "format", templateCall.Name)
	assert.Equal(t, 1, templateCall.ActiveArgument)

	filterOffset := uint32(strings.Index(source, "'fallback'") + 2)
	filter, found := TwigVueCallAtOffset(root, []byte(source), filterOffset)
	require.True(t, found)
	assert.Equal(t, "default", filter.Name)
	assert.True(t, filter.Filter)
}

func TestTwigVueCallsReturnsExactArgumentRanges(t *testing.T) {
	source := `<button @click="save(first, nested('x,y'), { tags: ['a', 'b'] }, ` +
		"`label-${format(value, 2)}`); value | default('fallback'); 'fake('\">Save</button>"
	root := twigparser.Parse(source).Tree.Root

	calls := TwigVueCalls(root, []byte(source))
	require.Len(t, calls, 4)
	assert.Equal(t, []string{"save", "nested", "format", "default"}, []string{
		calls[0].Name, calls[1].Name, calls[2].Name, calls[3].Name,
	})
	assert.Equal(t, []string{
		"first",
		"nested('x,y')",
		"{ tags: ['a', 'b'] }",
		"`label-${format(value, 2)}`",
	}, twigVueCallArgumentTexts(source, calls[0]))
	assert.Equal(t, []string{"'x,y'"}, twigVueCallArgumentTexts(
		source, calls[1],
	))
	assert.Equal(t, []string{"value", "2"}, twigVueCallArgumentTexts(
		source, calls[2],
	))
	assert.Equal(t, []string{"'fallback'"}, twigVueCallArgumentTexts(
		source, calls[3],
	))
	assert.True(t, calls[3].Filter)
}

func twigVueCallArgumentTexts(
	source string,
	call TwigVueCallSite,
) []string {
	result := make([]string, 0, len(call.Arguments))
	for _, rangeValue := range call.Arguments {
		result = append(result, source[rangeValue.Start:rangeValue.End])
	}
	return result
}

func TestTwigVueBindingMembersRespectLexicalShadowing(t *testing.T) {
	source := `<div v-for="item in items">
    {{ item.name }} {{ item?.active }} {{ item.name }}
    <span v-for="item in children">{{ item.inner }}</span>
    {{ item.after }}
</div>`
	root := twigparser.Parse(source).Tree.Root
	outerOffset := uint32(strings.Index(source, "item in items") + 1)
	outer, found := TwigVueBindingAtOffset(
		root, []byte(source), outerOffset,
	)
	require.True(t, found)
	members := TwigVueBindingMembers(root, []byte(source), *outer)
	assert.Equal(t, []string{"active", "after", "name"}, vueMemberNames(members))

	nameReferences := TwigVueBindingMemberReferences(
		root, []byte(source), *outer, "name",
	)
	assert.Len(t, nameReferences, 2)
	for _, reference := range nameReferences {
		assert.Equal(t, "name", source[reference.Start:reference.End])
	}
}

func vueMemberNames(members []TwigVueMember) []string {
	result := make([]string, 0, len(members))
	for _, member := range members {
		result = append(result, member.Name)
	}
	return result
}
