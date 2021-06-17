package smux

import (
	"encoding/binary"
	"sync"
	"time"
)

const (
	cmdSYN byte = iota // connection open
	cmdFIN             // connection close, a.k.a EOF mark
	cmdPSH             // data push
	cmdCFM             // number bytes of confirm
	cmdVRM             // verify remote is multiplexed
	cmdPIN             // ping
)

/*
	cmdSYN : connID
	cmdFIN : connID
	cmdPSH : connID + 推送的数据长度 + data
	cmdCFM : connID + 确认的数据长度
	cmdVRM : 随机值 + 随机值
	cmdPIN : 0 + 0
*/

const (
	sizeOfCmd  = 1
	sizeOfSid  = 2
	sizeOfLen  = 4
	headerSize = sizeOfCmd + sizeOfSid + sizeOfLen
)

const frameSize = 64 * 1024

const muxConnWindowSize = 512 * 1024

const (
	pingInterval = time.Second * 10
	pingTimeout  = pingInterval * 10
)

type header [headerSize]byte

func (h header) Cmd() byte {
	return h[0]
}

func (h header) Uint16() uint16 {
	return binary.LittleEndian.Uint16(h[1:])
}

func (h header) Uint32() uint32 {
	return binary.LittleEndian.Uint32(h[3:])
}

var headerGroup = sync.Pool{
	New: func() interface{} {
		return make([]byte, headerSize)
	},
}

func newHeader(cmd byte, id uint16, len uint32) []byte {
	hdr := headerGroup.Get().([]byte)
	//hdr := make([]byte, headerSize)
	hdr[0] = cmd
	binary.LittleEndian.PutUint16(hdr[1:], id)
	binary.LittleEndian.PutUint32(hdr[3:], len)
	return hdr
}

func putHeader(h []byte) {
	headerGroup.Put(h)
}

func notifyEvent(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

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
