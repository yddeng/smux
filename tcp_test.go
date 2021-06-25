package smux

import (
	"net"
	"testing"
	"time"
)

func connection(addr string) (conn net.Conn, conn2 net.Conn, close func()) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}

	conn, err = net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	conn2, err = ln.Accept()
	if err != nil {
		return
	}

	return conn, conn2, func() {
		conn.Close()
		conn2.Close()
		ln.Close()
	}
	//for {
	//	conn, err := ln.Accept()
	//	if err != nil {
	//		return
	//	}
	//
	//	go func() {
	//		buf := make([]byte, 2048)
	//		for {
	//			_, err := conn.Read(buf)
	//			if err != nil {
	//				return
	//			}
	//		}
	//	}()
	//
	//}
}

func TestTCPSend(t *testing.T) {
	readf := func(conn net.Conn) {
		buf := make([]byte, 2048)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				return
			}
		}
	}

	sendf := func(conn net.Conn, buf []byte) {
		now := time.Now()
		sendn := 0
		for {
			n, err := conn.Write(buf)
			if err != nil {
				break
			}
			sendn += n
			if sendn >= 16*1024*1024 {
				break
			}
		}
		t.Logf("send %d, useTime %s", sendn, time.Since(now).String())
	}

	addr := "127.0.0.1:9999"
	conn, conn2, close := connection(addr)
	go readf(conn)
	sendf(conn2, make([]byte, 1024))
	close()

	time.Sleep(time.Millisecond * 100)
	conn, conn2, close = connection(addr)
	go readf(conn)
	sendf(conn2, make([]byte, 1024*8))
	close()

	/*
		测试写，总量相等，少量多次写与多量少次写的对比
		多量少次 在发送耗时上比 少量多次 更少
		写入量到达8k后，性能不再提升。

		tcp_test.go:52: send 16777216, useTime 96.316858ms // 1024
		tcp_test.go:52: send 16777216, useTime 45.219984ms // 1024*2

		tcp_test.go:52: send 16777216, useTime 100.832707ms // 1024
		tcp_test.go:52: send 16777216, useTime 23.085315ms  // 1024*4

		tcp_test.go:52: send 16777216, useTime 92.895334ms // 1024
		tcp_test.go:52: send 16777216, useTime 11.242953ms // 1024*8

		tcp_test.go:52: send 16777216, useTime 94.337331ms // 1024
		tcp_test.go:52: send 16777216, useTime 10.983391ms // 1024*16

		tcp_test.go:52: send 16777216, useTime 93.026444ms // 1024
		tcp_test.go:52: send 16785408, useTime 11.205002ms // 1024*12
	*/
}

func TestTCPRead(t *testing.T) {
	sendf := func(conn net.Conn) {
		buf := make([]byte, 1024*8)
		sendn := 0
		for {
			n, err := conn.Write(buf)
			if err != nil {
				return
			}
			sendn += n
			if sendn >= 16*1024*1024 {
				break
			}
		}
	}

	readf := func(conn net.Conn, buf []byte) {
		now := time.Now()
		readn := 0
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break
			}
			readn += n
			// time.Sleep(time.Millisecond) // 处理数据耗时
			if readn >= 16*1024*1024 {
				break
			}
		}
		t.Logf("read %d, useTime %s", readn, time.Since(now).String())
	}

	addr := "127.0.0.1:9999"

	conn, conn2, close := connection(addr)
	go sendf(conn)
	readf(conn2, make([]byte, 1024))
	close()

	time.Sleep(time.Millisecond * 100)
	conn, conn2, close = connection(addr)
	go sendf(conn)
	readf(conn2, make([]byte, 1024*4))
	close()

	/*
		测试读
		2k左右，性能不再提升

		tcp_test.go:139: read 16777216, useTime 29.365764ms // 1024
		tcp_test.go:139: read 16777216, useTime 13.738081ms // 1024*2

		这里没有逻辑处理数据0耗时。有逻辑时tcp缓冲区会堆积更多的数据，扩大有提升
		假定处理数据耗时 Millisecond

		tcp_test.go:140: read 16777216, useTime 22.659684142s // 1024
		tcp_test.go:140: read 16777216, useTime 5.663868389s  // 1024*4

		tcp_test.go:140: read 16777216, useTime 22.620517942s // 1024
		tcp_test.go:140: read 16777216, useTime 2.845837955s  // 1024*8

	*/
}

func TestTCPNOTAppect(t *testing.T) {
	addr := "127.0.0.1:0"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		panic(err)
	}

	n, err := conn.Write(make([]byte, 1024))
	t.Log(n, err)

}
