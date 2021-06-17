package smux

import (
	"encoding/binary"
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

	hdr := make([]byte, headerSize)
	hdr[0] = cmdVRM
	binary.LittleEndian.PutUint16(hdr[1:], v1)
	binary.LittleEndian.PutUint32(hdr[3:], v2)

	conn.SetWriteDeadline(deadline)
	if _, err := conn.Write(hdr); err != nil {
		panic(err)
	}
	conn.SetWriteDeadline(time.Time{})

	conn.SetReadDeadline(deadline)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return false
		} else {
			panic(err)
		}
	}
	conn.SetReadDeadline(time.Time{})

	cmd := hdr[0]
	rv1 := binary.LittleEndian.Uint16(hdr[1:])
	rv2 := binary.LittleEndian.Uint32(hdr[3:])
	if cmd != cmdVRM || rv1 != v11 || rv2 != v22 {
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
