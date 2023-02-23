package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hotafrika/tcp_proxy_epoll/pkg/epoll"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"
)

type backend struct {
	ctx         context.Context
	logger      *zerolog.Logger
	addr        string
	dialler     net.Dialer
	active      atomic.Bool
	rmu         sync.RWMutex
	connections map[int]*PipedConn
	bufPool     *sync.Pool
	epoller     *epoll.Epoll

	healthcheckInterval time.Duration
}

var _ connManager = (*backend)(nil)

func newBackend(ctx context.Context, logger *zerolog.Logger, address string, bufPool *sync.Pool) (*backend, error) {
	_, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.Wrap(err, "SplitHostPort()")
	}
	dialer := net.Dialer{
		Timeout: 2 * time.Second,
	}
	epoller, err := epoll.New()
	if err != nil {
		return nil, errors.Wrap(err, "New()")
	}
	return &backend{
		ctx:                 ctx,
		logger:              logger,
		addr:                address,
		dialler:             dialer,
		connections:         make(map[int]*PipedConn),
		bufPool:             bufPool,
		epoller:             epoller,
		healthcheckInterval: 5 * time.Second,
	}, nil
}

// addConn adds connection to the connections map or closes this connection.
func (b *backend) addConn(conn *PipedConn) {
	select {
	case <-b.ctx.Done():
		conn.Close()
	default:
	}
	// add connection to the connection map
	// add connection fd to epoll
	b.rmu.Lock()
	defer b.rmu.Unlock()
	b.connections[conn.fd] = conn
	b.epoller.Add(conn.fd)
}

// delConn deletes connection from the connections map or does nothing.
func (b *backend) delConn(fd int) {
	select {
	case <-b.ctx.Done():
		return
	default:
	}
	// delete connection fd from epoll
	// delete connection from connection map
	b.rmu.Lock()
	defer b.rmu.Unlock()
	b.epoller.Del(fd)
	delete(b.connections, fd)
}

// getConnCount returns connections count.
func (b *backend) getConnCount() int {
	b.rmu.RLock()
	defer b.rmu.RUnlock()
	return len(b.connections)
}

// getConnByFD returns connection by its file descriptor.
func (b *backend) getConnByFD(fd int) *PipedConn {
	b.rmu.RLock()
	defer b.rmu.RUnlock()
	return b.connections[fd]
}

// run is a blocking function. It starts runHealthcheck goroutine.
// It exits on ctx is done and closes all connections.
func (b *backend) run(wg *sync.WaitGroup) {
	defer wg.Done()

	go b.runHealthcheck()
	go b.listenEpoll()

	// waiting for the graceful shutdown. after this it closes epoll and connections
	<-b.ctx.Done()
	b.logger.Info().Str("backend", b.addr).Msg("closing connections")

	b.epoller.Close()

	b.rmu.RLock()
	defer b.rmu.RUnlock()
	for _, conn := range b.connections {
		conn.Close()
	}
}

// runHealthcheck is blocking method. It is responsible for active health checks of the target backend.
// It exits if backend ctx is done.
func (b *backend) runHealthcheck() {
	ticker := time.NewTicker(b.healthcheckInterval)
	defer ticker.Stop()

	// The first check is right after start
	netConn, err := b.dialler.DialContext(b.ctx, "tcp", b.addr)
	if err != nil {
		b.setActive(false)
	} else {
		netConn.Close()
		b.setActive(true)
	}

	// infinite loop to check backend availability
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			netConn, err = b.dialler.DialContext(b.ctx, "tcp", b.addr)
			if err != nil {
				b.setActive(false)
				continue
			}
			netConn.Close()
			b.setActive(true)
		}
	}
}

func (b *backend) listenEpoll() {
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		events, err := b.epoller.Wait()
		if err != nil {
			b.logger.Info().Err(err).Str("backend", b.addr).Msg("Wait()")
			continue
		}
		for _, event := range events {
			b.serveEvent(event)
		}
	}
}

func (b *backend) setActive(t bool) {
	if b.active.CompareAndSwap(!t, t) {
		b.logger.Info().Str("backend", b.addr).Bool("active", t).Msg("changed active status")
	}
}

// createConn creates new net.Conn to the backend.
func (b *backend) createConn() (net.Conn, error) {
	conn, err := b.dialler.DialContext(b.ctx, "tcp", b.addr)
	if err != nil {
		// passive healthcheck
		b.setActive(false)
		return nil, errors.Wrap(err, "Dial()")
	}
	b.logger.Debug().Str("backend", b.addr).Str("connection", conn.LocalAddr().String()).Msg("new remote connection")
	return conn, nil
}

// getBuf() returns buffer from the buffer pool.
func (b *backend) getBuf() *[]byte {
	buff, _ := b.bufPool.Get().(*[]byte)
	return buff
}

// serveEvent checks the type of event and handles it.
func (b *backend) serveEvent(event unix.EpollEvent) {
	conn := b.getConnByFD(int(event.Fd))
	if conn == nil {
		return
	}

	if !conn.setUnderIO(true) {
		return
	}

	if event.Events&(unix.EPOLLHUP|unix.EPOLLRDHUP) != 0 {
		go conn.finalizeOnce.Do(func() {
			fmt.Println("back: because of event type unix.EPOLLHUP|unix.EPOLLRDHUP", event.Events)
			b.logger.Debug().Msgf("closing connection %s -> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
			b.logger.Debug().Msgf("closing connection %s -> %s", conn.pipeTo.LocalAddr().String(), conn.pipeTo.RemoteAddr().String())
			conn.finalize()
		})
		return
	}

	// TODO use goroutine pool in the future
	if event.Events&unix.EPOLLIN != 0 {
		fmt.Println("back: because of event EPOLLIN", event.Events)
		go b.serveConn(conn)
	}
}

// serveConn executes IO operation for connections.
func (b *backend) serveConn(conn *PipedConn) {
	buf := b.getBuf()
	defer b.bufPool.Put(buf)

	n, err := io.CopyBuffer(conn.pipeTo, conn, *buf)
	conn.setUnderIO(false)

	if err != nil {
		b.logger.Info().Err(err).Msgf("can't copy data %s -> %s", conn.LocalAddr().String(), conn.pipeTo.RemoteAddr().String())
	}
	if err != nil || n == 0 {
		conn.finalizeOnce.Do(func() {
			b.logger.Debug().Msgf("closing connection %s -> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
			b.logger.Debug().Msgf("closing connection %s -> %s", conn.pipeTo.LocalAddr().String(), conn.pipeTo.RemoteAddr().String())
			conn.finalize()
		})
		return
	}
}
