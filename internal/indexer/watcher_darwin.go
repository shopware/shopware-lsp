//go:build darwin && cgo

package indexer

/*
#cgo LDFLAGS: -framework CoreServices -framework CoreFoundation
#include <CoreServices/CoreServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
#include <stdlib.h>

#pragma clang diagnostic ignored "-Wdeprecated-declarations"

extern void goShopwareFSEvent(uintptr_t handle, char *path, uint32_t flags);

typedef struct {
	FSEventStreamRef stream;
	CFRunLoopRef runLoop;
} ShopwareFSEventWatcher;

static void shopwareFSEventCallback(
	ConstFSEventStreamRef streamRef,
	void *clientCallBackInfo,
	size_t numEvents,
	void *eventPaths,
	const FSEventStreamEventFlags eventFlags[],
	const FSEventStreamEventId eventIds[]
) {
	(void)streamRef;
	(void)eventIds;
	char **paths = (char **)eventPaths;
	uintptr_t handle = (uintptr_t)clientCallBackInfo;
	for (size_t index = 0; index < numEvents; index++) {
		goShopwareFSEvent(handle, paths[index], (uint32_t)eventFlags[index]);
	}
}

static ShopwareFSEventWatcher *shopwareFSEventCreate(
	const char *path,
	uintptr_t handle
) {
	CFStringRef watchedPath = CFStringCreateWithCString(
		kCFAllocatorDefault,
		path,
		kCFStringEncodingUTF8
	);
	if (watchedPath == NULL) {
		return NULL;
	}
	const void *values[] = { watchedPath };
	CFArrayRef paths = CFArrayCreate(
		kCFAllocatorDefault,
		values,
		1,
		&kCFTypeArrayCallBacks
	);
	CFRelease(watchedPath);
	if (paths == NULL) {
		return NULL;
	}

	ShopwareFSEventWatcher *watcher = calloc(1, sizeof(ShopwareFSEventWatcher));
	if (watcher == NULL) {
		CFRelease(paths);
		return NULL;
	}
	FSEventStreamContext context = {0, (void *)handle, NULL, NULL, NULL};
	watcher->stream = FSEventStreamCreate(
		kCFAllocatorDefault,
		&shopwareFSEventCallback,
		&context,
		paths,
		kFSEventStreamEventIdSinceNow,
		0.10,
		kFSEventStreamCreateFlagFileEvents |
			kFSEventStreamCreateFlagWatchRoot |
			kFSEventStreamCreateFlagNoDefer
	);
	CFRelease(paths);
	if (watcher->stream == NULL) {
		free(watcher);
		return NULL;
	}

	watcher->runLoop = CFRunLoopGetCurrent();
	FSEventStreamScheduleWithRunLoop(
		watcher->stream,
		watcher->runLoop,
		kCFRunLoopDefaultMode
	);
	if (!FSEventStreamStart(watcher->stream)) {
		FSEventStreamInvalidate(watcher->stream);
		FSEventStreamRelease(watcher->stream);
		free(watcher);
		return NULL;
	}
	return watcher;
}

static void shopwareFSEventStep(ShopwareFSEventWatcher *watcher) {
	(void)watcher;
	CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.25, false);
}

static void shopwareFSEventDestroy(ShopwareFSEventWatcher *watcher) {
	if (watcher == NULL) {
		return;
	}
	FSEventStreamStop(watcher->stream);
	FSEventStreamInvalidate(watcher->stream);
	FSEventStreamRelease(watcher->stream);
	free(watcher);
}

static uint32_t shopwareFSEventRescanFlags(void) {
	return (uint32_t)(
		kFSEventStreamEventFlagMustScanSubDirs |
		kFSEventStreamEventFlagUserDropped |
		kFSEventStreamEventFlagKernelDropped |
		kFSEventStreamEventFlagRootChanged
	);
}
*/
import "C"

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/cgo"
	"time"
	"unsafe"
)

const watcherDebounce = 200 * time.Millisecond

type platformWatcherState struct {
	watcherLive bool
}

