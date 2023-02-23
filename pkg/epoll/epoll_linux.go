//go:build linux

package epoll

import (
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// Epoll is a simple wrapper for unix epoll .
type Epoll struct {
	fd int
}

func New() (*Epoll, error) {
	fd, err := unix.EpollCreate1(0)
	if err != nil {
		return nil, errors.Wrap(err, "EpollCreate1()")
	}
	return &Epoll{
		fd: fd,
	}, nil
}

// Add adds fd to epoll.
// EPOLLIN - associated with fd file is ready for read .
// EPOLLHUP - hang up happened on the associated file descriptor.
// EPOLLRDHUP - stream socket peer closed connection, or shut down writing half of connection.
// EPOLLET requests edge-triggered notification for the associated file descriptor .
func (e *Epoll) Add(fd int) error {
	// err := unix.EpollCtl(e.fd, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLHUP | unix.EPOLLRDHUP | unix.EPOLLET, Fd: int32(fd)})
	err := unix.EpollCtl(e.fd, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLHUP | unix.EPOLLRDHUP, Fd: int32(fd)})
	if err != nil {
		return errors.Wrap(err, "EpollCtl()")
	}
	return nil
}

// Del deletes fd from epoll.
func (e *Epoll) Del(fd int) error {
	err := unix.EpollCtl(e.fd, syscall.EPOLL_CTL_DEL, fd, nil)
	if err != nil {
		return errors.Wrap(err, "EpollCtl()")
	}
	return nil
}

// Close closes epoll fd.
func (e *Epoll) Close() error {
	err := unix.Close(e.fd)
	if err != nil {
		return errors.Wrap(err, "Close()")
	}
	return nil
}

// Wait returns FDs that received events.
func (e *Epoll) Wait() ([]unix.EpollEvent, error) {
	events := make([]unix.EpollEvent, 100)
	var n int
	var err error
	for {
		// -1 means block until new events
		n, err = unix.EpollWait(e.fd, events, -1)
		if err != nil {
			if temporaryErr(err) {
				continue
			}
			return nil, errors.Wrap(err, "EpollWait()")
		}
		break
	}

	return events[:n], nil
}

// temporaryErr helps to check if err is temporary according to
// https://cs.opensource.google/go/go/+/refs/tags/go1.19.4:src/syscall/syscall_unix.go;l=134 .
func temporaryErr(err error) bool {
	return err == unix.EINTR ||
		err == unix.EMFILE ||
		err == unix.ENFILE ||
		err == unix.EAGAIN ||
		err == unix.EWOULDBLOCK ||
		err == unix.ETIMEDOUT
}
