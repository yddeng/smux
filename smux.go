package smux

import (
	"errors"
	"io"
	"math/rand"
	"net"
	"time"
)

var (
	ErrInvalidCmd = errors.New("invalid command. ")
	ErrTimeout    = errors.New("timeout. ")
	ErrClosedPipe = errors.New("read/write on closed pipe. ")
	ErrBrokenPipe = errors.New("broken pipe. ")
	ErrNoSmux     = errors.New("remote connection is't smux. ")
)

func notifyEvent(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// 判断对端是否是多路复用
func IsSmux(conn net.Conn) bool {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	v1, v2 := r.Uint32(), r.Uint32()
	v11, v22 := verifyCode(v1, v2)

	conn.SetWriteDeadline(time.Now().Add(time.Second * 5))
	defer conn.SetWriteDeadline(time.Time{})
	_, err := conn.Write(newHeader(cmdVRM, v1, v2))
	if err != nil {
		panic(err)
	}

	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	defer conn.SetReadDeadline(time.Time{})
	var hdr header
	_, err = io.ReadFull(conn, hdr[:])
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return false
		} else {
			panic(err)
		}
	}

	if hdr.Cmd() != cmdVRM || hdr.StreamID() != v11 || hdr.Length() != v22 {
		return false
	}
	return true
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
	if !IsSmux(conn) {
		conn.Close()
		return nil, ErrNoSmux
	}
	return Client(conn), nil
}
