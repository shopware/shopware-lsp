package indexer

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/require"
)

var benchmarkMemoizedValue any

func TestParsedFileReusesSyntaxAndLineIndex(t *testing.T) {
	file := NewParsedFile(
		"/project/Resources/config/services.xml",
		[]byte("<container>\n<services/>\n</container>"),
	)

	const workers = 32
	trees := make([]*cst.Tree, workers)
	lineIndexes := make([]*cst.LineIndex, workers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)

	for i := range workers {
		go func() {
			defer waitGroup.Done()
			trees[i] = file.SyntaxTree()
			lineIndexes[i] = file.LineIndex()
		}()
	}
	waitGroup.Wait()

	require.NotNil(t, trees[0])
	require.NotNil(t, lineIndexes[0])
	for i := 1; i < workers; i++ {
		require.Same(t, trees[0], trees[i])
		require.Same(t, lineIndexes[0], lineIndexes[i])
	}
}

func TestParsedFileUnsupportedExtensionDoesNotParse(t *testing.T) {
	file := NewParsedFile("/project/README.md", []byte("# Project"))

	require.Nil(t, file.SyntaxTree())
	require.Equal(t, ".md", file.Extension())
	require.NotNil(t, file.LineIndex())
}

func TestParsedFileMemoizesDerivedValuesConcurrently(t *testing.T) {
	file := NewParsedFile("/project/src/Example.php", []byte("<?php"))
	key := &struct{}{}
	var computations atomic.Int32

	const workers = 32
	values := make([]any, workers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for i := range workers {
		go func() {
			defer waitGroup.Done()
			values[i] = file.Memoized(key, func() any {
				return &struct{ generation int32 }{
					generation: computations.Add(1),
				}
			})
		}()
	}
	waitGroup.Wait()

	require.EqualValues(t, 1, computations.Load())
	for i := 1; i < workers; i++ {
		require.Same(t, values[0], values[i])
	}

	file.clearMemoized()
	recomputed := file.Memoized(key, func() any {
		return &struct{ generation int32 }{
			generation: computations.Add(1),
		}
	})
	require.EqualValues(t, 2, computations.Load())
	require.NotSame(t, values[0], recomputed)
}

func TestParsedFileMemoizesMultipleDerivedValues(t *testing.T) {
	file := NewParsedFile("/project/src/Example.php", []byte("<?php"))
	firstKey := &struct{ id int }{id: 1}
	secondKey := &struct{ id int }{id: 2}
	var firstComputations atomic.Int32
	var secondComputations atomic.Int32

	first := file.Memoized(firstKey, func() any {
		firstComputations.Add(1)
		return &struct{ key string }{key: "first"}
	})
	second := file.Memoized(secondKey, func() any {
		secondComputations.Add(1)
		return &struct{ key string }{key: "second"}
	})

	require.Same(t, first, file.Memoized(firstKey, func() any {
		t.Fatal("first memoized value was recomputed")
		return nil
	}))
	require.Same(t, second, file.Memoized(secondKey, func() any {
		t.Fatal("second memoized value was recomputed")
		return nil
	}))
	require.EqualValues(t, 1, firstComputations.Load())
	require.EqualValues(t, 1, secondComputations.Load())

	file.clearMemoized()
	require.NotSame(t, first, file.Memoized(firstKey, func() any {
		firstComputations.Add(1)
		return &struct{ key string }{key: "first after clear"}
	}))
	require.NotSame(t, second, file.Memoized(secondKey, func() any {
		secondComputations.Add(1)
		return &struct{ key string }{key: "second after clear"}
	}))
	require.EqualValues(t, 2, firstComputations.Load())
	require.EqualValues(t, 2, secondComputations.Load())
}

func TestParsedFileMemoizedHitDoesNotAllocate(t *testing.T) {
	file := NewParsedFile("/project/example.php", []byte("<?php"))
	key := &struct{}{}
	expected := &struct{}{}
	require.Same(t, expected, file.Memoized(key, func() any {
		return expected
	}))

	allocations := testing.AllocsPerRun(100, func() {
		actual := file.Memoized(key, func() any {
			t.Fatal("memoized value was recomputed")
			return nil
		})
		if actual != expected {
			t.Fatal("memoized value changed")
		}
	})
	require.Zero(t, allocations)
}

func BenchmarkParsedFileMemoizedSingleKey(b *testing.B) {
	key := &struct{}{}
	expected := &struct{}{}
	b.ReportAllocs()
	for b.Loop() {
		file := ParsedFile{}
		benchmarkMemoizedValue = file.Memoized(key, func() any {
			return expected
		})
		file.clearMemoized()
	}
}
