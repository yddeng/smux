package smux

import (
	"net"
	"sync"
	"testing"
	"time"
)

//func listen(addr string, f func(session *MuxSession)) {
//	l, err := net.Listen("tcp", addr)
//	if err != nil {
//		panic(err)
//	}
//
//	for {
//		conn, err := l.Accept()
//		if err != nil {
//			panic(err)
//		}
//
//		f(NewMuxSession(conn.(*net.TCPConn)))
//	}
//}

var ch = make(chan struct{})

func startListen(addr string, t *testing.T) {
	go Listen(addr, func(session *MuxSession) {
		t.Log("new session")
		go func() {
			for {
				s, err := session.Accept()
				if err != nil {
					panic(err)
				}
				t.Log("new MuxConn", s.ID())
				handle(s, t)
			}
		}()
	})
}

func handle(s *MuxConn, t *testing.T) {
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
			//t.Log("s2 read", s.ID(), n)
		}

		t.Log("s2 read all length", sum)
	}()
}

func listenAndDial(addr string, t *testing.T) *MuxSession {
	startListen(addr, t)
	time.Sleep(time.Millisecond * 100)
	sess, err := Dial(addr, time.Second)
	if err != nil {
		panic(err)
	}
	return sess
}

func TestFullSend(t *testing.T) {
	addr := "127.0.0.1:4562"
	sess := listenAndDial(addr, t)

	conn, err := sess.Open()
	if err != nil {
		panic(err)
	}

	t.Log("s1", conn.ID())

	data := make([]byte, 64*1024)
	sum := 0
	for {
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		n, err := conn.Write(data)
		t.Log("s1 write", n, err)
		sum += n
		if err != nil {
			break
		}
	}

	t.Log("fullWrite", sum)
	close(ch)
	conn.SetWriteDeadline(time.Time{})
	conn.Close()

	time.Sleep(time.Second)
}

func TestStream_SetNonblock(t *testing.T) {
	addr := "127.0.0.1:4562"
	sess := listenAndDial(addr, t)

	conn, err := sess.Open()
	if err != nil {
		panic(err)
	}

	conn.SetNonblock(true)

	t.Log("s1", conn.ID())

	data := make([]byte, 64*1024)
	sum := 0
	for {
		n, err := conn.Write(data)
		t.Log("s1 write", n, err)
		sum += n
		if err != nil {
			break
		}
	}

	t.Log("fullWrite", sum)
	conn.Close()
}

func TestStream_Open(t *testing.T) {
	addr := "127.0.0.1:4562"

	go Listen(addr, func(s *MuxSession) {
		t.Log("new session")
		go func() {
			for {
				conn, err := s.Accept()
				if err != nil {
					panic(err)
				}
				t.Log("new MuxConn", conn.ID())
				handle(conn, t)
			}
		}()

		go func() {
			for {
				MuxConn, err := s.Open()
				if err != nil {
					t.Log(err)
					break
				}
				t.Log("open ", MuxConn.ID())
				MuxConn.Write([]byte{1, 2, 3, 4})
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

	s := NewMuxSession(conn)
	go func() {
		for {
			conn, err := s.Accept()
			if err != nil {
				panic(err)
			}
			t.Log("new MuxConn", conn.ID())
			handle(conn, t)
		}
	}()

	for {
		conn, err := s.Open()
		if err != nil {
			t.Log(err)
			break
		}
		conn.Write([]byte{1, 2, 3, 4})
	}

	time.Sleep(time.Second)
}

func TestIsSmux(t *testing.T) {

	addr := "127.0.0.1:4562"
	go Listen(addr, func(session *MuxSession) {
		t.Log("new session")
		go func() {
			for {
				s, err := session.Accept()
				if err != nil {
					panic(err)
				}
				t.Log("new MuxConn", s.ID())
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

func setupServer(t *testing.T) (addr string, stopfunc func(), client net.Conn, err error) {
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
	session := NewMuxSession(conn)
	for {
		if s, err := session.Accept(); err == nil {
			go func(s *MuxConn) {
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
			}(s)
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
	session := NewMuxSession(cli.(*net.TCPConn))
	conn, _ := session.Open()
	t.Log(conn.LocalAddr(), conn.RemoteAddr())

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		buf := make([]byte, 1024*1024)
		nrecv := 0
		i := 0
		for {
			n, err := conn.Read(buf)
			if err != nil {
				t.Error(err)
				break
			} else {
				nrecv += n
				i++
				//t.Log(i, nrecv)
				if nrecv >= 4096*4096 {
					break
				}
			}
		}
		conn.Close()
		t.Log("time for 16MB rtt", time.Since(start))
		wg.Done()
	}()
	msg := make([]byte, 8192)
	wn := 0
	for i := 0; i < 2048; i++ {
		n, _ := conn.Write(msg)
		wn += n
		//t.Log(i)
	}
	t.Log("write length", wn)
	wg.Wait()
	session.Close()
}
