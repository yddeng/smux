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

const (
	err_ok byte = iota
	err_invalid_cmd
	err_timeout
	err_closed_pipe
	err_broken_pipe
	err_eof
)

func GetCode(code byte) error {
	switch code {
	case err_invalid_cmd:
		return ErrInvalidCmd
	case err_timeout:
		return ErrTimeout
	case err_closed_pipe:
		return ErrClosedPipe
	case err_broken_pipe:
		return ErrBrokenPipe
	default:
		return nil
	}
}

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
