# smux

`socket multiplexing`, 基于可靠连接`tcp`的多路复用。

基于 `xtaci/smux` 修改、新增部分逻辑。相较于原项目，传输效率有所下降。

下降原因，项目增加了发送字节确认机制。

## 1 修改及新增

1. 某一个 `stream` 不读，但对端一直写导致 `tcp` 缓冲区被写满，进而堵塞其他的 `stream` 。

2. 对于`tcp`的超时写，可能已经写了部分数据才超时。 返回到`stream`应该表现出来。由问题1引出，如果设置了写超时，
    本次的写入数据只写入了一部分，返回应用层写入0（但实际tcp会将本次数据写完），如果应用层选择重发，导致数据重复或者混乱。

3. streamID 可能溢出，虽然uint32的容量挺大的，但还是有可能。同时降低包头 streamID占用的长度，提高传输效率。

4. 新增检测对端是否使用多路复用


## 2 项目设计

基于可靠有序的连接

### 2.1 协议包头

`cmd (byte) + id (uint16) + length (uint32)`

cmd 命令类型：`cmdSYN` 打开一个`stream`；`cmdFIN`  关闭一个 `stream`；`cmdPSH` 数据推送；`cmdCFM` 数据接受确认；

id streamID。

length 视情况而定。 `cmdPSH` 代表携带的数据长度。`cmdCFM` 代表确认的数据长度。其他情况为0，无意义。

### 2.2 StreamID

根据协议包头可知，最大能支持的ID值为 `65535` 。

`streamID` 采用回收复用的方式，避免累加后超过上限值。用位图来存储使用、空闲的`id`，减少内存占用。

也意味着同时能开启的`stream`数量为`65535`个，超过这个数字就没有可使用的`id`了。经测试这个值较为合适，
连接数达到一定阀值，即`tcp`的传输效率已经到达最大，更多的`stream`数反而会带来性能瓶颈。这个时候建议多开`tcp`连接。

### 2.3 打开连接

在 `idBitmap` 中申请一个id，发往对端 `cmdSYN`命令。

正常情况下对端新建 `stream` 将其放入 `accept channel`中，对端通过 `Accept` 获取新`stream`。
存在两端同时 `Open` 且 分配到相同的`ID`。两端都会收到 `cmdSYN`，直接忽略命令(相当于直接绑定到现有`stream`上)。

### 2.4 数据传输

给每个`stream` 设置读`buffer`，通过窗口调节发送频率。

由于通信链路只有一条，而有多条虚拟链路。需保证通信链路畅通，不被任意 `stream` 堵塞。
通信链路的 `buffer` 大小固定为 `64k`。`stream` 的读写缓冲区大小固定为 `512k`。

A端发送数据累计到 `waitConfirm` 中，往对端发送的窗口大小为 `streamWindowSize - waitConfirm`, 
结果为0 时，B端读缓冲区已满。本端不再发送数据，等待B端读数据。
                                  
B端应用层调用 `Read` 后，推送已读数据长度 `bufferRead` 。A端 `waitConfirm -= bufferBuffer`, 
A端的发送窗口不为0，继续发送数据。


### 2.5 写超时

在`Stream`端可能会多次调用 `tcp.Write`（发送数据大于`frameSize`将会分包）。
已经调用了`tcp.Write` 的数据默认成功发送（除非`tcp`连接报错，否则一定能到达对端，即使会堵塞一段时间）。

如果 `stream` 的写超时到达，累计已经调用 `tcp.Write` 数据的长度返回，还未调用的不再调用。由应用层选择是否继续发送超时数据。

### 2.6 关闭连接

主动关闭连接方发送 `cmdFIN`到对端，并等待 `cmdFIN`回来。 整个流程全部完成，释放资源并回收`streamID`。

需要确认关闭的原因：
存在 A 调用`Close`，已经释放资源并回收`streamID`，`cmdFIN`正在发送B端。
这时，A端 `Open` 一个相同ID 的 `Stream`，B端还没有收到 `cmdFIN`，仍在向A端发送数据。
导致数据混乱。

同时关闭的情况，对于任意一端来说，都相当于发出并接收到`cmdFIN`，完成了整个关闭流程。

### 2.7 判断对端是否使用多路复用

`cmdVRM` 。id 和 length 为随机数，对端经过算法验证返回两个随机数的确定值。

`IsSmux(conn net.Conn) bool `

```
/*
	v1:uint16, v2:uint32。
	v1 分4个4位(b1,b2,b3,b4): (b1,b2,b3,b4) -> (b3,b4,b2,b1)
	v2 分4个8位(b1,b2,b3,b4): (b1,b2,b3,b4) -> (b1,b4,b2,b3)
*/
func verifyCode(v1 uint16, v2 uint32) (uint16, uint32) {
	v11 := ((v1 & 0xFF) << 8) | ((v1 & 0xF000) >> 16) | ((v1 & 0xF00) >> 4)
	v22 := (v2 & 0xFF000000) | ((v2 & 0xFF0000) >> 8) | ((v2 & 0xFF00) >> 8) | ((v2 & 0xFF) << 16)
	return v11, v22
}
```

## 3 Usage

```go

func client() {
    // Get a TCP connection
    conn, err := net.Dial(...)
    if err != nil {
        panic(err)
    }

    // Session of smux
    session := smux.SmuxSession(conn)

    // Open a new stream
    stream, err := session.Open()
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

   // Session of smux
   session := smux.SmuxSession(conn)

    // Accept a stream
    stream, err := session.Accept()
    if err != nil {
        panic(err)
    }

    // Listen for a message
    buf := make([]byte, 4)
    stream.Read(buf)
    stream.Close()
    session.Close()
}
```

## example

网关服务 https://github.com/yddeng/gate