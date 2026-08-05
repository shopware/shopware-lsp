package admin

import (
	"strings"
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/require"
)

func TestJavaScriptDocumentAnalysisPrecomputesLexicalBindings(t *testing.T) {
	source := `
const utils = Shopware.Utils;
const { chunk: chunkArray } = Shopware.Utils.array;
const services = Application.getContainer('service');
utils.format.date(value);
chunkArray(values, 2);
services.repositoryFactory;
`
	root := javascriptparser.Parse(source).Tree.Root
	analysis := NewJavaScriptDocumentAnalysis(root)
	declarations := analysis.Nodes(jssyntax.JsVariableDeclaration)
	require.Len(t, declarations, 3)

	binding, found := analysis.variableBinding(declarations[1], "chunkArray")
	require.True(t, found)
	require.Equal(t, "chunk", binding.sourceName)
	require.Equal(t, "Shopware.Utils.array", binding.initializer)
	name, initializer, found := analysis.constInitializer(declarations[2])
	require.True(t, found)
	require.Equal(t, "services", name)
	require.Equal(t, "Application.getContainer('service')", initializer)

	for _, member := range analysis.Nodes(jssyntax.JsMemberExpression) {
		text := strings.TrimSpace(member.Text())
		switch {
		case strings.HasSuffix(text, "utils.format.date"):
			receiver, name, matched := analysis.ShopwareUtilsMember(member)
			require.True(t, matched)
			require.Equal(t, []string{"format"}, receiver)
			require.Equal(t, "date", name)
		case strings.HasSuffix(text, "services.repositoryFactory"):
			container, name, matched := analysis.ApplicationContainerMember(member)
			require.True(t, matched)
			require.Equal(t, "service", container)
			require.Equal(t, "repositoryFactory", name)
		}
	}

	calls := analysis.Calls()
	require.Equal(t, jsquery.Calls(root), calls)
}
