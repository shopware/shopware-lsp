package semantic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSymbolIDCaseFoldsASCIIAndUnicode(t *testing.T) {
	t.Parallel()
	require.Equal(
		t,
		NewSymbolID(ClassSymbol, "app\\product", "/product.php", 0),
		NewSymbolID(ClassSymbol, "\\APP\\Product", "/product.php", 0),
	)
	require.Equal(
		t,
		NewSymbolID(ClassSymbol, "äpp\\größe", "/unicode.php", 0),
		NewSymbolID(ClassSymbol, "\\ÄPP\\GRÖẞE", "/unicode.php", 0),
	)
	require.Equal(
		t,
		NewSymbolID(ClassSymbol, "App\\Product", "/first.php", 1),
		NewSymbolID(ClassSymbol, "App\\Product", "/other/long/path.php", 99),
	)
	require.NotEqual(
		t,
		NewSymbolID(LocalSymbol, "", "/first.php", 1),
		NewSymbolID(LocalSymbol, "", "/first.php", 2),
	)
}
