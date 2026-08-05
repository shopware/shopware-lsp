package uriutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileURIRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folder with spaces", "file.php")
	uri := FileURI(path)
	roundTrip, err := Path(uri)
	require.NoError(t, err)
	require.Equal(t, path, roundTrip)
}

func TestPathRejectsNonFileURI(t *testing.T) {
	_, err := Path("https://example.com/file.php")
	require.Error(t, err)
}
