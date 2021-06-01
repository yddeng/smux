# 项目设计

基于可靠有序的连接

## 协议包头

cmd (byte) + id (uint16) + length (uint32)

cmd 命令类型：cmdSYN 打开一个stream；cmdFIN  关闭一个Stream；cmdPSH 数据推送；cmdCFM 数据接受确认；

id streamID。

length 视情况而定。 cmdPSH 代表携带的数据长度。cmdCFM 代表确认的数据长度。其他情况为0，无意义。

cmdVRM 特殊的协议， 验证对端是的开启多路复用。id 和 length 为随机数，对端经过算法验证返回两个随机数的确定值。

## StreamID

根据协议包头可知，最大能支持的ID值为65535。

streamID 采用回收复用的方式，避免累加后超过上限值。用位图来存储使用、空闲的ID，减少内存占用。

也意味着同时能开启的stream数量为65535个，超过这个数字就没有可使用的ID了。经测试这个值较为合适，
连接数达到一定阀值，即tcp的传输效率已经到达最大，更多的stream数反而会带来性能瓶颈。这个时候建议多开tcp连接数。