package smux

import (
	"errors"
	"io"
	"math/rand"
	"net"
	"time"
)

var (
	ErrTimeout    = errors.New("timeout. ")
	ErrClosedPipe = errors.New("the stream has closed. ")
	ErrBrokenPipe = errors.New("write on closed stream. ")
)

// 判断对端是否是多路复用
func IsSmux(conn net.Conn, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	v1, v2 := uint16(r.Uint32()%65535), r.Uint32()
	v11, v22 := verifyCode(v1, v2)

	conn.SetWriteDeadline(deadline)
	defer conn.SetWriteDeadline(time.Time{})
	_, err := conn.Write(newHeader(cmdVRM, v1, v2))
	if err != nil {
		panic(err)
	}

	conn.SetReadDeadline(deadline)
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

	if hdr.Cmd() != cmdVRM || hdr.Uint16() != v11 || hdr.Uint32() != v22 {
		return false
	}
	return true
}

func Listen(address string, callback func(session *MuxSession)) error {
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

		callback(NewMuxSession(conn))
	}
}

func Dial(address string, timeout time.Duration) (*MuxSession, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	return NewMuxSession(conn), nil
}
