package smux

import (
	"errors"
	"io"
	"net"
	"time"
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

func Server(conn net.Conn) *Session {
	return newSession(conn, 1)
}

func Client(conn net.Conn) *Session {
	return newSession(conn, 2)
}

func Listen(address string, callback func(session *Session)) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(time.Millisecond * 5)
				continue
			} else {
				return err
			}
		}

		callback(Server(conn))
	}
}

func Dial(address string) (*Session, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	return Client(conn), nil
}
