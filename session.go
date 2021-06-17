package smux

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Session struct {
	conn    net.Conn
	idAlloc *idBitmap

	streams    map[uint16]*Stream
	waitFin    map[uint16]struct{}
	streamLock sync.Mutex

	chAccepts chan *Stream

	chClose     chan struct{}
	closeOnce   sync.Once
	closeReason atomic.Value

	chWrite chan *writeRequest

	lastPingTime atomic.Value // time.Time

	aioService       *AIOService // epoll
	aioServiceLocker sync.Mutex
}

func newSession(conn net.Conn) *Session {
	sess := &Session{
		conn:       conn,
		idAlloc:    NewIDBitmap(),
		streams:    map[uint16]*Stream{},
		waitFin:    map[uint16]struct{}{},
		streamLock: sync.Mutex{},
		chAccepts:  make(chan *Stream, 1024),
		chClose:    make(chan struct{}),
		closeOnce:  sync.Once{},
		chWrite:    make(chan *writeRequest),
	}
	sess.lastPingTime.Store(time.Now())

	go sess.readLoop()
	go sess.writeLoop()
	go sess.ping()
	return sess
}

func (this *Session) Accept() (*Stream, error) {
	select {
	case stream := <-this.chAccepts:
		return stream, nil
	case <-this.chClose:
		return nil, this.closeReason.Load().(error)
	}
}

func (this *Session) Open() (*Stream, error) {
	select {
	case <-this.chClose:
		return nil, this.closeReason.Load().(error)
	default:
	}

	this.streamLock.Lock()
	defer this.streamLock.Unlock()

	sid, err := this.idAlloc.Get()
	if err != nil {
		return nil, err
	}

	s := newStream(sid, this)
	this.streams[sid] = s

	this.writeHeader(cmdSYN, sid, 0)

	return s, nil
}

// IsClosed does a safe check to see if we have shutdown
func (this *Session) IsClosed() bool {
	select {
	case <-this.chClose:
		return true
	default:
		return false
	}
}

func (this *Session) NumStream() int {
	select {
	case <-this.chClose:
		return 0
	default:
		this.streamLock.Lock()
		defer this.streamLock.Unlock()
		return len(this.streams)
	}
}

func (this *Session) GetStream(streamID uint16) *Stream {
	select {
	case <-this.chClose:
		return nil
	default:
		this.streamLock.Lock()
		defer this.streamLock.Unlock()
		return this.streams[streamID]
	}
}

func (this *Session) Addr() net.Addr {
	return this.conn.LocalAddr()
}

func (this *Session) Close() {
	this.close(errors.New("closed smux. "))
}

func (this *Session) close(err error) {
	this.closeOnce.Do(func() {
		this.closeReason.Store(err)
		close(this.chClose)
		this.conn.Close()

		this.streamLock.Lock()
		for sid := range this.streams {
			this.streams[sid].fin()
			this.preparseCmd(sid, cmdFIN)
		}
		this.streams = map[uint16]*Stream{}
		this.streamLock.Unlock()
	})
}

