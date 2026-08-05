//go:build windows

package tray

import "sync"

type quitSignal struct {
	once sync.Once
	done chan struct{}
}

func newQuitSignal() *quitSignal {
	return &quitSignal{done: make(chan struct{})}
}

func (s *quitSignal) signal() {
	s.once.Do(func() { close(s.done) })
}

func (s *quitSignal) channel() <-chan struct{} {
	return s.done
}
