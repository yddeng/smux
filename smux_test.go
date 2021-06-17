package smux

import (
	"net"
	"sync"
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

		f(SmuxSession(conn.(*net.TCPConn)))
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

	s := SmuxSession(conn.(*net.TCPConn))
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

func TestStream_SetNonblock(t *testing.T) {
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
			}
		}()
	})

	time.Sleep(time.Second)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	s := SmuxSession(conn.(*net.TCPConn))
	stream, err := s.Open()
	if err != nil {
		panic(err)
	}

	stream.SetNonblock(true)

	t.Log("s1", stream.StreamID())

	data := make([]byte, 64*1024)
	sum := 0
	for {
		n, err := stream.Write(data)
		t.Log("s1 write", n, err)
		sum += n
		if err != nil {
			break
		}
	}

	t.Log("fullWrite", sum)
	stream.Close()
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

	s := SmuxSession(conn)
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

func setupServer(tb testing.TB) (addr string, stopfunc func(), client net.Conn, err error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, nil, err
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleConnection(conn)
	}()
	addr = ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		ln.Close()
		return "", nil, nil, err
	}
	return ln.Addr().String(), func() { ln.Close() }, conn, nil
}

func handleConnection(conn net.Conn) {
	session := SmuxSession(conn.(*net.TCPConn))
	for {
		if stream, err := session.Accept(); err == nil {
			go func(s *Stream) {
				buf := make([]byte, 65536)
				i := 0
				send := 0
				for {
					n, err := s.Read(buf)
					if err != nil {
						return
					}
					i++
					sendn, _ := s.Write(buf[:n])
					send += sendn
				}
			}(stream)
		} else {
			return
		}
	}
}

func TestSpeed(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session := SmuxSession(cli.(*net.TCPConn))
	stream, _ := session.Open()
	t.Log(stream.LocalAddr(), stream.RemoteAddr())

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		buf := make([]byte, 1024*1024)
		nrecv := 0
		i := 0
		for {
			n, err := stream.Read(buf)
			if err != nil {
				t.Error(err)
				break
			} else {
				nrecv += n
				i++
				if nrecv >= 4096*4096 {
					break
				}
			}
		}
		stream.Close()
		t.Log("time for 16MB rtt", time.Since(start))
		wg.Done()
	}()
	msg := make([]byte, 8192)
	for i := 0; i < 2048; i++ {
		stream.Write(msg)
	}
	wg.Wait()
	session.Close()
}
