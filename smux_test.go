package smux

import (
	"fmt"
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

		f(Server(conn.(*net.TCPConn)))
	}
}

func dial(addr string) *Session {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	return Client(conn.(*net.TCPConn))
}

var ch = make(chan struct{})

func handle(s *Stream) {
	go func() {
		<-ch
		b := make([]byte, 65535)
		for {
			n, err := s.Read(b)
			if err != nil {
				panic(err)
			}
			fmt.Println("s2 read", s.StreamID(), n)
			n, err = s.Write(b[:n])
			fmt.Println("s2 write", s.StreamID(), n, err)
		}
	}()
}

func TestSmux(t *testing.T) {
	addr := "127.0.0.1:4562"
	go listen(addr, func(session *Session) {
		fmt.Println("new session")

		go func() {
			for {
				s, err := session.Accept()
				if err != nil {
					panic(err)
				}
				fmt.Println("new stream", s.StreamID())
				if s.StreamID() == 4 {
					handle(s)
				}
			}
		}()
	})

	time.Sleep(time.Second)

	s := dial(addr)
	stream, err := s.Open()
	if err != nil {
		panic(err)
	}

	fmt.Println("s1", stream.StreamID())

	data := make([]byte, 1024*64)
	sum := 0
	for {
		stream.SetWriteDeadline(time.Now().Add(time.Second))
		n, err := stream.Write(data)
		fmt.Println("s1 write", n, err)
		sum += n
		if err != nil {
			break
		}
	}

	fmt.Println("fullWrite", sum)
	close(ch)

	//n, err := stream.Read(data)
	//fmt.Println("s1 read", n, err)
	//stream.Close()

	stream2, _ := s.Open()
	n, err := stream2.Write(data)
	fmt.Println("s2 write", n, err)

	time.Sleep(time.Second * 5)

}
