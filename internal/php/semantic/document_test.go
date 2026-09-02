package semantic

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func referenceWithTargets(
	reference Reference,
	qualified []string,
	candidates []SymbolID,
) Reference {
	reference.SetQualifiedNames(qualified)
	reference.SetCandidateIDs(candidates)
	return reference
}

func TestReferenceUsesCompactCommonRecord(t *testing.T) {
	t.Parallel()
	require.LessOrEqual(t, unsafe.Sizeof(Reference{}), uintptr(64))
	require.LessOrEqual(t, unsafe.Sizeof(referenceTargets{}), uintptr(32))
	require.LessOrEqual(t, unsafe.Sizeof(referenceTargetExtras{}), uintptr(32))
}

func TestReferenceReleasesEmptyLazyTargets(t *testing.T) {
	t.Parallel()

	var reference Reference
	require.Nil(t, reference.targets)
	reference.SetQualifiedName("App\\Service")
	require.NotNil(t, reference.targets)
	require.Nil(t, reference.targets.extras)
	require.Equal(t, 1, reference.QualifiedNameCount())
	require.Equal(t, "App\\Service", reference.QualifiedNameAt(0))

	reference.AddCandidate("first")
	require.Equal(t, SymbolID("first"), reference.Resolved)
	reference.AddCandidate("second")
	require.Equal(t, []SymbolID{"first", "second"}, reference.CandidateIDs())
	require.NotNil(t, reference.targets.extras)

	reference.ClearCandidateIDs()
	require.Equal(t, []string{"App\\Service"}, reference.QualifiedNames())
	require.NotNil(t, reference.targets)
	reference.SetQualifiedNames(nil)
	require.Nil(t, reference.targets)
}

func TestReferencePromotesMultipleQualifiedNamesToExtras(t *testing.T) {
	t.Parallel()

	var reference Reference
	reference.SetQualifiedNames([]string{"App\\run", "run"})
	require.Equal(t, []string{"App\\run", "run"}, reference.QualifiedNames())
	require.False(t, reference.targets.hasSingleQualified)
	require.NotNil(t, reference.targets.extras)

	reference.SetQualifiedName("App\\Service")
	require.Equal(t, []string{"App\\Service"}, reference.QualifiedNames())
	require.True(t, reference.targets.hasSingleQualified)
	require.Nil(t, reference.targets.extras)
}

func TestReferenceAddCandidateKeepsUniqueTargetInline(t *testing.T) {
	t.Parallel()

	var reference Reference
	reference.AddCandidate("")
	require.Empty(t, reference.Resolved)
	require.Nil(t, reference.CandidateIDs())

	reference.AddCandidate("first")
	require.Equal(t, SymbolID("first"), reference.Resolved)
	require.Nil(t, reference.CandidateIDs())

	reference.AddCandidate("second")
	require.Empty(t, reference.Resolved)
	require.Equal(
		t,
		[]SymbolID{"first", "second"},
		reference.CandidateIDs(),
	)

	reference.AddCandidate("third")
	require.Empty(t, reference.Resolved)
	require.Equal(
		t,
		[]SymbolID{"first", "second", "third"},
		reference.CandidateIDs(),
	)
}
