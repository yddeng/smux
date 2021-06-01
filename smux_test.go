package smux

import (
	"net"
	"testing"
	"time"
)

func listen(addr string, f func(session *Session)) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			panic(err)
		}

		f(NewSession(conn.(*net.TCPConn)))
	}
}

var ch = make(chan struct{})

func handle(s *Stream, t *testing.T) {
	go func() {
		<-ch
		b := make([]byte, 64*1024)
		sum := 0
		for {
			n, err := s.Read(b)
			if err != nil {
				t.Log("s2", err)
				break
			}
			sum += n
			//t.Log("s2 read", s.StreamID(), n)
		}

		t.Log("s2 read all length", sum)
	}()
}

func TestFullSend(t *testing.T) {
	addr := "127.0.0.1:4562"
	go listen(addr, func(session *Session) {
		t.Log("new session")
		go func() {
			for {
				s, err := session.Accept()
				if err != nil {
					panic(err)
				}
				t.Log("new stream", s.StreamID())
				handle(s, t)
			}
		}()
	})

	time.Sleep(time.Second)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	s := NewSession(conn.(*net.TCPConn))
	stream, err := s.Open()
	if err != nil {
		panic(err)
	}

	t.Log("s1", stream.StreamID())

	data := make([]byte, 64*1024)
	sum := 0
	for {
		stream.SetWriteDeadline(time.Now().Add(time.Second))
		n, err := stream.Write(data)
		t.Log("s1 write", n, err)
		sum += n
		if err != nil {
			break
		}
	}

	t.Log("fullWrite", sum)
	close(ch)
	stream.SetWriteDeadline(time.Time{})
	stream.Close()

	time.Sleep(time.Second * 5)

}

func TestStream_Open(t *testing.T) {
	addr := "127.0.0.1:4562"

	go listen(addr, func(s *Session) {
		t.Log("new session")
		go func() {
			for {
				stream, err := s.Accept()
				if err != nil {
					panic(err)
				}
				t.Log("new stream", stream.StreamID())
				handle(stream, t)
			}
		}()

		go func() {
			for {
				stream, err := s.Open()
				if err != nil {
					t.Log(err)
					break
				}
				t.Log("open ", stream.StreamID())
				stream.Write([]byte{1, 2, 3, 4})
				time.Sleep(time.Millisecond)
			}
		}()
	})
	time.Sleep(time.Second)
	close(ch)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	s := NewSession(conn)
	go func() {
		for {
			stream, err := s.Accept()
			if err != nil {
				panic(err)
			}
			t.Log("new stream", stream.StreamID())
			handle(stream, t)
		}
	}()

	for {
		stream, err := s.Open()
		if err != nil {
			t.Log(err)
			break
		}
		stream.Write([]byte{1, 2, 3, 4})
	}

	time.Sleep(time.Second)
}

func TestIsSmux(t *testing.T) {

	addr := "127.0.0.1:4562"
	go listen(addr, func(session *Session) {
		t.Log("new session")
		go func() {
			for {
				s, err := session.Accept()
				if err != nil {
					panic(err)
				}
				t.Log("new stream", s.StreamID())
				handle(s, t)
			}
		}()
	})

	time.Sleep(time.Second)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	b := IsSmux(conn, time.Second*5)
	t.Log("remote connection is smux", b)
}
