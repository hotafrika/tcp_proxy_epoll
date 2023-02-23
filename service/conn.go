package service

import (
	"net"
	"reflect"
	"sync"
	"sync/atomic"
)

type connManager interface {
	addConn(*PipedConn)
	delConn(int)
}

// Conn is the net.Conn wrapper that contains also information about its file descriptor and connManager.
type Conn struct {
	net.Conn
	fd      int
	closed  atomic.Bool
	manager connManager
}

func newConn(conn net.Conn, manager connManager) *Conn {
	return &Conn{
		Conn:    conn,
		fd:      fdFromConn(conn),
		manager: manager,
	}
}

// Close closes net.Conn and prevents repeated connection close.
func (c *Conn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		return c.Conn.Close()
	}
	return nil
}

// PipedConn is the Conn wrapper (A-leg) that contains information about B-leg connection.
// This is directional entity. A-leg is the connection for reading. B-leg is the connection for writing.
type PipedConn struct {
	*Conn
	pipeTo       *Conn
	finalizeOnce *sync.Once
	underIO      atomic.Bool
}

func newPiped(conn *Conn, out *Conn, finalizeOnce *sync.Once) *PipedConn {
	return &PipedConn{
		Conn:         conn,
		pipeTo:       out,
		finalizeOnce: finalizeOnce,
	}
}

// finalize closes A- and B-leg connections and deletes them from connManager.
func (c *PipedConn) finalize() {
	c.setUnderIO(false)
	c.Close()
	c.pipeTo.Close()
	c.pipeTo.manager.delConn(c.pipeTo.fd)
	c.manager.delConn(c.fd)
}

// setUnderIO can be used to change the state. Also, it returns result if the value was changed.
// If we set true and it was true, this method returns false.
// If we set true and it was false, this method returns true.
func (c *PipedConn) setUnderIO(underIO bool) bool {
	return c.underIO.CompareAndSwap(!underIO, underIO)
}

// fdFromConn extracts fd from net.Conn.
func fdFromConn(conn net.Conn) int {
	tcpConn := reflect.Indirect(reflect.ValueOf(conn)).FieldByName("conn")
	fdVal := tcpConn.FieldByName("fd")
	pfdVal := reflect.Indirect(fdVal).FieldByName("pfd")
	return int(pfdVal.FieldByName("Sysfd").Int())
}
