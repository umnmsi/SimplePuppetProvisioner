package interfaces

import "github.com/fsnotify/fsnotify"

// This painful composition of things from fsnotify.Watcher enables mocks for testing of code that uses fsnotify.
type FsnotifyWatcher struct {
	Close  func() error
	Add    func(name string) error
	Remove func(name string) error

	Events chan fsnotify.Event
	Errors chan error
}
