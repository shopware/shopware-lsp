package textutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFoldASCIIMatcherFindsAnyPattern(t *testing.T) {
	t.Parallel()

	matcher := NewFoldASCIIMatcher("render", "template", "stream")
	require.True(t, matcher.ContainsString("$this->ReNdErView()"))
	require.True(t, matcher.ContainsBytes([]byte("#[TEMPLATE]")))
	require.True(t, matcher.ContainsString("upstream response"))
	require.False(t, matcher.ContainsString("ordinary application source"))
	require.False(t, matcher.ContainsString("temp\xfflate"))

	overlapping := NewFoldASCIIMatcher("he", "she", "his", "hers")
	require.True(t, overlapping.ContainsString("ushers"))
	require.True(t, overlapping.ContainsString("HISTORY"))

	empty := NewFoldASCIIMatcher("")
	require.True(t, empty.ContainsString(""))
	require.True(t, empty.ContainsBytes(nil))
}

func TestFoldASCIIMatcherDoesNotAllocateWhileScanning(t *testing.T) {
	matcher := NewFoldASCIIMatcher("generate", "redirecttoroute")
	ordinary := []byte("ordinary application source")
	require.Zero(t, testing.AllocsPerRun(1000, func() {
		if !matcher.ContainsString("$this->ReDiReCtToRoUtE('route')") {
			panic("route marker was not found")
		}
		if matcher.ContainsBytes(ordinary) {
			panic("unexpected route marker")
		}
	}))
}
