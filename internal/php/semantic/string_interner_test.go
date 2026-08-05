package semantic

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceStringInternerCanonicalizesAcrossRepresentations(
	t *testing.T,
) {
	t.Parallel()

	interner := newWorkspaceStringInterner(2)
	first := strings.Clone("App\\Service")
	second := strings.Clone("App\\Service")
	canonical := interner.Intern(first)
	require.Equal(t, canonical, interner.Intern(second))
	require.Equal(
		t,
		unsafe.StringData(canonical),
		unsafe.StringData(interner.Intern(second)),
	)

	fromBytes, found := interner.LookupBytes([]byte("App\\Service"))
	require.True(t, found)
	require.Equal(t, unsafe.StringData(canonical), unsafe.StringData(fromBytes))
}

func TestWorkspaceStringInternerGrowsWithoutLosingValues(t *testing.T) {
	t.Parallel()

	const count = 10_000
	interner := newWorkspaceStringInterner(1)
	values := make([]string, count)
	for index := range count {
		values[index] = "App\\Service" + strconv.Itoa(index)
		interner.Intern(values[index])
	}
	require.Equal(t, count, interner.count)
	for _, value := range values {
		actual, found := interner.LookupBytes([]byte(value))
		require.True(t, found)
		require.Equal(t, value, actual)
	}
}

func TestWorkspaceStringInternerCopiesOnlyNewValues(t *testing.T) {
	t.Parallel()

	interner := newWorkspaceStringInterner(0)
	copies := 0
	copyValue := func(value string) string {
		copies++
		return strings.Clone(value)
	}
	first := interner.InternCopy("App\\Service", copyValue)
	second := interner.InternCopy(strings.Clone("App\\Service"), copyValue)
	require.Equal(t, first, second)
	require.Equal(t, 1, copies)
	require.Equal(t, unsafe.StringData(first), unsafe.StringData(second))
}

func TestWorkspaceStringInternerExistingLookupDoesNotAllocate(t *testing.T) {
	interner := newWorkspaceStringInterner(1)
	interner.Intern("App\\Service")
	value := []byte("App\\Service")

	allocations := testing.AllocsPerRun(100, func() {
		actual, found := interner.LookupBytes(value)
		if !found || actual != "App\\Service" {
			t.Fatalf("unexpected lookup result %q, %t", actual, found)
		}
	})
	require.Zero(t, allocations)
}

func TestWorkspaceStringInternerDefersCapacityUntilFirstValue(t *testing.T) {
	t.Parallel()

	interner := newWorkspaceStringInterner(700_000)
	require.Empty(t, interner.entries)
	interner.Intern("App\\Service")
	require.Len(t, interner.entries, workspaceStringTableSize(700_000))
}

func TestWorkspaceStringTableSizeReservesAtMostSeventyFivePercent(
	t *testing.T,
) {
	t.Parallel()

	for _, expected := range []int{1, 6, 7, 700_000} {
		size := workspaceStringTableSize(expected)
		require.GreaterOrEqual(t, size*workspaceStringTableLoadPercent, expected*100)
		require.Zero(t, size&(size-1))
	}
	require.Equal(
		t,
		workspaceStringTableSize(workspaceStringTableMaximumHint),
		workspaceStringTableSize(int(^uint(0)>>1)),
	)
}
