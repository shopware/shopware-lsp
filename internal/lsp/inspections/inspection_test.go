package inspections

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

type problemCollector struct {
	problems []lsp.Problem
}

func (c *problemCollector) Report(problem lsp.Problem) error {
	c.problems = append(c.problems, problem)
	return nil
}

type staticDocumentResolver map[string]lsp.DocumentSnapshot

func (r staticDocumentResolver) ResolveDocument(
	_ context.Context,
	uri string,
) (lsp.DocumentSnapshot, error) {
	return r[uri], nil
}

func TestAdminTwigPrivilegeInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "privileges.ts"),
		[]byte(`Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")),
		`<mt-button :disabled="acl.can('product.viwer')" />`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t,
		lsp.DiagnosticID("admin.privilege.not-found"),
		problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "product.viewer") {
			updated = candidate
			break
		}
	}
	require.Equal(
		t,
		`<mt-button :disabled="acl.can('product.viewer')" />`,
		updated,
	)
}

func TestAdminTwigBlockInspectionBuildsParentBlockTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	parentTemplate := filepath.Join(adminRoot, "sw-page.html.twig")
	childTemplate := filepath.Join(adminRoot, "acme-page.html.twig")
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-page", Kind: admin.ComponentRegister,
		FilePath:     filepath.Join(adminRoot, "sw-page.js"),
		TemplatePath: parentTemplate,
		Blocks: []admin.TwigBlock{{
			Name: "sw_page_content", FilePath: parentTemplate, Line: 4,
		}},
	}))
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "acme-page", Kind: admin.ComponentExtend,
		TargetComponent: "sw-page", ExtendsComponent: "sw-page",
		FilePath:     filepath.Join(adminRoot, "acme-page.js"),
		TemplatePath: childTemplate,
	}))
	document := lsp.NewTextDocument(
		uriutil.FileURI(childTemplate),
		`{% block sw_page_contnet %}{% endblock %}`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("admin.component.block-not-found"), problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if candidate == `{% block sw_page_content %}{% endblock %}` {
			updated = candidate
			break
		}
	}
	require.Equal(t, `{% block sw_page_content %}{% endblock %}`, updated)
}

func TestAdminPropValueInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-label", FilePath: filepath.Join(root, "sw-label/index.js"),
		Props: []admin.VueComponentProp{{
			Name: "variant", Type: "String",
			AllowedValues:         []string{"primary", "secondary"},
			AllowedValuesComplete: true,
		}},
	}))
	inspection := NewAdmin(adminIndex)
	for _, test := range []struct {
		name, source, expected string
	}{
		{
			"static value", `<sw-label variant="primry" />`,
			`<sw-label variant="primary" />`,
		},
		{
			"bound string literal", `<sw-label :variant="'primry'" />`,
			`<sw-label :variant="'primary'" />`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(
					root, "src/Administration/Resources/app/administration/src/view.html.twig",
				)),
				test.source,
				1,
			)
			collector := &problemCollector{}
			require.NoError(t, inspection.Inspect(
				context.Background(), document, collector,
			))
			require.Len(t, collector.problems, 1)
			problem := collector.problems[0]
			require.Equal(
				t,
				lsp.DiagnosticID("admin.component.invalid-prop-value"),
				problem.ID,
			)
			require.NotEmpty(t, problem.Fixes)

			var updated string
			for _, bound := range problem.Fixes {
				if bound.ID != suggestionFixID {
					continue
				}
				fix := quickFixWithID(t, inspection, bound.ID)
				plan, buildErr := fix.Build(
					context.Background(),
					fixContext(t, document, problem, bound, nil),
				)
				require.NoError(t, buildErr)
				require.Len(t, plan.Documents, 1)
				candidate, applyErr := plan.Documents[0].Apply()
				require.NoError(t, applyErr)
				if candidate == test.expected {
					updated = candidate
					break
				}
			}
			require.Equal(t, test.expected, updated)
		})
	}
}

func TestAdminComponentContractInspectionBuildsNameReplacements(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: filepath.Join(root, "sw-field.ts"),
		Props: []admin.VueComponentProp{
			{Name: "isLoading", Type: "Boolean"},
			{Name: "checked", Type: "Boolean"},
		},
		Events: []admin.VueComponentEvent{
			{Name: "itemClick"}, {Name: "update:checked"},
		},
		Slots: []admin.VueComponentSlot{{Name: "header"}},
	}))
	inspection := NewAdmin(adminIndex)
	for _, test := range []struct {
		name, source, expected string
		code                   lsp.DiagnosticID
	}{
		{
			name:     "bound prop",
			source:   `<sw-field :is-laoding.sync="ready" />`,
			expected: `<sw-field :is-loading.sync="ready" />`,
			code:     "admin.component.unknown-prop",
		},
		{
			name:     "event",
			source:   `<sw-field @item-clik.stop="select" />`,
			expected: `<sw-field @item-click.stop="select" />`,
			code:     "admin.component.unknown-event",
		},
		{
			name:     "named model",
			source:   `<sw-field v-model:cheked.trim="checked" />`,
			expected: `<sw-field v-model:checked.trim="checked" />`,
			code:     "admin.component.unknown-model",
		},
		{
			name:     "named slot",
			source:   `<sw-field><template #heder>Title</template></sw-field>`,
			expected: `<sw-field><template #header>Title</template></sw-field>`,
			code:     "admin.component.unknown-slot",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(
					root, "Resources/app/administration/src/view.html.twig",
				)),
				test.source,
				1,
			)
			collector := &problemCollector{}
			require.NoError(t, inspection.Inspect(
				context.Background(), document, collector,
			))
			require.Len(t, collector.problems, 1)
			problem := collector.problems[0]
			require.Equal(t, test.code, problem.ID)
			require.NotEmpty(t, problem.Fixes)

			var updated string
			for _, bound := range problem.Fixes {
				if bound.ID != suggestionFixID {
					continue
				}
				fix := quickFixWithID(t, inspection, bound.ID)
				plan, buildErr := fix.Build(
					context.Background(),
					fixContext(t, document, problem, bound, nil),
				)
				require.NoError(t, buildErr)
				require.Len(t, plan.Documents, 1)
				candidate, applyErr := plan.Documents[0].Apply()
				require.NoError(t, applyErr)
				if candidate == test.expected {
					updated = candidate
					break
				}
			}
			require.Equal(t, test.expected, updated)
		})
	}
}

func TestAdminDirectiveInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "app/directive/tooltip.directive.ts"),
		[]byte(`Shopware.Directive.register('tooltip', {});`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")),
		`<div v-tooltpi.bottom="message"></div>`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("admin.directive.not-found"), problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "v-tooltip.bottom") {
			updated = candidate
			break
		}
	}
	require.Equal(t, `<div v-tooltip.bottom="message"></div>`, updated)
}

func TestAdminFilterInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "app/filter/currency.ts"),
		[]byte(`Shopware.Filter.register('currency', value => value);`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
		`Shopware.Filter.getByName('currncy');`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(t, lsp.DiagnosticID("admin.filter.not-found"), problem.ID)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "'currency'") {
			updated = candidate
			break
		}
	}
	require.Equal(t, `Shopware.Filter.getByName('currency');`, updated)
}

func TestDALEntityInspectionBuildsTypoReplacement(t *testing.T) {
	dalIndex, err := dal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
		"/project/src/Core/Content/Product/ProductDefinition.php",
		[]byte(`<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/consumer.ts",
		`Shopware.EntityDefinition.get('prodcut');`,
		1,
	)
	inspection := NewDALEntity(dalIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("shopware.dal.entity-not-found"), problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)
	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "'product'") {
			updated = candidate
			break
		}
	}
	require.Equal(t, `Shopware.EntityDefinition.get('product');`, updated)
}

func TestAdminCMSInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "cms.ts"),
		[]byte(`cmsService.registerCmsElement({ name: 'hero' });`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
		`cmsService.getCmsElementConfigByName('herp');`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("admin.cms-element.not-found"), problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)
	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "ByName('hero')") {
			updated = candidate
			break
		}
	}
	require.Equal(
		t, `cmsService.getCmsElementConfigByName('hero');`, updated,
	)
}

func TestAdminComponentInspectionBuildsTagTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "component/sw-button/index.js"),
		[]byte(`Shopware.Component.register('sw-button', {});`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")),
		`<sw-butotn>Save</sw-butotn>`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t,
		lsp.DiagnosticID("admin.component.not-found"),
		problem.ID,
	)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != replaceAdminComponentTagFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, replaceAdminComponentTagFixID)
		plan, err := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, err)
		require.Len(t, plan.Documents, 1)
		updated, err = plan.Documents[0].Apply()
		require.NoError(t, err)
		break
	}
	require.Equal(t, `<sw-button>Save</sw-button>`, updated)
}

func TestAdminTwigSlotMemberInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-inherit-wrapper", FilePath: filepath.Join(root, "wrapper.js"),
		Slots: []admin.VueComponentSlot{{
			Name: "content", MembersComplete: true,
			Members: []admin.VueComponentSlotMember{
				{Name: "currentValue", Type: "string"},
				{Name: "isInherited", Type: "boolean"},
			},
		}},
	}))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "src/Administration/Resources/app/administration/src/view.html.twig",
		)),
		`<sw-inherit-wrapper><template #content="props">{{ props.curentValue }}</template></sw-inherit-wrapper>`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t,
		lsp.DiagnosticID("admin.component.unknown-slot-prop"),
		problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "props.currentValue") {
			updated = candidate
			break
		}
	}
	require.Equal(
		t,
		`<sw-inherit-wrapper><template #content="props">{{ props.currentValue }}</template></sw-inherit-wrapper>`,
		updated,
	)
}

func TestAdminTemplateMemberInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/sw-card.html.twig",
	)
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-card", TemplatePath: templatePath,
		FilePath: filepath.Join(filepath.Dir(templatePath), "index.ts"),
		Members: []admin.VueComponentMember{{
			Name: "getSlots", Kind: admin.ComponentMemberMethod,
		}},
	}))
	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), `{{ getSlos() }}`, 1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t,
		lsp.DiagnosticID("admin.component.unknown-template-member"),
		problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "getSlots") {
			updated = candidate
			break
		}
	}
	require.Equal(t, `{{ getSlots() }}`, updated)
}

func TestAdminModuleRegistryInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "main.ts"),
		[]byte(`Shopware.Module.register('sw-product', { routes: {} });`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.js")),
		`Shopware.Module.getModuleRegistry().get('sw-prduct');`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(t, lsp.DiagnosticID("admin.module.not-found"), problem.ID)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, "sw-product") {
			updated = candidate
			break
		}
	}
	require.Equal(
		t,
		`Shopware.Module.getModuleRegistry().get('sw-product');`,
		updated,
	)
}

func TestYAMLCompatibilityInspectionBuildsValidatedReplacement(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		"class: \"Foo\\Bar\"\n",
		1,
	)
	inspection := NewYAMLCompatibility(&project.Model{
		Dependencies: []project.Package{{
			Name:    "symfony/http-kernel",
			Version: "7.3.0",
		}},
	})
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(t, lsp.DiagnosticID("symfony.yaml.quoted_escape"), problem.ID)
	require.Len(t, problem.Fixes, 1)

	fix := quickFixWithID(t, inspection, problem.Fixes[0].ID)
	plan, err := fix.Build(context.Background(), fixContext(
		t,
		document,
		problem,
		problem.Fixes[0],
		nil,
	))
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Equal(t, "class: \"Foo\\\\Bar\"\n", updated)
	require.Empty(t, lsp.NewTextDocument(document.URI, updated, 2).ParseErrors)
}

func TestServiceArgumentFixRewritesYAMLServiceDefinition(t *testing.T) {
	source := `services:
  app.consumer:
    class: App\Consumer
`
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		source,
		1,
	)
	serviceOffset := uint32(strings.Index(source, "app.consumer"))
	serviceNode := document.SyntaxTree.Root.NodeAtOffset(serviceOffset)
	require.NotNil(t, serviceNode)
	problem := lsp.Problem{
		ID:      "symfony.service.arguments.missing",
		Range:   serviceNode.Range(),
		Element: serviceNode,
		Message: "Missing service arguments",
		Payload: map[string]any{
			"format": "yaml",
			"missingArguments": []map[string]any{
				{"name": "$logger", "suggestedService": "logger"},
				{"name": "$name", "suggestedService": "?"},
			},
		},
	}
	bound := lsp.BindFix(serviceArgumentsFixID, struct{}{})
	plan, err := (serviceArgumentFix{}).Build(
		context.Background(),
		fixContext(t, document, problem, bound, nil),
	)
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Contains(t, updated, "    arguments:\n")
	require.Contains(t, updated, "      - '@logger'\n")
	require.Contains(t, updated, "      - '@?'")
	require.Empty(t, lsp.NewTextDocument(document.URI, updated, 2).ParseErrors)
}

func TestFormClassConstantFixUsesSharedPHPImportRewrite(t *testing.T) {
	source := `<?php
namespace App;

$form->add('enabled', 'checkbox');
`
	document := lsp.NewTextDocument("file:///project/src/Form.php", source, 1)
	strings := phpquery.Nodes(document.SyntaxTree.Root, phpsyntax.PhpString)
	require.Len(t, strings, 2)
	literal := strings[1]
	problem := lsp.Problem{
		ID:      "symfony.form.type.legacy_alias",
		Range:   literal.RangeTrimmedTrivia(),
		Element: literal,
		Message: "Use fully-qualified class name (FQCN)",
	}
	bound := lsp.BindFix(formClassConstantFixID, formClassPayload{
		ClassName: `Symfony\Component\Form\Extension\Core\Type\CheckboxType`,
	})
	plan, err := (formClassConstantFix{}).Build(
		context.Background(),
		fixContext(t, document, problem, bound, nil),
	)
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Equal(t, `<?php
namespace App;

use Symfony\Component\Form\Extension\Core\Type\CheckboxType;

$form->add('enabled', CheckboxType::class);
`, updated)
	require.Empty(t, lsp.NewTextDocument(document.URI, updated, 2).ParseErrors)
}

