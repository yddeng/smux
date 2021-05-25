package smux

import (
	"encoding/binary"
	"sync"
)

const (
	cmdSYN byte = iota // stream open
	cmdFIN             // stream close, a.k.a EOF mark
	cmdPSH             // data push
	cmdCFM             // number bytes of confirm
	cmdVRM             // verify remote is multiplexed
)

/*
	cmdSYN : streamID
	cmdFIN : streamID
	cmdPSH : streamID + 推送的数据长度 + data
	cmdCFM : streamID + 确认的数据长度
	cmdVRM : 随机值 + 随机值
*/

const (
	sizeOfCmd  = 1
	sizeOfSid  = 4
	sizeOfLen  = 4
	headerSize = sizeOfCmd + sizeOfSid + sizeOfLen
)

const frameSize = 64 * 1024

type header [headerSize]byte

func (h header) Cmd() byte {
	return h[0]
}

func (h header) StreamID() uint32 {
	return binary.LittleEndian.Uint32(h[1:])
}

func (h header) Length() uint32 {
	return binary.LittleEndian.Uint32(h[5:])
}

var headerGroup = sync.Pool{
	New: func() interface{} {
		return make([]byte, headerSize)
	},
}

func newHeader(cmd byte, sid uint32, len uint32) []byte {
	//hdr := headerGroup.Get().([]byte)
	hdr := make([]byte, headerSize)
	hdr[0] = cmd
	binary.LittleEndian.PutUint32(hdr[1:], sid)
	binary.LittleEndian.PutUint32(hdr[5:], len)
	return hdr
}

func putHeader(h []byte) {
	headerGroup.Put(h)
}

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