func (this *Session) readLoop() {
	var (
		hdr    header
		buffer = make([]byte, frameSize)
	)
	for {
		// read header first
		if _, err := io.ReadFull(this.conn, hdr[:]); err == nil {
			cmd := hdr.Cmd()
			sid := hdr.StreamID()
			length := hdr.Length()
			switch cmd {
			case cmdSYN:
				this.streamLock.Lock()
				if _, ok := this.streams[sid]; !ok {
					this.idAlloc.Set(sid)
					stream := newStream(sid, this)
					this.streams[sid] = stream
					select {
					case this.chAccepts <- stream:
					case <-this.chClose:
					}
				}
				this.streamLock.Unlock()
			case cmdFIN:
				this.streamLock.Lock()
				if _, ok := this.waitFin[sid]; ok {
					/*
					 1. A端关闭，B端执行stream.fin()后返回fin。
					 2. 两端同时关闭，本端fin还未到达对端，就收到对端的fin。
					*/
					this.idAlloc.Put(sid)
					delete(this.streams, sid)
					delete(this.waitFin, sid)
				} else if stream, ok := this.streams[sid]; ok {
					// 对端关闭
					stream.fin()
					delete(this.streams, sid)
					this.writeHeader(cmdFIN, sid, 0)
					this.preparseCmd(sid, cmdFIN)
				}
				this.streamLock.Unlock()
			case cmdPSH:
				if length > 0 {
					if _, err := io.ReadFull(this.conn, buffer[:length]); err == nil {
						this.streamLock.Lock()
						if stream, ok := this.streams[sid]; ok {
							stream.pushBytes(buffer[:length])
							this.preparseCmd(stream.streamID, cmdPSH) // epoll
						}
						this.streamLock.Unlock()
					} else {
						this.close(err)
						return
					}
				}
			case cmdCFM:
				this.streamLock.Lock()
				if stream, ok := this.streams[sid]; ok {
					stream.bytesConfirm(length)
					this.preparseCmd(stream.streamID, cmdCFM) // epoll
				}
				this.streamLock.Unlock()
			case cmdVRM:
				v11, v22 := verifyCode(sid, length)
				this.writeHeader(cmdVRM, v11, v22)
			case cmdPIN:
				this.lastPingTime.Store(time.Now())
			default:
				this.close(errors.New("invalid command. "))
				return
			}
		} else {
			this.close(err)
			return
		}
	}
}

type writeResult struct {
	n   int
	err error
}

type writeRequest struct {
	sid   uint16
	hdr   []byte
	d     []byte
	doing int32
	done  chan *writeResult
}

func (this *Session) writeLoop() {
	for {
		select {
		case <-this.chClose:
			return
		case req := <-this.chWrite:
			if !atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
				// 已经超时返回，不再执行
				return
			}

			// 本次数据必定发往对端（tcp无错）
			/*
				不将超时设置到conn上，可能情况:
				header 写入完成，data 超时。导致对端拆包出错。
			*/

			ret := new(writeResult)
			if _, ret.err = this.conn.Write(req.hdr); ret.err == nil && req.d != nil {
				ret.n, ret.err = this.conn.Write(req.d)
			}
			putHeader(req.hdr)

			req.done <- ret
			close(req.done)

			if ret.err != nil {
				this.close(ret.err)
				return
			}
		}
	}
}

func (this *Session) ping() {
	timer := time.NewTimer(pingInterval)
	defer timer.Stop()
	for {
		select {
		case <-this.chClose:
			return
		case now := <-timer.C:
			this.writeHeader(cmdPIN, 0, 0)

			// check
			lastPing := this.lastPingTime.Load().(time.Time)
			if now.Sub(lastPing) > pingTimeout {
				this.close(errors.New("smux ping timeout. "))
				return
			}

			timer.Reset(pingInterval)
		}
	}
}

// 仅返回写入的数据长度
func (this *Session) writeData(sid uint16, b []byte, deadline <-chan time.Time) (n int, err error) {
	req := &writeRequest{
		sid: sid, d: b,
		hdr:   newHeader(cmdPSH, sid, uint32(len(b))),
		doing: 0,
		done:  make(chan *writeResult, 1),
	}

	select {
	case <-this.chClose:
		return 0, this.closeReason.Load().(error)
	case this.chWrite <- req:
	case <-deadline:
		if atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
			return 0, ErrTimeout
		} else {
			return len(b), ErrTimeout
		}
	}

	select {
	case <-this.chClose:
		return 0, this.closeReason.Load().(error)
	case <-deadline:
		if atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
			return 0, ErrTimeout
		} else {
			return len(b), ErrTimeout
		}
	case ret := <-req.done:
		return ret.n, ret.err
	}
}

func (this *Session) writeHeader(cmd byte, sid uint16, length uint32) {
	req := &writeRequest{
		sid:   sid,
		hdr:   newHeader(cmdPSH, sid, length),
		doing: 0,
		done:  make(chan *writeResult, 1),
	}

	select {
	case <-this.chClose:
	case this.chWrite <- req:
	}
}

func (this *Session) closedStream(sid uint16) {
	this.streamLock.Lock()
	defer this.streamLock.Unlock()
	if _, ok := this.streams[sid]; ok {
		this.waitFin[sid] = struct{}{}
		this.writeHeader(cmdFIN, sid, 0)
		this.preparseCmd(sid, cmdFIN)
	}
}