func TestPHPMethodFixResolvesIndexedClassAndBuildsCrossFileEdit(t *testing.T) {
	projectRoot := t.TempDir()
	targetPath := filepath.Join(projectRoot, "src", "ProductController.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), 0o755))
	targetSource := `<?php
namespace App;

class ProductController
{
}
`
	require.NoError(t, os.WriteFile(targetPath, []byte(targetSource), 0o600))

	phpIndex, err := php.NewPHPIndex(filepath.Join(t.TempDir(), "php-index"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		targetPath,
		[]byte(targetSource),
	)))

	routeSource := "controller: App\\ProductController::show\n"
	routeDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(projectRoot, "config", "routes.yaml")),
		routeSource,
		4,
	)
	anchorRange := cst.TextRange{
		Start: uint32(strings.Index(routeSource, "App\\ProductController")),
		End:   uint32(strings.Index(routeSource, "::show")),
	}
	anchor := routeDocument.SyntaxTree.Root.DescendantForRange(anchorRange)
	require.NotNil(t, anchor)
	problem := lsp.Problem{
		ID:      "symfony.controller.method.missing",
		Range:   anchorRange,
		Element: anchor,
		Message: "Controller method is missing",
	}
	bound := lsp.BindFix(controllerMethodFixID, phpMethodPayload{
		ClassName:  `App\ProductController`,
		MethodName: "show",
		Parameters: []string{"id", "$slug"},
	})
	targetURI := uriutil.FileURI(targetPath)
	targetVersion := 8
	targetDocument := lsp.NewTextDocument(targetURI, targetSource, targetVersion)
	resolver := staticDocumentResolver{
		targetURI: {
			Document: targetDocument,
			Version:  &targetVersion,
		},
	}
	fix := phpMethodFix{
		id:          controllerMethodFixID,
		titlePrefix: "controller",
		phpIndex:    phpIndex,
	}
	plan, err := fix.Build(
		context.Background(),
		fixContext(t, routeDocument, problem, bound, resolver),
	)
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	require.Equal(t, targetURI, plan.Documents[0].URI)
	require.Equal(t, &targetVersion, plan.Documents[0].Version)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Contains(t, updated, "    public function show($id, $slug)\n")
	require.Contains(t, updated, "    {\n    }\n}")
	require.Empty(t, lsp.NewTextDocument(targetURI, updated, targetVersion+1).ParseErrors)

	// The target document can change independently from the diagnostic's
	// source document. Recheck the current target CST before inserting so a
	// lazy action never creates a duplicate method from an older index entry.
	changedVersion := targetVersion + 1
	resolver[targetURI] = lsp.DocumentSnapshot{
		Document: lsp.NewTextDocument(targetURI, updated, changedVersion),
		Version:  &changedVersion,
	}
	_, err = fix.Build(
		context.Background(),
		fixContext(t, routeDocument, problem, bound, resolver),
	)
	require.ErrorContains(t, err, "already exists")
}

