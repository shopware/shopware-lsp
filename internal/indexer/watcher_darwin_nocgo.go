//go:build darwin && !cgo

package indexer

import "fmt"

type platformWatcherState struct{}

func (fs *FileScanner) StartWatcher() error {
	return fmt.Errorf("macOS file watching requires cgo for FSEvents")
}

func (fs *FileScanner) StopWatcher() {}
