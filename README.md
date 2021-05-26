# smux

`socket multiplexing`, 基于可靠连接`tcp`的多路复用。

基于 `xtaci/smux` 修改

## 缺陷

1. 某一个 `stream` 不读，但对端一直写导致 `tcp` 缓冲区被写满，进而堵塞其他的 `stream` 。

2. 对于`tcp`的超时写，可能已经写了部分数据才超时。 返回到`stream`应该表现出来。由问题1引出，如果设置了写超时，
    本次的写入数据只写入了一部分，返回应用层写入0（但实际tcp会将本次数据写完），如果应用层选择重发，导致数据重复或者混乱。

## 方案

1. 给每个`stream` 设置读`buffer`，通过窗口调节发送频率。

    由于通信链路只有一条，而有多条虚拟链路。需保证通信链路畅通，不被任意 `stream` 堵塞。
    通信链路的 `buffer` 大小固定为 `64k`。`stream` 的读写缓冲区大小固定为 `512k`。

    A端发送数据累计到 `waitConfirm` 中，往对端发送的窗口大小为 `streamWindowSize - waitConfirm`, 
    结果为0 时，B端读缓冲区已满。本端不再发送数据，等待B端读数据。

    B端应用层调用 `Read` 后，推送已读数据长度 `bufferRead` 。A端 `waitConfirm -= bufferBuffer`, 
    A端的发送窗口不为0，继续发送数据。

2. 已经调用了`tcp.Write` 的数据默认成功发送（除非`tcp`连接报错，否则一定能到达对端，即使会堵塞一段时间），如果写超时先到，则放弃本次的写。

    
## 新增

1. 判断对端是否是多路复用

`IsSmux(conn net.Conn) bool `

算法较简单
```
/*
	v1:uint32, v2:uint32。分4个8位(b1,b2,b3,b4)
	v1 : (b1,b2,b3,b4) -> (b3,b4,b2,b1)
	v2 : (b1,b2,b3,b4) -> (b1,b4,b2,b3)
*/
func verifyCode(v1, v2 uint32) (uint32, uint32) {
	v11 := ((v1 & 0xFFFF) << 16) | ((v1 & 0xFF000000) >> 24) | ((v1 & 0xFF0000) >> 8)
	v22 := (v2 & 0xFF000000) | ((v2 & 0xFF0000) >> 8) | ((v2 & 0xFF00) >> 8) | ((v2 & 0xFF) << 16)
	return v11, v22
}
```

## Usage

```go

func client() {
    // Get a TCP connection
    conn, err := net.Dial(...)
    if err != nil {
        panic(err)
    }

    // Setup client side of smux
    session, err := smux.Client(conn, nil)
    if err != nil {
        panic(err)
    }

    // Open a new stream
    stream, err := session.OpenStream()
    if err != nil {
        panic(err)
    }

    // Stream implements io.ReadWriteCloser
    stream.Write([]byte("ping"))
    stream.Close()
    session.Close()
}

func server() {
    // Accept a TCP connection
    conn, err := listener.Accept()
    if err != nil {
        panic(err)
    }

    // Setup server side of smux
    session, err := smux.Server(conn, nil)
    if err != nil {
        panic(err)
    }

    // Accept a stream
    stream, err := session.AcceptStream()
    if err != nil {
        panic(err)
    }

    // Listen for a message
    buf := make([]byte, 4)
    stream.Read(buf)
    stream.Close()
    session.Close()
}