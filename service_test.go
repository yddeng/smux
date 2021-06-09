package smux

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	aioConns   = map[uint16]*AIOConn{}
	aioLock    = sync.Mutex{}
	aioService *AIOService

	taskQueue chan func()
)

func init() {
	taskQueue = make(chan func(), 128)
	go func() {
		for {
			taskFunc := <-taskQueue
			taskFunc()
		}
	}()
}

type AIOConn struct {
	stream *Stream
}

func newAIOConn(stream *Stream) *AIOConn {
	return &AIOConn{
		stream: stream,
	}
}

func (this *AIOConn) doWrite() {
}

func (this *AIOConn) doRead() {
	taskQueue <- func() {
		buf := make([]byte, 128)
		n, err := this.stream.Read(buf)
		fmt.Println("read", this.stream.StreamID(), n, err, buf[:n])
		n, err = this.stream.Write(buf[:n])
		fmt.Println("write", this.stream.StreamID(), n, err, buf[:n])
	}
}

func (this *AIOConn) onEventCallback(event Event) {
	fmt.Println("onEventCallback", this.stream.StreamID(), event)
	if event.Readable() {
		this.doRead()
	}

	if event.Writable() {
		this.doWrite()
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
		for {
			stream, err := session.Accept()
			if err != nil {
				t.Error(err)
				return
			}
			//t.Log("stream", stream.StreamID())

			aioConn := &AIOConn{stream: stream}

			aioLock.Lock()
			aioConns[stream.StreamID()] = aioConn
			aioLock.Unlock()

			stream.SetNonblock(true)
			aioService.Watch(stream.StreamID(), aioConn.onEventCallback)
		}
	}()

}

func client(conn net.Conn, t *testing.T) {
	session := SmuxSession(conn)

	wg := sync.WaitGroup{}

	var count uint32 = 0
	for i := 0; i < 100; i++ {
		stream, _ := session.Open()
		wg.Add(1)
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, uint32(i))

		//t.Log("open", stream.StreamID())
		go func(stream *Stream) {
			defer wg.Done()
			defer stream.Close()

			_, err := stream.Write(data)
			if err != nil {
				t.Error(err)
				return
			}
			buf := make([]byte, 12)
			n, err := stream.Read(buf)
			if err != nil {
				t.Error(err)
				return
			}
			k := atomic.AddUint32(&count, 1)
			t.Log(buf[:n], k)

		}(stream)
	}

	wg.Wait()
}
