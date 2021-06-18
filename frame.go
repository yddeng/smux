package smux

import (
	"encoding/binary"
	"errors"
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

var (
	ErrTimeout    = errors.New("timeout. ")
	ErrClosedPipe = errors.New("the stream has closed. ")
	ErrBrokenPipe = errors.New("write on closed stream. ")
)

func packHeader(buf []byte, cmd byte, v1 uint16, v2 uint32) {
	buf[0] = cmd
	binary.LittleEndian.PutUint16(buf[1:], v1)
	binary.LittleEndian.PutUint32(buf[3:], v2)
}

func unpackHeader(data []byte) (cmd byte, v1 uint16, v2 uint32) {
	cmd = data[0]
	v1 = binary.LittleEndian.Uint16(data[1:])
	v2 = binary.LittleEndian.Uint32(data[3:])
	return
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
