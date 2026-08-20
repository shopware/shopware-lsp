// Package bytescan provides allocation-free byte searches for parser hot paths.
// Searches return len(source) when no matching byte exists. Normal builds use
// scalar implementations; GOEXPERIMENT=simd selects architecture-specific
// implementations where Go exposes suitable vector operations.
package bytescan
