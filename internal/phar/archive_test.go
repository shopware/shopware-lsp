package phar

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAndExtract(t *testing.T) {
	t.Parallel()

	path := writeTestArchive(t, []testEntry{
		{name: "src/Plain.php", content: []byte("<?php class Plain {}")},
		{
			name:        "src/Compressed.php",
			content:     []byte("<?php class Compressed {}"),
			compression: compressionGzip,
		},
		{name: "empty.php"},
	})

	archive, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archive.Close()) })
	require.Equal(t, path, archive.Path())
	require.Len(t, archive.Entries(), 3)

	for index, expected := range []string{
		"<?php class Plain {}",
		"<?php class Compressed {}",
		"",
	} {
		var content bytes.Buffer
		require.NoError(t, archive.Extract(archive.Entries()[index], &content))
		require.Equal(t, expected, content.String())
	}
}

func TestOpenSupportsCommonStubEndings(t *testing.T) {
	t.Parallel()

	for _, ending := range []string{"", " ?>", " ?>\n", " ?>\r\n"} {
		ending := ending
		t.Run(ending, func(t *testing.T) {
			t.Parallel()
			path := writeTestArchiveWithEnding(
				t,
				[]testEntry{{name: "test.php", content: []byte("<?php")}},
				ending,
			)
			archive, err := Open(path)
			require.NoError(t, err)
			require.NoError(t, archive.Close())
		})
	}
}

func TestOpenRejectsWrapperAndMalformedArchive(t *testing.T) {
	t.Parallel()

	wrapper := filepath.Join(t.TempDir(), "wrapper.phar")
	require.NoError(t, os.WriteFile(wrapper, []byte("<?php echo 'wrapper';"), 0o600))
	_, err := Open(wrapper)
	require.ErrorIs(t, err, ErrNotArchive)

	malformed := filepath.Join(t.TempDir(), "malformed.phar")
	require.NoError(t, os.WriteFile(
		malformed,
		append([]byte("<?php __HALT_COMPILER();"), 0xff, 0xff, 0xff, 0x7f),
		0o600,
	))
	_, err = Open(malformed)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrNotArchive))
}

func TestExtractRejectsCorruptContent(t *testing.T) {
	t.Parallel()

	path := writeTestArchive(t, []testEntry{{
		name:    "test.php",
		content: []byte("<?php echo 1;"),
	}})
	archiveBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	archiveBytes[len(archiveBytes)-1] ^= 0xff
	require.NoError(t, os.WriteFile(path, archiveBytes, 0o600))

	archive, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archive.Close()) })
	require.ErrorContains(
		t,
		archive.Extract(archive.Entries()[0], io.Discard),
		"CRC32 mismatch",
	)
}

func TestExternalArchive(t *testing.T) {
	path := os.Getenv("SHOPWARE_LSP_TEST_PHAR")
	if path == "" {
		t.Skip("SHOPWARE_LSP_TEST_PHAR is not set")
	}

	archive, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archive.Close()) })
	require.NotEmpty(t, archive.Entries())
	for _, entry := range archive.Entries() {
		if filepath.Ext(entry.Name) != ".php" {
			continue
		}
		var content bytes.Buffer
		require.NoError(t, archive.Extract(entry, &content))
		require.Contains(t, content.String(), "<?php")
		return
	}
	t.Fatal("archive does not contain a PHP file")
}

type testEntry struct {
	name        string
	content     []byte
	compression uint32
}

func writeTestArchive(t *testing.T, entries []testEntry) string {
	t.Helper()
	return writeTestArchiveWithEnding(t, entries, " ?>\r\n")
}

func writeTestArchiveWithEnding(
	t *testing.T,
	entries []testEntry,
	stubEnding string,
) string {
	t.Helper()

	var entryManifest bytes.Buffer
	var contents bytes.Buffer
	for _, entry := range entries {
		compressed := entry.content
		if entry.compression == compressionGzip {
			var buffer bytes.Buffer
			writer, err := flate.NewWriter(&buffer, flate.DefaultCompression)
			require.NoError(t, err)
			_, err = writer.Write(entry.content)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			compressed = buffer.Bytes()
		}
		writeUint32(t, &entryManifest, uint32(len(entry.name)))
		_, err := entryManifest.WriteString(entry.name)
		require.NoError(t, err)
		writeUint32(t, &entryManifest, uint32(len(entry.content)))
		writeUint32(t, &entryManifest, 1_700_000_000)
		writeUint32(t, &entryManifest, uint32(len(compressed)))
		writeUint32(t, &entryManifest, crc32.ChecksumIEEE(entry.content))
		writeUint32(t, &entryManifest, 0o100644|entry.compression)
		writeUint32(t, &entryManifest, 0)
		_, err = contents.Write(compressed)
		require.NoError(t, err)
	}

	var manifest bytes.Buffer
	writeUint32(t, &manifest, uint32(len(entries)))
	require.NoError(t, binary.Write(&manifest, binary.LittleEndian, uint16(0x0011)))
	writeUint32(t, &manifest, 0)
	writeUint32(t, &manifest, 0)
	writeUint32(t, &manifest, 0)
	_, err := manifest.Write(entryManifest.Bytes())
	require.NoError(t, err)

	var archive bytes.Buffer
	_, err = archive.WriteString(
		"<?php // fixture\n__HALT_COMPILER();" + stubEnding,
	)
	require.NoError(t, err)
	writeUint32(t, &archive, uint32(manifest.Len()))
	_, err = archive.Write(manifest.Bytes())
	require.NoError(t, err)
	_, err = archive.Write(contents.Bytes())
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "fixture.phar")
	require.NoError(t, os.WriteFile(path, archive.Bytes(), 0o600))
	return path
}

func writeUint32(t *testing.T, writer io.Writer, value uint32) {
	t.Helper()
	require.NoError(t, binary.Write(writer, binary.LittleEndian, value))
}
