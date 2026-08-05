package admin

import (
	"strings"
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseJS(t *testing.T, code string) *jssyntax.Node {
	t.Helper()
	result := javascriptparser.Parse(code)
	require.NotNil(t, result.Tree.Root)
	return result.Tree.Root
}

func TestParseComponentDefinition_Props(t *testing.T) {
	code := `
export default {
    props: {
        /**
         * @description Text shown in the card header.
         * It may contain a translated label.
         * @type {String}
         * @required true
         */
        title: {
            type: String,
            required: true,
        },
        count: {
            type: Number,
            default: 0,
        },
        active: Boolean,
    },
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	require.Len(t, def.Props, 3)

	assert.Equal(t, "title", def.Props[0].Name)
	assert.Equal(t, "String", def.Props[0].Type)
	assert.Equal(
		t, "Text shown in the card header.\nIt may contain a translated label.",
		def.Props[0].Documentation,
	)
	assert.True(t, def.Props[0].Required)
	titleStart := uint32(strings.Index(code, "title:"))
	startLine, startCharacter := jssyntax.NewLineIndex(code).PositionUTF16(
		titleStart,
	)
	assert.Equal(t, int(startLine), def.Props[0].NameRange.StartLine)
	assert.Equal(t, int(startCharacter), def.Props[0].NameRange.StartCharacter)
	assert.Equal(
		t, int(startCharacter)+len("title"),
		def.Props[0].NameRange.EndCharacter,
	)
	assert.True(t, def.Props[0].NameRange.Declaration)
	assert.True(t, def.Props[0].NameRange.Identifier)

	assert.Equal(t, "count", def.Props[1].Name)
	assert.Equal(t, "Number", def.Props[1].Type)
	assert.False(t, def.Props[1].Required)
	assert.Equal(t, "0", def.Props[1].Default)

	assert.Equal(t, "active", def.Props[2].Name)
	assert.Equal(t, "Boolean", def.Props[2].Type)
}

func TestJavaScriptDocumentationIgnoresContractMetadata(t *testing.T) {
	code := `export default {
    props: {
        /** Plain description. */
        plain: String,
        /**
         * @deprecated Use modern instead.
         * @type {String}
         */
        legacy: String,
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Props, 2)
	assert.Equal(t, "Plain description.", definition.Props[0].Documentation)
	assert.Empty(t, definition.Props[1].Documentation)
	assert.Equal(t, "Use modern instead.", definition.Props[1].Deprecated)
}

func TestJavaScriptDocumentationOnArrayStyleProp(t *testing.T) {
	code := `export default {
    props: [
        /** Label displayed by the component. */
        'label',
    ],
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Props, 1)
	assert.Equal(t, "label", definition.Props[0].Name)
	assert.Equal(
		t, "Label displayed by the component.",
		definition.Props[0].Documentation,
	)
}

func TestJavaScriptEventAnnotationsEnrichExplicitContract(t *testing.T) {
	code := `/**
 * @event save { Product: product } Fired after the product was saved.
 * @event closeModal (void)
 */
export default {
    emits: ['save', 'modal-close'],
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Events, 2)
	save, found := componentDefinitionEvent(definition.Events, "save")
	require.True(t, found)
	assert.Equal(t, "Product", save.Type)
	assert.Equal(t, "Fired after the product was saved.", save.Documentation)
	assert.True(t, save.NameRange.Declaration)
	assert.NotZero(t, save.Line)
	_, found = componentDefinitionEvent(definition.Events, "close-modal")
	assert.False(t, found, "an explicit emits declaration rejects stale annotations")
	_, found = componentDefinitionEvent(definition.Events, "modal-close")
	assert.True(t, found)
}

func TestJavaScriptEventAnnotationsProvideConstantEmitFallback(t *testing.T) {
	code := `/**
 * @event media-upload-add { UploadTask[]: data }
 * @event media-upload-fail UploadTask UploadTask
 */
export default {
    methods: {
        notify(payload) { this.$emit(UploadEvents.UPLOAD_ADDED, payload); },
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	for name, eventType := range map[string]string{
		"media-upload-add":  "UploadTask[]",
		"media-upload-fail": "UploadTask",
	} {
		event, found := componentDefinitionEvent(definition.Events, name)
		require.True(t, found, name)
		assert.Equal(t, eventType, event.Type, name)
		assert.True(t, event.NameRange.Declaration, name)
	}
}

func TestEventDeclarationDocumentation(t *testing.T) {
	code := `export default {
    emits: {
        /** Fired after validation succeeds. */
        valid: null,
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	event, found := componentDefinitionEvent(definition.Events, "valid")
	require.True(t, found)
	assert.Equal(t, "Fired after validation succeeds.", event.Documentation)
}

func TestParseComponentDefinition_PropAllowedValues(t *testing.T) {
	code := `
export default {
    props: {
        variant: {
            type: String,
            default: '',
            validValues: ['primary', 'secondary'],
        },
        runtimeValues: {
            type: String,
            validValues: ['known', importedValue],
        },
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Props, 2)

	values, complete := VuePropAllowedValues(definition.Props[0])
	assert.True(t, complete)
	assert.Equal(t, []string{"", "primary", "secondary"}, values)
	values, complete = VuePropAllowedValues(definition.Props[1])
	assert.False(t, complete)
	assert.Equal(t, []string{"known"}, values)
}

func TestParseComponentDefinition_PropValidatorAllowedValues(t *testing.T) {
	code := `
export default {
    props: {
        navigationType: {
            type: String,
            default: 'arrow',
            validator(value) {
                return ['arrow', 'button', 'all'].includes(value);
            },
        },
        arrowStyle: {
            type: String,
            validator: (candidate) => ['inside', 'outside', 'none'].includes(candidate),
        },
        dynamicValues: {
            type: String,
            validator(value) {
                return importedValues.includes(value);
            },
        },
        conditionalValues: {
            type: String,
            validator(value) {
                return ['known'].includes(value) || isExtensionValue(value);
            },
        },
        mixedValues: {
            validator(value) {
                return ['known', importedValue].includes(value);
            },
        },
        mode: {
            validator(value) {
                return ['view', 'edit', 'create'].includes(value);
            },
        },
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Props, 6)

	values, complete := VuePropAllowedValues(definition.Props[0])
	assert.True(t, complete)
	assert.Equal(t, []string{"arrow", "button", "all"}, values)
	values, complete = VuePropAllowedValues(definition.Props[1])
	assert.True(t, complete)
	assert.Equal(t, []string{"inside", "outside", "none"}, values)
	for _, prop := range definition.Props[2:4] {
		values, complete = VuePropAllowedValues(prop)
		assert.False(t, complete, prop.Name)
		assert.Empty(t, values, prop.Name)
	}
	values, complete = VuePropAllowedValues(definition.Props[4])
	assert.False(t, complete)
	assert.Equal(t, []string{"known"}, values)
	assert.Empty(t, definition.Props[4].Type)
	assert.Equal(t, "String", definition.Props[5].Type)
	values, complete = VuePropAllowedValues(definition.Props[5])
	assert.True(t, complete)
	assert.Equal(t, []string{"view", "edit", "create"}, values)
}

func TestParseComponentDefinition_PropValidatorEmptyStringGuard(t *testing.T) {
	code := `
export default {
    props: {
        variant: {
            type: String,
            validator(value) {
                if (!value.length) {
                    return true;
                }
                return ['primary', 'secondary'].includes(value);
            },
        },
        arrowVariant: {
            type: String,
            validator: (candidate) => {
                if (!candidate.length) {
                    return true;
                }
                return ['inside', 'outside'].includes(candidate);
            },
        },
        untypedVariant: {
            validator(value) {
                if (!value.length) {
                    return true;
                }
                return ['known'].includes(value);
            },
        },
        compoundGuard: {
            type: String,
            validator(value) {
                if (!value.length || isExtensionValue(value)) {
                    return true;
                }
                return ['known'].includes(value);
            },
        },
        guardedSideEffect: {
            type: String,
            validator(value) {
                if (!value.length) {
                    observe(value);
                    return true;
                }
                return ['known'].includes(value);
            },
        },
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Props, 5)

	for _, index := range []int{0, 1} {
		values, complete := VuePropAllowedValues(definition.Props[index])
		assert.True(t, complete, definition.Props[index].Name)
		assert.Contains(t, values, "", definition.Props[index].Name)
	}
	values, _ := VuePropAllowedValues(definition.Props[0])
	assert.ElementsMatch(t, []string{"primary", "secondary", ""}, values)
	values, _ = VuePropAllowedValues(definition.Props[1])
	assert.ElementsMatch(t, []string{"inside", "outside", ""}, values)
	for _, prop := range definition.Props[2:] {
		values, complete := VuePropAllowedValues(prop)
		assert.False(t, complete, prop.Name)
		assert.Empty(t, values, prop.Name)
	}
}

func TestParseComponentDefinition_PropValidatorLocalConstValues(t *testing.T) {
	code := `
export default {
    props: {
        variant: {
            type: String,
            validator(value: string) {
                const variants = ['gallery', 'detail', 'listing'];
                return variants.includes(value);
            },
        },
        arrowVariant: {
            type: String,
            validator: (candidate) => {
                const variants = ['small', 'large'];
                return variants.includes(candidate);
            },
        },
        inferredVariant: {
            validator(value) {
                const variants = ['view', 'edit'];
                return variants.includes(value);
            },
        },
        guardedVariant: {
            type: String,
            validator(value) {
                if (!value.length) {
                    return true;
                }
                const variants = ['primary', 'secondary'];
                return variants.includes(value);
            },
        },
        mixedVariant: {
            validator(value) {
                const variants = ['known', importedValue];
                return variants.includes(value);
            },
        },
        letVariant: {
            type: String,
            validator(value) {
                let variants = ['known'];
                return variants.includes(value);
            },
        },
        varVariant: {
            type: String,
            validator(value) {
                var variants = ['known'];
                return variants.includes(value);
            },
        },
        importedVariant: {
            type: String,
            validator(value) {
                const variants = importedVariants;
                return variants.includes(value);
            },
        },
        reassignedVariant: {
            type: String,
            validator(value) {
                const variants = ['known'];
                variants = importedVariants;
                return variants.includes(value);
            },
        },
        mutatedVariant: {
            type: String,
            validator(value) {
                const variants = ['known'];
                variants.push('extension');
                return variants.includes(value);
            },
        },
        wrongReceiver: {
            type: String,
            validator(value) {
                const variants = ['known'];
                return importedVariants.includes(value);
            },
        },
        compoundReturn: {
            type: String,
            validator(value) {
                const variants = ['known'];
                return variants.includes(value) || isExtensionValue(value);
            },
        },
    },
};`
	definition := ParseComponentDefinition(parseJS(t, code), []byte(code))
	require.Len(t, definition.Props, 12)

	values, complete := VuePropAllowedValues(definition.Props[0])
	assert.True(t, complete)
	assert.Equal(t, []string{"gallery", "detail", "listing"}, values)
	values, complete = VuePropAllowedValues(definition.Props[1])
	assert.True(t, complete)
	assert.Equal(t, []string{"small", "large"}, values)
	values, complete = VuePropAllowedValues(definition.Props[2])
	assert.True(t, complete)
	assert.Equal(t, []string{"view", "edit"}, values)
	assert.Equal(t, "String", definition.Props[2].Type)
	values, complete = VuePropAllowedValues(definition.Props[3])
	assert.True(t, complete)
	assert.ElementsMatch(t, []string{"primary", "secondary", ""}, values)
	values, complete = VuePropAllowedValues(definition.Props[4])
	assert.False(t, complete)
	assert.Equal(t, []string{"known"}, values)
	assert.Empty(t, definition.Props[4].Type)
	for _, prop := range definition.Props[5:] {
		values, complete = VuePropAllowedValues(prop)
		assert.False(t, complete, prop.Name)
		assert.Empty(t, values, prop.Name)
	}
}

func TestParseComponentDefinition_PreservesTypeScriptPropTypes(t *testing.T) {
	code := `
export default defineComponent({
    props: {
        editLink: {
            type: Object as PropType<RouteLocationNamedRaw | null>,
            required: false,
            default: null,
        },
        values: {
            type: Array as unknown as PropType<Array<Record<string, number>>>,
            required: true,
        },
    },
});`
	root := parseJS(t, code)
	definition := ParseComponentDefinition(root, []byte(code))

	require.Len(t, definition.Props, 2)
	assert.Equal(
		t,
		"Object as PropType<RouteLocationNamedRaw | null>",
		definition.Props[0].Type,
	)
	assert.Equal(t, "null", definition.Props[0].Default)
	assert.Equal(
		t,
		"Array as unknown as PropType<Array<Record<string, number>>>",
		definition.Props[1].Type,
	)
	assert.True(t, definition.Props[1].Required)
}

func TestParseComponentDefinition_PropsArray(t *testing.T) {
	code := `
export default {
    props: ['title', 'count', 'active'],
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	require.Len(t, def.Props, 3)
	assert.Equal(t, "title", def.Props[0].Name)
	assert.Equal(t, "count", def.Props[1].Name)
	assert.Equal(t, "active", def.Props[2].Name)
	assert.False(t, def.Props[0].NameRange.Identifier)
	assert.True(t, def.Props[0].NameRange.Declaration)
	assert.Equal(
		t, len("title"),
		def.Props[0].NameRange.EndCharacter-
			def.Props[0].NameRange.StartCharacter,
	)
	line := strings.Split(code, "\n")[def.Props[0].NameRange.StartLine]
	assert.Equal(
		t, "title",
		line[def.Props[0].NameRange.StartCharacter:def.Props[0].NameRange.EndCharacter],
	)
}

func TestParseComponentDefinition_LegacyModelContract(t *testing.T) {
	code := `
export default {
    model: { prop: 'selection', event: 'selection-change' },
    props: { selection: { type: Array, required: true } },
    emits: ['selection-change'],
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	assert.Equal(t, "selection", definition.ModelProp)
	assert.Equal(t, "selection-change", definition.ModelEvent)

	code = `export default { model: {}, props: ['value'], emits: ['input'] };`
	definition = ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	assert.Equal(t, "value", definition.ModelProp)
	assert.Equal(t, "input", definition.ModelEvent)
}

func TestParseComponentDefinition_DefineComponentWithTypedMethods(t *testing.T) {
	code := `
export default defineComponent({
    props: {
        title: { type: String, required: true },
    },
    data(): CardData {
        return { isLoading: false };
    },
    computed: {
        label(): string {
            return this.title;
        },
    },
    methods: {
        async save(value: string): Promise<void> {
            if (value) {
                await this.repository.save(value);
            }
        },
        reset(): void {
            this.isLoading = false;
        },
    },
});`
	root := parseJS(t, code)
	definition := ParseComponentDefinitionWithLineIndex(
		root,
		jssyntax.NewLineIndex(code),
	)

	require.Len(t, definition.Props, 1)
	assert.Equal(t, "title", definition.Props[0].Name)
	assert.True(t, definition.Props[0].Required)
	assert.Equal(t, []string{"isLoading"}, definition.Data)
	assert.Equal(t, []string{"label"}, definition.Computed)
	assert.Equal(t, []string{"save", "reset"}, definition.Methods)
}

func TestParseComponentDefinitionSetupReturnMembers(t *testing.T) {
	code := `
export default Shopware.Component.wrapComponentConfig({
    setup() {
        const count = ref<number>(0);
        const rawTitle: Ref<string> = ref('Fallback');
        const products = computed((): Product[] => Shopware.Store.get('product').items);
        const summary = computed(() => ({ total: count.value }));
        const save = async (id: string): Promise<void> => persist(id);
        function reset(force: boolean): boolean { return force; }
		const options = { handler: () => true };
        const hidden = ref(false);
        onMounted(() => {
            const nested = ref('not public');
            return nested;
        });
        return {
            count,
            title: rawTitle,
            products,
            summary,
            save,
            reset,
			options,
        };
    },
});`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)

	members := make(map[string]VueComponentMember)
	for _, member := range definition.Members {
		members[member.Name] = member
	}
	require.Len(t, members, 7)
	assert.Equal(t, ComponentMemberData, members["count"].Kind)
	assert.Equal(t, "number", members["count"].Type)
	assert.Equal(t, "count", members["count"].BindingName)
	assert.True(t, members["count"].Shorthand)
	assert.Greater(
		t,
		members["count"].NameRange.EndCharacter,
		members["count"].NameRange.StartCharacter,
	)
	assert.Equal(t, ComponentMemberData, members["title"].Kind)
	assert.Equal(t, "string", members["title"].Type)
	assert.Equal(t, "rawTitle", members["title"].BindingName)
	assert.False(t, members["title"].Shorthand)
	assert.Greater(
		t,
		members["title"].NameRange.EndCharacter,
		members["title"].NameRange.StartCharacter,
	)
	assert.Equal(t, ComponentMemberComputed, members["products"].Kind)
	assert.Equal(t, "Product[]", members["products"].Type)
	assert.Equal(t, ComponentMemberComputed, members["summary"].Kind)
	assert.Equal(t, "Object", members["summary"].Type)
	assert.Equal(t, ComponentMemberMethod, members["save"].Kind)
	assert.Equal(t, "(id: string) => Promise<void>", members["save"].Type)
	assert.Equal(t, ComponentMemberMethod, members["reset"].Kind)
	assert.Equal(t, "(force: boolean) => boolean", members["reset"].Type)
	assert.Equal(t, ComponentMemberData, members["options"].Kind)
	assert.Equal(t, "Object", members["options"].Type)
	assert.NotContains(t, members, "hidden")
	assert.NotContains(t, members, "nested")
}

func TestParseComponentDefinitionSetupMergesStaticReturnBranches(t *testing.T) {
	code := `export default defineComponent({
	setup() {
		const template = computed(() => 'ready');
		if (disabled) return { template: null };
		return { template };
	},
});`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)

	require.Len(t, definition.Members, 1)
	assert.Equal(t, "template", definition.Members[0].Name)
	assert.Equal(t, ComponentMemberComputed, definition.Members[0].Kind)
	assert.Equal(t, "string", definition.Members[0].Type)
}

func TestParseComponentDefinitionExpressionSetup(t *testing.T) {
	code := `export default defineComponent({
	setup: () => ({ ready: true, label: 'Ready' }),
});`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)

	require.Len(t, definition.Members, 2)
	assert.Equal(t, "ready", definition.Members[0].Name)
	assert.Equal(t, "boolean", definition.Members[0].Type)
	assert.Equal(t, "label", definition.Members[1].Name)
	assert.Equal(t, "string", definition.Members[1].Type)
}

func TestParseComponentDefinitionIgnoresDynamicSetupReturnObjects(t *testing.T) {
	code := `export default defineComponent({
	setup: (props, context) => createExtendableSetup(
		{ props, context, name: 'sw-card' },
		() => ({ internalState: ref(false) }),
	),
});`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	assert.Empty(t, definition.Members)

	code = `export default defineComponent({
		setup() { return createState({ internalState: true }); },
	});`
	definition = ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	assert.Empty(t, definition.Members)
}

func TestParseComponentDefinitionRetainsDirectInstanceAssignments(t *testing.T) {
	code := `export default {
	data() { return { mediaItems: [], selectedMedia: null }; },
	methods: {
		async load() {
			this.mediaItems = await this.mediaRepository.search(this.criteria);
			if (this.selectedId) {
				this.selectedMedia = await this.mediaRepository.get(this.selectedId);
			}
			this.mediaItems = [];
			this.selectedMedia.name = 'ignored';
			this.page += 1;
			this.ready === true;
		},
	},
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)

	require.Len(t, definition.Assignments, 3)
	assert.Equal(t, VueComponentAssignment{
		Target:     "mediaItems",
		Expression: "await this.mediaRepository.search(this.criteria)",
		Line:       5,
	}, definition.Assignments[0])
	assert.Equal(t, "selectedMedia", definition.Assignments[1].Target)
	assert.Equal(
		t, "await this.mediaRepository.get(this.selectedId)",
		definition.Assignments[1].Expression,
	)
	assert.Equal(t, "[]", definition.Assignments[2].Expression)
}

func TestComponentAssignmentResolvesVisibleConstInitializer(t *testing.T) {
	code := `export default {
	data() {
		return { items: [], nestedItems: [], afterItems: [], mutableItems: [] };
	},
	methods: {
		async load() {
			const result = await this.mediaRepository.search(this.criteria);
			this.items = result;
			if (this.includeFolders) {
				const result = await this.folderRepository.search(this.criteria);
				this.nestedItems = result;
			}
			this.afterItems = result;
			let mutable = await this.productRepository.search(this.criteria);
			this.mutableItems = mutable;
		},
		other() {
			this.unrelatedItems = result;
		},
	},
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	expressions := make(map[string]string)
	for _, assignment := range definition.Assignments {
		expressions[assignment.Target] = assignment.Expression
	}

	assert.Equal(
		t, "await this.mediaRepository.search(this.criteria)",
		expressions["items"],
	)
	assert.Equal(
		t, "await this.folderRepository.search(this.criteria)",
		expressions["nestedItems"],
	)
	assert.Equal(
		t, "await this.mediaRepository.search(this.criteria)",
		expressions["afterItems"],
	)
	assert.Equal(t, "mutable", expressions["mutableItems"])
	assert.Equal(t, "result", expressions["unrelatedItems"])
}

func TestComponentAssignmentResolvesConstInsideTryFinally(t *testing.T) {
	code := `export default {
	data() { return { items: null }; },
	computed: {
		newsletterRecipientRepository() {
			return this.repositoryFactory.create('newsletter_recipient');
		},
	},
	methods: {
		async getList() {
			if (this.adminEsEnable) {
				this.criteria.setTerm(this.term);
			} else {
				this.criteria = await this.addQueryScores(this.term, this.criteria);
			}
			try {
				const searchResult = await this.newsletterRecipientRepository.search(this.criteria);
				this.items = searchResult;
				this.total = searchResult.total;
			} finally {
				this.isLoading = false;
			}
		},
	},
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	var items VueComponentAssignment
	for _, assignment := range definition.Assignments {
		if assignment.Target == "items" {
			items = assignment
			break
		}
	}
	assert.Equal(
		t, "await this.newsletterRecipientRepository.search(this.criteria)",
		items.Expression,
	)
}

func TestComponentAssignmentResolvesPromiseThenParameter(t *testing.T) {
	code := `export default {
	data() { return { items: null, selected: null }; },
	methods: {
		load() {
			this.repository.search(this.criteria).then((result: SearchResult) => {
				this.items = result;
			});
			this.repository.get(this.id).then(entity => {
				this.selected = entity;
			});
			this.repository.search(this.criteria).then(
				() => {},
				(error) => { this.failure = error; },
			);
			this.rows.map((row) => { this.currentRow = row; });
			this.repository.get(this.id).then(function (entity) {
				this.functionSelected = entity;
			});
		},
	},
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	expressions := make(map[string]string)
	for _, assignment := range definition.Assignments {
		expressions[assignment.Target] = assignment.Expression
	}

	assert.Equal(
		t, "await this.repository.search(this.criteria)", expressions["items"],
	)
	assert.Equal(
		t, "await this.repository.get(this.id)", expressions["selected"],
	)
	assert.Equal(t, "error", expressions["failure"])
	assert.Equal(t, "row", expressions["currentRow"])
	assert.Equal(t, "entity", expressions["functionSelected"])
}

func TestStaticCallbackCallReceiver(t *testing.T) {
	root := parseJS(t, `promise?.then((value) => value);`)
	calls := jsquery.Calls(root)
	require.NotEmpty(t, calls)
	var thenCall *jssyntax.Node
	for _, call := range calls {
		if jsquery.CallMethodName(call) == "then" {
			thenCall = call
			break
		}
	}
	require.NotNil(t, thenCall)
	receiver, found := staticCallbackCallReceiver(thenCall, "then")
	require.True(t, found)
	assert.Equal(t, "promise", receiver)
}

func TestDirectComponentConstInitializer(t *testing.T) {
	name, expression, found := directComponentConstInitializer(
		`const selected: Entity<'media'> | null = await this.repository.get(id);`,
	)
	require.True(t, found)
	assert.Equal(t, "selected", name)
	assert.Equal(t, "await this.repository.get(id)", expression)

	_, _, found = directComponentConstInitializer(`let selected = value;`)
	assert.False(t, found)
	_, _, found = directComponentConstInitializer(`const first = one, second = two;`)
	assert.False(t, found)
}

func TestParseComponentDefinitionAfterInlineDataReturnType(t *testing.T) {
	code := `
export default defineComponent({
    data(): {
        rows: Row[];
        isLoading: boolean;
    } {
        return { rows: [], isLoading: false };
    },
    computed: {
        visibleRows(): Row[] { return this.rows; },
    },
});`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)

	assert.ElementsMatch(t, []string{"rows", "isLoading"}, definition.Data)
	assert.Equal(t, []string{"visibleRows"}, definition.Computed)
	member, found := func() (VueComponentMember, bool) {
		for _, candidate := range definition.Members {
			if candidate.Name == "visibleRows" {
				return candidate, true
			}
		}
		return VueComponentMember{}, false
	}()
	require.True(t, found)
	assert.Equal(t, "Row[]", member.Type)
	memberTypes := make(map[string]string)
	for _, candidate := range definition.Members {
		memberTypes[candidate.Name] = candidate.Type
	}
	assert.Equal(t, "Row[]", memberTypes["rows"])
	assert.Equal(t, "boolean", memberTypes["isLoading"])
}

func TestParseComponentDefinition_Emits(t *testing.T) {
	code := `
export default {
    emits: ['filter-reset', 'update:modelValue', 'close'],
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	require.Len(t, def.Emits, 3)
	assert.Equal(t, "filter-reset", def.Emits[0])
	assert.Equal(t, "update:modelValue", def.Emits[1])
	assert.Equal(t, "close", def.Emits[2])
	require.Len(t, def.Events, 3)
	assert.Equal(t, "filter-reset", def.Events[0].Name)
	assert.Equal(t, 3, def.Events[0].Line)
	assert.True(t, def.Events[0].NameRange.Declaration)
	assert.False(t, def.Events[0].NameRange.Identifier)
	line := strings.Split(code, "\n")[def.Events[0].NameRange.StartLine]
	assert.Equal(
		t, "filter-reset",
		line[def.Events[0].NameRange.StartCharacter:def.Events[0].NameRange.EndCharacter],
	)
	assert.Equal(t, "update:model-value", CanonicalEventName(def.Events[1].Name))
}

func TestParseComponentDefinitionSourceAwareEvents(t *testing.T) {
	code := `export default {
    emits: {
        save(payload) { return Boolean(payload); },
        'update:modelValue': null,
    },
    methods: {
        close() { this.$emit('close'); },
        update(context) { context.emit('updated'); },
        dynamic(name) { this.$emit(name); },
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code),
		jssyntax.NewLineIndex(code),
	)
	require.Equal(
		t,
		[]string{"save", "update:modelValue", "close", "updated"},
		definition.Emits,
	)
	require.Len(t, definition.Events, 4)
	assert.Equal(t, 3, definition.Events[0].Line)
	assert.Equal(t, 4, definition.Events[1].Line)
	assert.Equal(t, 7, definition.Events[2].Line)
	assert.Equal(t, 8, definition.Events[3].Line)
	for index, expected := range []struct {
		name       string
		identifier bool
	}{
		{"save", true},
		{"update:modelValue", false},
		{"close", false},
		{"updated", false},
	} {
		event := definition.Events[index]
		assert.True(t, event.NameRange.Declaration, event.Name)
		assert.Equal(t, expected.identifier, event.NameRange.Identifier, event.Name)
		line := strings.Split(code, "\n")[event.NameRange.StartLine]
		assert.Equal(
			t, expected.name,
			line[event.NameRange.StartCharacter:event.NameRange.EndCharacter],
			event.Name,
		)
	}
}

func TestParseComponentDefinition_Methods(t *testing.T) {
	code := `
export default {
    methods: {
        resetFilter() {
            this.$emit('filter-reset');
        },
        handleClick() {
            console.log('clicked');
        },
    },
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	require.Len(t, def.Methods, 2)
	assert.Equal(t, "resetFilter", def.Methods[0])
	assert.Equal(t, "handleClick", def.Methods[1])
}

func TestParseComponentDefinition_Computed(t *testing.T) {
	code := `
export default {
    computed: {
        fullName() {
            return this.firstName + ' ' + this.lastName;
        },
        isActive() {
            return this.status === 'active';
        },
    },
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	require.Len(t, def.Computed, 2)
	assert.Equal(t, "fullName", def.Computed[0])
	assert.Equal(t, "isActive", def.Computed[1])
}

func TestParseComponentDefinition_TemplateScope(t *testing.T) {
	code := `
export default {
    inject: ['repositoryFactory', 'acl'],
    mixins: [Mixin.getByName('notification')],
    data() {
        return {
            isLoading: false,
            selectedIds: [],
        };
    },
    computed: {
        repository() { return this.repositoryFactory.create('product'); },
    },
    methods: {
        save() {},
    },
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	assert.ElementsMatch(t, []string{"repositoryFactory", "acl"}, def.Injected)
	assert.Equal(t, []string{"notification"}, def.Mixins)
	assert.ElementsMatch(t, []string{"isLoading", "selectedIds"}, def.Data)
	require.Len(t, def.Members, 6)
	for _, member := range def.Members {
		assert.NotZero(t, member.Line, member.Name)
	}
}

func TestParseComponentDefinition_LocalDirectives(t *testing.T) {
	code := `
const hide = {};
export default {
    directives: {
        hide,
        focus: {},
        'drag-target': {},
        myDirective: {},
    },
};
`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	require.Len(t, definition.LocalDirectives, 4)
	assert.Equal(t, "hide", definition.LocalDirectives[0].Name)
	assert.True(t, definition.LocalDirectives[0].Shorthand)
	assert.Equal(t, "focus", definition.LocalDirectives[1].Name)
	assert.False(t, definition.LocalDirectives[1].Shorthand)
	assert.Equal(t, "drag-target", definition.LocalDirectives[2].Name)
	assert.True(t, definition.LocalDirectives[2].Quoted)
	assert.Equal(t, "my-directive", definition.LocalDirectives[3].Name)
	for _, directive := range definition.LocalDirectives {
		assert.NotZero(t, directive.Line, directive.Name)
		assert.Less(
			t, directive.NameRange.StartCharacter,
			directive.NameRange.EndCharacter, directive.Name,
		)
	}
}

func TestParseComponentDefinition_TemplateImport(t *testing.T) {
	code := `
import template from './sw-base-filter.html.twig';
import './sw-base-filter.scss';

export default {
    template,
    props: {},
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	assert.Equal(t, "./sw-base-filter.html.twig", def.TemplatePath)
	assert.True(t, def.HasTemplate)
}

func TestParseComponentDefinition_Full(t *testing.T) {
	code := `
import template from './sw-base-filter.html.twig';
import './sw-base-filter.scss';

export default {
    template,

    emits: ['filter-reset'],

    props: {
        title: {
            type: String,
            required: true,
        },
        showResetButton: {
            type: Boolean,
            required: true,
        },
        active: {
            type: Boolean,
            required: true,
        },
    },

    computed: {
        isVisible() {
            return this.active && this.showResetButton;
        },
    },

    methods: {
        resetFilter() {
            this.$emit('filter-reset');
        },
    },
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	assert.Equal(t, "./sw-base-filter.html.twig", def.TemplatePath)
	assert.True(t, def.HasTemplate)

	require.Len(t, def.Emits, 1)
	assert.Equal(t, "filter-reset", def.Emits[0])

	require.Len(t, def.Props, 3)
	assert.Equal(t, "title", def.Props[0].Name)
	assert.Equal(t, "showResetButton", def.Props[1].Name)
	assert.Equal(t, "active", def.Props[2].Name)

	require.Len(t, def.Computed, 1)
	assert.Equal(t, "isVisible", def.Computed[0])

	require.Len(t, def.Methods, 1)
	assert.Equal(t, "resetFilter", def.Methods[0])
}

func TestParseComponentDefinition_NoExportDefault(t *testing.T) {
	code := `
const component = {
    props: {
        title: String,
    },
};
`
	root := parseJS(t, code)
	def := ParseComponentDefinition(root, []byte(code))

	// Should return empty definition when no export default
	assert.Empty(t, def.Props)
	assert.Empty(t, def.Methods)
}
