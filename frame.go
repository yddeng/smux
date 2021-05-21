package smux

import (
	"encoding/binary"
)

const (
	cmdSYN byte = iota // stream open
	cmdFIN             // stream close, a.k.a EOF mark
	cmdPSH             // data push
	cmdUPW             // update window size
)

/*
	cmdSYN : streamID
	cmdFIN : streamID
	cmdPSH : streamID + 推送的数据长度 + data
	cmdUPW ：streamID + 读缓存剩余容量
*/

const (
	sizeOfCmd  = 1
	sizeOfSid  = 4
	sizeOfLen  = 2
	headerSize = sizeOfCmd + sizeOfSid + sizeOfLen
)

const frameSize = 65535 - headerSize

type header [headerSize]byte

func (h header) Cmd() byte {
	return h[0]
}

func (h header) StreamID() uint32 {
	return binary.LittleEndian.Uint32(h[1:])
}

func (h header) Length() uint16 {
	return binary.LittleEndian.Uint16(h[5:])
}

func headerBytes(cmd byte, sid uint32, len uint16) []byte {
	hdr := make([]byte, headerSize)
	hdr[0] = cmd
	binary.LittleEndian.PutUint32(hdr[1:], sid)
	binary.LittleEndian.PutUint16(hdr[5:], len)
	return hdr
}

func headerWrite(cmd byte, sid uint32, len uint16, b []byte) {
	b[0] = cmd
	binary.LittleEndian.PutUint32(b[1:], sid)
	binary.LittleEndian.PutUint16(b[5:], len)
}
