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

// cmd 高1位 为请求回复标记位，0请求1回复
// cmd 高2-4位，错误码，0成功
// cmd 低4-8位，cmd

/*
	cmdSYN : cmd + sid + win
	cmdFIN : cmd + sid + win
	cmdPSH : cmd + sid + len + data
	cmdUPW : cmd + sid + win
*/

const (
	sizeOfCmd  = 1
	sizeOfSid  = 4
	sizeOfLen  = 2
	headerSize = sizeOfCmd + sizeOfSid + sizeOfLen
)

const frameSize = 65535

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
