package admin

import (
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentDefinitionRetainsStateAndMethodTypes(t *testing.T) {
	code := `
export default defineComponent({
    props: {
        fallbackProducts: { type: Array as PropType<Product[]> },
    },
		data() {
		return {
            /** @type {Product[]} */
            products: [],
            rows: [] as Array<{ id: string; label: string }>,
            count: 0,
			options: { active: true, label: 'All' },
			rowsWithRuntimeState: [] as Array<{ id: string }>,
        };
    },
    computed: {
        visibleProducts(): Product[] { return this.products; },
        fallback() { return this.fallbackProducts; },
        isEmpty() { return this.count === 0; },
    },
	methods: {
        save(product: Product): Promise<void> { return repository.save(product); },
		reset() { this.count = 0; },
		markRows() {
			this.rowsWithRuntimeState.forEach((row) => {
				row.deletable = Boolean(row.id);
			});
		},
    },
});`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	types := make(map[string]string)
	for _, member := range definition.Members {
		types[string(member.Kind)+":"+member.Name] = member.Type
	}
	assert.Equal(t, "Product[]", types["data:products"])
	assert.Equal(
		t, "Array<{ id: string; label: string }>", types["data:rows"],
	)
	assert.Equal(t, "number", types["data:count"])
	assert.Equal(
		t, "{ active: boolean; label: string }", types["data:options"],
	)
	assert.Equal(t, "Product[]", types["computed:visibleProducts"])
	assert.Equal(
		t, "Array as PropType<Product[]>", types["computed:fallback"],
	)
	assert.Equal(t, "boolean", types["computed:isEmpty"])
	assert.Equal(
		t, "(product: Product) => Promise<void>", types["method:save"],
	)
	assert.Equal(t, "() => unknown", types["method:reset"])

	byName := make(map[string]VueComponentMember)
	for _, member := range definition.Members {
		byName[member.Name] = member
	}
	assert.Equal(t, "this.products", byName["visibleProducts"].SourceExpression)
	assert.Equal(t, "repository.save(product)", byName["save"].SourceExpression)
	assert.True(t, byName["options"].OpenRuntimeShape)
	assert.False(t, byName["rows"].OpenRuntimeShape)
	assert.False(t, byName["products"].OpenRuntimeShape)
	require.Len(t, byName["rowsWithRuntimeState"].ElementMembers, 1)
	assert.Equal(
		t,
		VueComponentElementMember{Name: "deletable", Type: "boolean", Line: 26},
		byName["rowsWithRuntimeState"].ElementMembers[0],
	)
}

func TestVueTypeMembersExtractInlineIterableShape(t *testing.T) {
	members := VueTypeMembers(`{ id: string; label?: string; count: number }`)
	require.Len(t, members, 3)
	byName := make(map[string]TwigVueMember)
	for _, member := range members {
		byName[member.Name] = member
	}
	assert.Equal(t, "string", byName["id"].Type)
	assert.Equal(t, "string", byName["label"].Type)
	assert.Equal(t, "number", byName["count"].Type)
}

func TestVueCallableReturnType(t *testing.T) {
	assert.Equal(
		t, "Entity<'product'> | null",
		VueCallableReturnType("(id: string) => Entity<'product'> | null"),
	)
	assert.Equal(
		t, "EntityCollection<'product'>",
		VueCallableReturnType("(): EntityCollection<'product'>"),
	)
	assert.Empty(t, VueCallableReturnType("Entity<'product'>"))
	assert.Empty(t, VueCallableReturnType("Function"))
}

func TestVueCallableSignature(t *testing.T) {
	parameters, returnType, found := VueCallableSignature(
		"(value: string, options?: { locale: string, formats: string[] }, " +
			"callback?: (result: boolean) => void) => Promise<boolean>",
	)
	require.True(t, found)
	assert.Equal(t, []string{
		"value: string",
		"options?: { locale: string, formats: string[] }",
		"callback?: (result: boolean) => void",
	}, parameters)
	assert.Equal(t, "Promise<boolean>", returnType)

	parameters, returnType, found = VueCallableSignature(
		"(value: string, retries?: number): boolean",
	)
	require.True(t, found)
	assert.Equal(t, []string{"value: string", "retries?: number"}, parameters)
	assert.Equal(t, "boolean", returnType)

	_, _, found = VueCallableSignature("Function")
	assert.False(t, found)
}

func TestVuePropValueTypeAndProvableCompatibility(t *testing.T) {
	assert.Equal(t, "number", VuePropValueType("Number"))
	assert.Equal(
		t, "ReadonlyArray<Row>",
		VuePropValueType("Array as unknown as PropType<ReadonlyArray<Row>>"),
	)
	assert.Equal(
		t, "string | number", VuePropValueType("[String, Number]"),
	)
	assert.Equal(
		t, "TextEditorLinkMenuConfig",
		VuePropValueType("Object as () => TextEditorLinkMenuConfig"),
	)

	for _, pair := range [][2]string{
		{"Number", "string"},
		{"String", "boolean"},
		{"Array as PropType<Row[]>", "string"},
		{"Object as PropType<Row>", "Array<string>"},
		{"Function", "number"},
		{"string | number", "boolean"},
	} {
		assert.True(
			t, VueTypesProvablyIncompatible(pair[0], pair[1]),
			"%s <- %s", pair[0], pair[1],
		)
	}
	for _, pair := range [][2]string{
		{"Number", "number"},
		{"String", "string | null"},
		{"Function", "() => Entity<'customer_address'>[]"},
		{"Array as PropType<Row[]>", "Array<Product>"},
		{"Object as PropType<Row>", "{ name: string }"},
		{"Object as () => TextEditorLinkMenuConfig", "{ title: string }"},
		{"Identifier", "string"},
		{"Number", "unknown"},
		{"string | object", "{ name: string; params: { id: string } }"},
		{"string | number", "number"},
	} {
		assert.False(
			t, VueTypesProvablyIncompatible(pair[0], pair[1]),
			"%s <- %s", pair[0], pair[1],
		)
	}
}

func TestVueTypeAllowsUndefined(t *testing.T) {
	for _, value := range []string{
		"undefined", "void", "string | undefined", "(void) | number",
	} {
		assert.True(t, VueTypeAllowsUndefined(value), value)
	}
	for _, value := range []string{
		"", "unknown", "null", "string", "{ value: undefined }",
	} {
		assert.False(t, VueTypeAllowsUndefined(value), value)
	}
}

func TestVueModelExpressionAssignable(t *testing.T) {
	for _, expression := range []string{
		"enabled",
		"product.active",
		"items[index].name",
		"config[name][locale]",
		"order.addresses.get(order.billingAddressId).phoneNumber",
	} {
		assert.True(t, VueModelExpressionAssignable(expression), expression)
	}
	for _, expression := range []string{
		"true",
		"getValue()",
		"product?.active",
		"enabled && visible",
		"{ value: enabled }",
	} {
		assert.False(t, VueModelExpressionAssignable(expression), expression)
	}
}

func TestVueExpressionTextTypeRespectsNestedComparisonsAndTernaries(t *testing.T) {
	assert.Equal(
		t, "string",
		vueExpressionTextType(`active === true ? 'active' : 'inactive'`, nil),
	)
	assert.Equal(
		t, "Array",
		vueExpressionTextType(`active ? [first] : []`, nil),
	)
	assert.Equal(
		t, "boolean",
		vueExpressionTextType(`left.value === right.value`, nil),
	)
	assert.Equal(t, "boolean", vueExpressionTextType(`!disabled`, nil))
	assert.Equal(
		t, "Function", vueExpressionTextType(`(item) => !item.disabled`, nil),
	)
	assert.Empty(
		t,
		vueExpressionTextType(`items.find((item) => item.id === selectedId)`, nil),
	)
}

func TestComponentReturnInferenceIgnoresNestedCallbackReturns(t *testing.T) {
	code := `
export default {
    computed: {
        selected() {
            return this.items.find((item) => {
                return item.id === this.selectedId;
            });
        },
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	require.Len(t, definition.Members, 1)
	assert.Equal(t, "selected", definition.Members[0].Name)
	assert.Empty(t, definition.Members[0].Type)
	assert.Contains(
		t, definition.Members[0].SourceExpression, "this.items.find",
	)
}

func TestComponentGetterReturnInferenceIgnoresBranchAndCallbackReturns(t *testing.T) {
	code := `
export default {
    computed: {
        selected: {
            get() {
                const found = this.items.find((item) => item.id === this.selectedId);
                if (found) {
                    return [found];
                }
                return [{ id: this.selectedId }];
            },
            set(value) { this.$emit('change', value); },
        },
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	require.Len(t, definition.Members, 1)
	assert.Equal(t, "selected", definition.Members[0].Name)
	assert.Equal(t, "Array", definition.Members[0].Type)
}

func TestComponentReturnInferenceStaysOpenWhenAnyBranchIsUnknown(t *testing.T) {
	code := `
export default {
    methods: {
        getOptions(id) {
            if (this.options[id]) {
                return this.options[id];
            }
            return false;
        },
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	require.Len(t, definition.Members, 1)
	assert.Equal(t, "(id) => unknown", definition.Members[0].Type)
	assert.Empty(t, definition.Members[0].SourceExpression)
}

func TestComponentMembersRetainExhaustiveReturnAlternatives(t *testing.T) {
	code := `
export default {
    computed: {
        elementType() { return this.disabled ? 'span' : 'router-link'; },
        selectedForm() {
            if (this.contact) { return 'sw-contact'; }
            return this.runtimeForm;
        },
        inputComponent() {
            switch (this.fieldType) {
                case 'uuid': return 'sw-entity-multi-id-select';
                case 'float':
                case 'int': return 'sw-number-field';
                default: return 'sw-text-field';
            }
        },
        incomplete() {
            if (this.enabled) { return 'sw-card'; }
        },
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	byName := make(map[string]VueComponentMember)
	for _, member := range definition.Members {
		byName[member.Name] = member
	}
	require.Equal(t, []string{
		"this.disabled ? 'span' : 'router-link'",
	}, byName["elementType"].ReturnExpressions)
	assert.True(t, byName["elementType"].ReturnsComplete)
	require.Equal(t, []string{
		"'sw-contact'", "this.runtimeForm",
	}, byName["selectedForm"].ReturnExpressions)
	assert.True(t, byName["selectedForm"].ReturnsComplete)
	require.Equal(t, []string{
		"'sw-entity-multi-id-select'", "'sw-number-field'", "'sw-text-field'",
	}, byName["inputComponent"].ReturnExpressions)
	assert.True(t, byName["inputComponent"].ReturnsComplete)
	require.Equal(t, []string{"'sw-card'"}, byName["incomplete"].ReturnExpressions)
	assert.False(t, byName["incomplete"].ReturnsComplete)
}

func TestVueMethodReturnsCompleteControlFlow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		complete bool
	}{
		{name: "top-level return", body: `return value;`, complete: true},
		{name: "semicolonless return", body: `return value`, complete: true},
		{
			name: "statement after return",
			body: `return value; consume(value);`,
		},
		{name: "nested return", body: `if (value) { return value; }`},
		{
			name: "complete fallthrough switch",
			body: `switch (value) {
                case 'a':
                case 'b': return first;
                default: throw failure;
            }`,
			complete: true,
		},
		{
			name: "switch missing default",
			body: `switch (value) { case 'a': return first; }`,
		},
		{
			name: "switch breaks",
			body: `switch (value) {
                case 'a': break;
                default: return fallback;
            }`,
		},
		{
			name: "statement after switch",
			body: `switch (value) { default: return fallback; }
                consume(value);`,
		},
		{
			name: "last switch wins",
			body: `switch (first) { default: break; }
                switch (second) {
                    case 'a': return first;
                    default: return fallback;
                }`,
			complete: true,
		},
		{
			name: "final return supersedes switch",
			body: `switch (value) { default: break; }
                return fallback;`,
			complete: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := javascriptparser.Parse(
				"export default { value() {" + test.body + "} };",
			).Tree.Root
			methods := jsquery.Nodes(root, jssyntax.JsMethod)
			require.Len(t, methods, 1)
			assert.Equal(t, test.complete, vueMethodReturnsComplete(methods[0]))
		})
	}
}

func BenchmarkVueMethodReturnAnalysis(b *testing.B) {
	root := javascriptparser.Parse(`
export default {
    computed: {
        inputComponent() {
            switch (this.fieldType) {
                case 'uuid': return 'sw-entity-multi-id-select';
                case 'float':
                case 'int': return 'sw-number-field';
                default: return 'sw-text-field';
            }
        },
    },
};`).Tree.Root
	methods := jsquery.Nodes(root, jssyntax.JsMethod)
	if len(methods) != 1 {
		b.Fatalf("expected one method, got %d", len(methods))
	}
	method := methods[0]
	b.Run("expressions", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := vueMethodReturnExpressions(method)
			if len(result) != 3 {
				b.Fatalf("expected three return expressions, got %d", len(result))
			}
		}
	})
	b.Run("completeness", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if !vueMethodReturnsComplete(method) {
				b.Fatal("expected a complete switch")
			}
		}
	})
}

func TestComputedReturnInferenceStaysOpenAcrossAttributeFallback(t *testing.T) {
	code := `
export default {
    props: { resizeWidth: { type: Boolean } },
    computed: {
		computedMatchReferenceWidth() {
			if ('matchReferenceWidth' in this.$attrs || 'match-reference-width' in this.$attrs) {
				return this.$attrs.matchReferenceWidth ?? this.$attrs['match-reference-width'];
			}
			// Fallback to deprecated prop
			return this.resizeWidth;
        },
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	var member VueComponentMember
	for _, candidate := range definition.Members {
		if candidate.Name == "computedMatchReferenceWidth" {
			member = candidate
			break
		}
	}
	assert.Empty(t, member.Type)
	assert.Empty(t, member.SourceExpression)
}
