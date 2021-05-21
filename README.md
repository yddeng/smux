# smux

`socket multiplexing`, 基于可靠连接`tcp`的多路复用。

基于 `xtaci/smux` 修改，解决某一个 `stream` 不读，但对端一直写导致 `tcp` 缓冲区被写满，进而堵塞其他的 `stream` 。

## Introduce

由于通信链路只有一条，而有多条虚拟链路。需保证通信链路畅通，不被任意 stream 堵塞。

stream端设置读缓冲区

在 stream 端设置读缓冲区，stream 间的数据投递采用响应式，响应结果中带上对端读缓冲区的剩余容量。
本端更新缓冲区容量，同时状态置为可发送状态。

通信链路的 buffer 大小固定为 64k。
stream 的读写缓冲区大小固定为 512k。

    


