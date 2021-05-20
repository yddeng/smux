package smux

import (
	"errors"
	"net"
)

var (
	ErrInvalidCmd = errors.New("invalid command")
	ErrTimeout    = errors.New("timeout")
	ErrClosedPipe = errors.New("read/write on closed pipe")
	ErrBrokenPipe = errors.New("broken pipe")
)

func notifyEvent(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func Server(conn *net.TCPConn) *Session {
	return newSession(conn, 1)
}

func Client(conn *net.TCPConn) *Session {
	return newSession(conn, 2)
}
