package symfony

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseXMLTwigGlobals(t *testing.T) {
	source := `<container>
  <services>
    <service id="twig">
      <call method="addGlobal">
        <argument>app</argument>
        <argument type="service" id="App\Twig\AppVariable"/>
      </call>
      <call method="addGlobal">
        <argument>site_name</argument>
        <argument>Shop</argument>
      </call>
    </service>
  </services>
</container>`
	globals := ParseXMLTwigGlobals(
		"/project/var/cache/Container.xml",
		[]byte(source),
	)
	require.Len(t, globals, 2)
	require.Equal(t, "app", globals[0].Name)
	require.Equal(t, "App\\Twig\\AppVariable", globals[0].ServiceID)
	require.Equal(
		t,
		"app",
		source[globals[0].Range.Start:globals[0].Range.End],
	)
	require.Equal(t, "site_name", globals[1].Name)
	require.Equal(t, "Shop", globals[1].Value)
	require.Equal(
		t,
		strings.Index(source, "site_name"),
		int(globals[1].Range.Start),
	)
}
