# smux

`socket multiplexing`, 基于可靠连接`tcp`的多路复用。

基于 `xtaci/smux` 修改，解决某一个 `stream` 不读，但对端一直写导致 `tcp` 缓冲区被写满，进而堵塞其他的 `stream` 。

## Introduce

由于通信链路只有一条，而有多条虚拟链路。需保证通信链路畅通，不被任意 `stream` 堵塞。

通信链路的 buffer 大小固定为 64k。
stream 的读写缓冲区大小固定为 512k。

A端发送数据累计到 `waitConfirm` 中，往对端发送的窗口大小为 `streamWindowSize - waitConfirm`, 结果为0 时，B端读缓冲区已满，等待B端读数据。

B端应用层调用 Read 后，推送已读数据长度 `bufferRead` 。A端 `waitConfirm -= bufferBuffer`, 就能继续发送数据。