func TestTemplateInspectionCreatesAValidatedFilePlan(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(t.TempDir(), "twig-index"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "page.html.twig")),
		"{% include 'missing/card.html.twig' %}\n",
		1,
	)
	inspection := NewTemplate(root, twigIndex, nil)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(t, lsp.DiagnosticID("twig.template.missing"), problem.ID)
	require.Len(t, problem.Fixes, 1)
	require.Equal(t, createTemplateFixID, problem.Fixes[0].ID)

	fix := quickFixWithID(t, inspection, createTemplateFixID)
	plan, err := fix.Build(
		context.Background(),
		fixContext(t, document, problem, problem.Fixes[0], nil),
	)
	require.NoError(t, err)
	require.Empty(t, plan.Documents)
	require.Len(t, plan.Creates, 1)
	require.Equal(
		t,
		uriutil.FileURI(filepath.Join(root, "templates", "missing", "card.html.twig")),
		plan.Creates[0].URI,
	)
	_, err = os.Stat(filepath.Join(root, "templates", "missing", "card.html.twig"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestInvokableCommandInspectionRewritesReturnTypeFromTheCST(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(filepath.Join(t.TempDir(), "php-index"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	document := lsp.NewTextDocument(
		"file:///project/src/Command.php",
		`<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class Command
{
    public function __invoke(): string
    {
        return 'failure';
    }
}
`,
		1,
	)
	inspection := NewInvokableCommand(phpIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.NotEmpty(t, collector.problems)
	problem := collector.problems[0]
	require.Equal(
		t,
		lsp.DiagnosticID("symfony.console.invoke.return_type"),
		problem.ID,
	)
	require.Len(t, problem.Fixes, 1)
	require.Empty(t, problem.Payload)

	fix := quickFixWithID(t, inspection, invokableReturnTypeFixID)
	plan, err := fix.Build(
		context.Background(),
		fixContext(t, document, problem, problem.Fixes[0], nil),
	)
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Contains(t, updated, "public function __invoke(): int")
	require.Empty(t, lsp.NewTextDocument(document.URI, updated, 2).ParseErrors)
}

func TestAdminApplicationContainerInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "global.types.ts"),
		[]byte(`
export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
}`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
		`Application.getContainer('factory').loacle;`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("admin.application-container.unknown-member"),
		problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, ".locale") {
			updated = candidate
			break
		}
	}
	require.Equal(
		t, `Application.getContainer('factory').locale;`, updated,
	)
}

func TestAdminShopwareContextInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "app/composables/use-context.ts"),
		[]byte(`export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string };
}`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
		`Shopware.Context.api.langaugeId;`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("admin.shopware-context.unknown-member"), problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, ".languageId") {
			updated = candidate
			break
		}
	}
	require.Equal(t, `Shopware.Context.api.languageId;`, updated)
}

func TestAdminShopwareUtilsInspectionBuildsTypoReplacement(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "core/service/util.service.ts"),
		[]byte(`export default { createId };
function createId(): string { return 'id'; }`),
	)))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
		`const utils = Shopware.Utils; utils.creatId();`,
		1,
	)
	inspection := NewAdmin(adminIndex)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(), document, collector,
	))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Equal(
		t, lsp.DiagnosticID("admin.shopware-utils.unknown-member"), problem.ID,
	)
	require.NotEmpty(t, problem.Fixes)

	var updated string
	for _, bound := range problem.Fixes {
		if bound.ID != suggestionFixID {
			continue
		}
		fix := quickFixWithID(t, inspection, bound.ID)
		plan, buildErr := fix.Build(
			context.Background(),
			fixContext(t, document, problem, bound, nil),
		)
		require.NoError(t, buildErr)
		require.Len(t, plan.Documents, 1)
		candidate, applyErr := plan.Documents[0].Apply()
		require.NoError(t, applyErr)
		if strings.Contains(candidate, ".createId") {
			updated = candidate
			break
		}
	}
	require.Equal(t, `const utils = Shopware.Utils; utils.createId();`, updated)
}

func quickFixWithID(
	t *testing.T,
	inspection lsp.Inspection,
	id lsp.FixID,
) lsp.RewriteQuickFix {
	t.Helper()
	for _, fix := range inspection.QuickFixes() {
		if fix.ID() == id {
			rewriteFix, ok := fix.(lsp.RewriteQuickFix)
			require.True(t, ok, "quick fix %q is not a rewrite", id)
			return rewriteFix
		}
	}
	t.Fatalf("quick fix %q was not registered", id)
	return nil
}

func fixContext(
	t *testing.T,
	document *lsp.TextDocument,
	problem lsp.Problem,
	bound lsp.BoundFix,
	documents lsp.DocumentResolver,
) lsp.FixContext {
	t.Helper()
	handle, err := rewrite.NewElementHandle(
		document.URI,
		document.Version,
		document.SyntaxLanguage,
		problem.Element,
	)
	require.NoError(t, err)
	return lsp.FixContext{
		Document: document,
		Diagnostic: protocol.Diagnostic{
			Range:   wireRange(document.LineIndex, problem.Range),
			Code:    string(problem.ID),
			Message: problem.Message,
		},
		Anchor:         handle,
		ProblemPayload: rawJSON(t, problem.Payload),
		FixPayload:     rawJSON(t, bound.Payload),
		Documents:      documents,
	}
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func wireRange(index *cst.LineIndex, rng cst.TextRange) protocol.Range {
	startLine, startCharacter := index.PositionUTF16(rng.Start)
	endLine, endCharacter := index.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{Line: int(startLine), Character: int(startCharacter)},
		End:   protocol.Position{Line: int(endLine), Character: int(endCharacter)},
	}
}
