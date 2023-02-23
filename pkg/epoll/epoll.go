//go:build !linux

package epoll

import (
	"github.com/pkg/errors"
)

// Epoll stub for non-linux envs. For Goland code check purposes only.
type Epoll struct {
}

func New() (*Epoll, error) {
	return nil, errors.New("it works only in Linux systems")
}

func (e *Epoll) Add(fd int) error {
	return nil
}

func (e *Epoll) Del(fd int) error {
	return nil
}

func (e *Epoll) Close() error {
	return nil
}

func (e *Epoll) Wait() ([]int, error) {
	return nil, nil
}
