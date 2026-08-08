package symfony

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPlanServicesXMLConversionIncludesLocalImports(t *testing.T) {
	files := map[string][]byte{
		"/project/config/services.xml": []byte(`<container>
    <imports>
        <import resource="packages/listeners.xml" type="xml"/>
    </imports>
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>`),
		"/project/config/packages/listeners.xml": []byte(`<container>
    <services>
        <service id="App\Listener" class="App\Listener"/>
    </services>
</container>`),
	}

	plan, err := PlanServicesXMLConversion(
		context.Background(),
		"/project/config/services.xml",
		func(_ context.Context, path string) ([]byte, error) {
			content, found := files[path]
			if !found {
				return nil, os.ErrNotExist
			}
			return content, nil
		},
		func(string) (bool, error) { return false, nil },
	)
	require.NoError(t, err)
	require.Len(t, plan, 2)
	require.Equal(t, "/project/config/packages/listeners.xml", plan[0].SourcePath)
	require.Equal(t, "/project/config/packages/listeners.yaml", plan[0].TargetPath)
	require.Equal(t, "/project/config/services.xml", plan[1].SourcePath)
	require.Equal(t, "/project/config/services.yaml", plan[1].TargetPath)
	require.Contains(t, string(plan[1].Content), "resource: packages/listeners.yaml")
	require.Contains(t, string(plan[1].Content), "type: yaml")

	for _, conversion := range plan {
		var parsed map[string]any
		require.NoError(t, yaml.Unmarshal(conversion.Content, &parsed))
	}
}

func TestPlanServicesXMLConversionRefusesExistingTarget(t *testing.T) {
	plan, err := PlanServicesXMLConversion(
		context.Background(),
		"/project/config/services.xml",
		func(_ context.Context, _ string) ([]byte, error) {
			return []byte(`<container/>`), nil
		},
		func(path string) (bool, error) {
			return path == "/project/config/services.yaml", nil
		},
	)
	require.ErrorContains(t, err, "services.yaml exists already")
	require.Empty(t, plan)
}

func TestPlanServicesXMLConversionRefusesLossyInput(t *testing.T) {
	plan, err := PlanServicesXMLConversion(
		context.Background(),
		"/project/config/services.xml",
		func(_ context.Context, _ string) ([]byte, error) {
			return []byte(`<container><services><stack id="app"/></services></container>`), nil
		},
		func(string) (bool, error) { return false, nil },
	)
	require.ErrorContains(t, err, "unsupported element <stack>")
	require.Empty(t, plan)
}