// StartWatcher uses FSEvents on macOS. fsnotify's kqueue backend opens one
// descriptor for every watched file, which is prohibitively expensive for a
// Shopware checkout and can exhaust the machine-wide descriptor table.
func (fs *FileScanner) StartWatcher() error {
	fs.watcherMu.Lock()
	if fs.watcherLive {
		fs.watcherMu.Unlock()
		return fmt.Errorf("file watcher is already running")
	}
	if fs.watcherCtx == nil || fs.watcherCtx.Err() != nil {
		fs.watcherCtx, fs.cancel = context.WithCancel(context.Background())
	}
	fs.nativeEvents = make(chan fileSystemEvent, 8192)
	fs.watcherLive = true
	fs.watcherMu.Unlock()

	ready := make(chan error, 1)
	handle := cgo.NewHandle(fs)
	fs.watcherWg.Add(1)
	go fs.runFSEventStream(handle, ready)
	if err := <-ready; err != nil {
		fs.cancel()
		fs.watcherWg.Wait()
		fs.watcherMu.Lock()
		fs.watcherLive = false
		fs.nativeEvents = nil
		fs.watcherMu.Unlock()
		return err
	}

	fs.watcherWg.Add(1)
	go fs.watchFSEvents()
	return nil
}

func (fs *FileScanner) runFSEventStream(
	handle cgo.Handle,
	ready chan<- error,
) {
	defer fs.watcherWg.Done()
	defer handle.Delete()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	path := C.CString(fs.projectRoot)
	defer C.free(unsafe.Pointer(path))
	watcher := C.shopwareFSEventCreate(path, C.uintptr_t(handle))
	if watcher == nil {
		ready <- fmt.Errorf("create FSEvents watcher for %s", fs.projectRoot)
		return
	}
	ready <- nil
	for fs.watcherCtx.Err() == nil {
		C.shopwareFSEventStep(watcher)
	}
	C.shopwareFSEventDestroy(watcher)
}

//export goShopwareFSEvent
func goShopwareFSEvent(
	handle C.uintptr_t,
	path *C.char,
	flags C.uint32_t,
) {
	if path == nil {
		return
	}
	fs, ok := cgo.Handle(handle).Value().(*FileScanner)
	if !ok || fs == nil || fs.nativeEvents == nil {
		return
	}
	select {
	case fs.nativeEvents <- fileSystemEvent{
		path: C.GoString(path), flags: uint32(flags),
	}:
	case <-fs.watcherCtx.Done():
	}
}

func (fs *FileScanner) watchFSEvents() {
	defer fs.watcherWg.Done()
	pending := make(map[string]struct{})
	fullScan := false
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(watcherDebounce)
	}
	var process func()
	process = func() {
		paths := mapKeys(pending)
		pending = make(map[string]struct{})
		if fullScan {
			fullScan = false
			if err := fs.IndexAll(fs.watcherCtx); err != nil &&
				fs.watcherCtx.Err() == nil {
				log.Printf("Error rescanning FSEvents changes: %v", err)
			}
			return
		}
		adds := make([]string, 0, len(paths))
		var removes []string
		for _, path := range paths {
			info, err := os.Stat(path)
			if err == nil && info.IsDir() {
				if fs.shouldEnterDirectory(path) {
					fullScan = true
				}
				continue
			}
			if err == nil {
				if isPHARArchivePath(path) {
					fullScan = true
				} else if fs.shouldIndexPath(path) {
					adds = append(adds, path)
				}
				continue
			}
			tracked, trackErr := fs.trackedPathsUnder(path)
			if trackErr != nil {
				log.Printf("Error resolving removed FSEvents path %s: %v", path, trackErr)
				continue
			}
			removes = append(removes, tracked...)
		}
		if fullScan {
			process()
			return
		}
		if len(adds) > 0 {
			if err := fs.IndexFiles(fs.watcherCtx, adds); err != nil &&
				fs.watcherCtx.Err() == nil {
				log.Printf("Error indexing FSEvents changes: %v", err)
			}
		}
		if len(removes) > 0 {
			if err := fs.RemoveFiles(fs.watcherCtx, removes); err != nil &&
				fs.watcherCtx.Err() == nil {
				log.Printf("Error removing FSEvents paths: %v", err)
			}
		}
	}

	rescanFlags := uint32(C.shopwareFSEventRescanFlags())
	for {
		select {
		case <-fs.watcherCtx.Done():
			return
		case event := <-fs.nativeEvents:
			if event.flags&rescanFlags != 0 {
				fullScan = true
			} else if event.path != "" {
				pending[event.path] = struct{}{}
			}
			resetTimer()
		case <-timer.C:
			process()
		}
	}
}

func (fs *FileScanner) StopWatcher() {
	fs.watcherMu.Lock()
	if !fs.watcherLive {
		fs.watcherMu.Unlock()
		return
	}
	fs.cancel()
	fs.watcherMu.Unlock()
	fs.watcherWg.Wait()
	fs.watcherMu.Lock()
	fs.watcherLive = false
	fs.nativeEvents = nil
	fs.watcherMu.Unlock()
}
