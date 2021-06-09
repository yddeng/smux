package smux

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
)

var (
	aioConns   = map[uint16]*AIOConn{}
	aioLock    = sync.Mutex{}
	aioService *AIOService

	taskQueue  = make(chan *task,128)
)

type task struct {

}

func

type AIOConn struct {
	stream *Stream

	readBuffer []byte
	readOffset int
	readLock   sync.Mutex

	writeBuffer []byte
	writeOffset int
	writeLock   sync.Mutex
}

func newAIOConn(stream *Stream) *AIOConn {
	return &AIOConn{
		stream:      stream,
		readBuffer:  make([]byte, 128),
		readLock:    sync.Mutex{},
		writeBuffer: make([]byte, 128),
		writeLock:   sync.Mutex{},
	}
}

func (this *AIOConn) onEventCallback(event Event) {
	if event.Readable() {

	}

	if event.Writeable() {

	}
}

func TestAIOService(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Error(err)
		return
	}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go server(conn, t)
	}()

	addr := ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Error(err)
		return
	}
	client(conn, t)
}

func server(conn net.Conn, t *testing.T) {
	session := SmuxSession(conn)

	aioService = session.OpenAIOService(1)

	go func() {
		stream, err := session.Accept()
		if err != nil {
			t.Error(err)
			return
		}

		aioConn := &AIOConn{stream: stream}

		aioLock.Lock()
		aioConns[stream.StreamID()] = aioConn
		aioLock.Unlock()

		aioService.Watch(stream.StreamID(), aioConn.onEventCallback)
	}()

}

func client(conn net.Conn, t *testing.T) {
	session := SmuxSession(conn)

	for i := 0; i < 100; i++ {
		stream, _ := session.Open()

		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, uint32(i))
		go func() {
			_, err := stream.Write(data)
			if err != nil {
				t.Error(err)
				return
			}
			buf := make([]byte, 12)
			for {
				n, err := stream.Read(buf)
				if err != nil {
					t.Error(err)
					return
				}
				t.Log(buf[:n])
			}

		}()
	}
}
