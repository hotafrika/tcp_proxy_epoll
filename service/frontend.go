package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hotafrika/tcp_proxy_epoll/pkg/epoll"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"
)

// frontend is ...
type frontend struct {
	ctx         context.Context
	logger      *zerolog.Logger
	app         *application
	laddr       *net.TCPAddr
	tcpListener *net.TCPListener
	rmu         sync.RWMutex
	connections map[int]*PipedConn
	bufPool     *sync.Pool
	epoller     *epoll.Epoll
}

var _ connManager = (*frontend)(nil)

func newFrontend(ctx context.Context, logger *zerolog.Logger, port int, app *application, bufPool *sync.Pool) (*frontend, error) {
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, errors.Wrap(err, "ResolveTCPAddr()")
	}
	epoller, err := epoll.New()
	if err != nil {
		return nil, errors.Wrap(err, "New()")
	}
	return &frontend{
		ctx:         ctx,
		logger:      logger,
		app:         app,
		laddr:       addr,
		connections: make(map[int]*PipedConn),
		bufPool:     bufPool,
		epoller:     epoller,
	}, nil
}

// addConn adds new connection to the connections map or closes this connection.
func (f *frontend) addConn(conn *PipedConn) {
	select {
	case <-f.ctx.Done():
		conn.Close()
	default:
	}
	// add connection to the connection map
	// add connection fd to epoll
	f.rmu.Lock()
	defer f.rmu.Unlock()
	f.connections[conn.fd] = conn
	f.epoller.Add(conn.fd)
}

// delConn deletes connection from the connections map or does nothing.
func (f *frontend) delConn(fd int) {
	select {
	case <-f.ctx.Done():
		return
	default:
	}
	// delete connection fd from epoll
	// delete connection from connection map
	f.rmu.Lock()
	defer f.rmu.Unlock()
	f.epoller.Del(fd)
	delete(f.connections, fd)
}

// getConnByFD returns connection by its file descriptor.
func (f *frontend) getConnByFD(fd int) *PipedConn {
	f.rmu.RLock()
	defer f.rmu.RUnlock()
	return f.connections[fd]
}

// run is a blocking function. It tries to create tcpListener.
// It starts listenForNewConn goroutine.
// It exits on ctx is done and closes tcpListener and all connections.
func (f *frontend) run(wg *sync.WaitGroup) {
	defer wg.Done()

	// trying to create TCP listener in the loop
	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		tcpListener, err := net.ListenTCP("tcp", f.laddr)
		if err != nil {
			f.logger.Error().Err(err).Str("frontend", f.laddr.String()).Msg("ListenTCP()")
			time.Sleep(5 * time.Second)
			continue
		}
		f.tcpListener = tcpListener
		break
	}

	go f.listenEpoll()
	go f.listenForNewConn()

	// waiting for the graceful shutdown. After this it closes the listener, epoll and connections
	<-f.ctx.Done()
	f.logger.Info().Str("frontend", f.laddr.String()).Msg("closing listener and connections")

	f.tcpListener.Close()
	f.epoller.Close()

	f.rmu.RLock()
	defer f.rmu.RUnlock()
	for _, conn := range f.connections {
		conn.Close()
	}
}

// listenForNewConn is a blocking function. It is responsible for accepting new incoming connections.
func (f *frontend) listenForNewConn() {
	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		netConn, err := f.tcpListener.AcceptTCP()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			f.logger.Info().Err(err).Str("frontend", f.laddr.String()).Msg("AcceptTCP()")
			continue
		}

		f.logger.Debug().Str("frontend", f.laddr.String()).Str("connection", netConn.RemoteAddr().String()).Msg("accepted new connection")

		go f.handleNewConnection(netConn)
	}
}

func (f *frontend) listenEpoll() {
	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		events, err := f.epoller.Wait()
		if err != nil {
			f.logger.Info().Err(err).Str("frontend", f.laddr.String()).Msg("Wait()")
			continue
		}
		for _, event := range events {
			f.serveEvent(event)
		}
	}
}

// handleNewConnection processes new incoming connections. It tries to find available backend and create remote connection.
// This function creates two PipedConn for every direction of io operation.
func (f *frontend) handleNewConnection(netConn *net.TCPConn) {
	// creating a remote connection Conn
	rConn, err := f.app.createRemoteConnection()
	if err != nil {
		f.logger.Error().Err(err).Str("frontend", f.laddr.String()).Msg("can't find next backend")
		f.logger.Debug().Msgf("closing connection %s -> %s", netConn.RemoteAddr().String(), netConn.LocalAddr().String())
		netConn.Close()
		return
	}
	// creating a local connection Conn
	conn := newConn(netConn, f)

	finalizeOnce := sync.Once{}
	// creating  -->proxy-->  piped connection
	tunneledConn := newPiped(conn, rConn, &finalizeOnce)
	tunneledConn.manager.addConn(tunneledConn)
	// creating  <--proxy<--  piped connection
	rTunneledConn := newPiped(rConn, conn, &finalizeOnce)
	rTunneledConn.manager.addConn(rTunneledConn)
}

// getBuf() returns buffer from the buffer pool.
func (f *frontend) getBuf() *[]byte {
	return f.bufPool.Get().(*[]byte)
}

// serveEvent checks the type of event and handles it.
func (f *frontend) serveEvent(event unix.EpollEvent) {
	conn := f.getConnByFD(int(event.Fd))
	if conn == nil {
		return
	}

	if !conn.setUnderIO(true) {
		return
	}

	if event.Events&(unix.EPOLLHUP|unix.EPOLLRDHUP) != 0 {
		go conn.finalizeOnce.Do(func() {
			fmt.Println("front: because of event type nix.EPOLLHUP|unix.EPOLLRDHUP", event.Events)
			f.logger.Debug().Msgf("closing connection %s -> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
			f.logger.Debug().Msgf("closing connection %s -> %s", conn.pipeTo.LocalAddr().String(), conn.pipeTo.RemoteAddr().String())
			conn.finalize()
		})
		return
	}

	// TODO use goroutine pool in the future
	if event.Events&unix.EPOLLIN != 0 {
		fmt.Println("front: because of event EPOLLIN", event.Events)
		go f.serveConn(conn)
	}
}

// serveConn executes IO operation for connections.
func (f *frontend) serveConn(conn *PipedConn) {
	buf := f.getBuf()
	defer f.bufPool.Put(buf)

	n, err := io.CopyBuffer(conn.pipeTo, conn, *buf)
	conn.setUnderIO(false)

	if err != nil {
		f.logger.Info().Err(err).Msgf("can't copy data %s -> %s", conn.LocalAddr().String(), conn.pipeTo.RemoteAddr().String())
	}
	if err != nil || n == 0 {
		conn.finalizeOnce.Do(func() {
			f.logger.Debug().Msgf("closing connection %s -> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
			f.logger.Debug().Msgf("closing connection %s -> %s", conn.pipeTo.LocalAddr().String(), conn.pipeTo.RemoteAddr().String())
			conn.finalize()
		})
		return
	}
}
