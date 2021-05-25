# smux

`socket multiplexing`, 基于可靠连接`tcp`的多路复用。

基于 `xtaci/smux` 修改

## 缺陷

1. 某一个 `stream` 不读，但对端一直写导致 `tcp` 缓冲区被写满，进而堵塞其他的 `stream` 。

2. 对于`tcp`的超时写，可能已经写了部分数据才超时。 返回到`stream`应该表现出来。由问题1引出，如果设置了写超时，
    本次的写入数据只写入了一部分，返回应用层写入0（但实际tcp会将本次数据写完），如果应用层选择重发，导致数据重复或者混乱。

## 方案

1. 给每个stream 设置读buffer，通过窗口调节发送频率。

    由于通信链路只有一条，而有多条虚拟链路。需保证通信链路畅通，不被任意 `stream` 堵塞。
    通信链路的 buffer 大小固定为 64k。stream 的读写缓冲区大小固定为 512k。

    A端发送数据累计到 `waitConfirm` 中，往对端发送的窗口大小为 `streamWindowSize - waitConfirm`, 
    结果为0 时，B端读缓冲区已满。本端不再发送数据，等待B端读数据。

    B端应用层调用 Read 后，推送已读数据长度 `bufferRead` 。A端 `waitConfirm -= bufferBuffer`, 
    A端的发送窗口不为0，继续发送数据。

2. 已经调用了tcp.Write 的数据默认成功发送（除非tcp连接报错，否则一定能到达对端，即使会堵塞一段时间），如果写超时先到，则放弃本次的写。
