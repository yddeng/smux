package smux

import (
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

	socketReadError      atomic.Value
	socketWriteError     atomic.Value
	chSocketReadError    chan struct{}
	chSocketWriteError   chan struct{}
	socketReadErrorOnce  sync.Once
	socketWriteErrorOnce sync.Once

	chClose   chan struct{}
	closeOnce sync.Once

	writeLock sync.Mutex

	aioService       *AIOService // epoll
	aioServiceLocker sync.Mutex
}

func newSession(conn net.Conn) *Session {
	sess := &Session{
		conn:                 conn,
		idAlloc:              NewIDBitmap(),
		streams:              map[uint16]*Stream{},
		waitFin:              map[uint16]struct{}{},
		streamLock:           sync.Mutex{},
		chAccepts:            make(chan *Stream, 1024),
		socketReadError:      atomic.Value{},
		socketWriteError:     atomic.Value{},
		chSocketReadError:    make(chan struct{}),
		chSocketWriteError:   make(chan struct{}),
		socketReadErrorOnce:  sync.Once{},
		socketWriteErrorOnce: sync.Once{},
		chClose:              make(chan struct{}),
		closeOnce:            sync.Once{},
	}
	sess.idAlloc.Set(0) // use 0
	go sess.readLoop()
	return sess
}

func (this *Session) Accept() (*Stream, error) {
	select {
	case stream := <-this.chAccepts:
		return stream, nil
	case <-this.chSocketReadError:
		return nil, this.socketReadError.Load().(error)
	case <-this.chClose:
		return nil, ErrClosedPipe
	}
}

func (this *Session) Open() (*Stream, error) {

	select {
	case <-this.chClose:
		return nil, ErrClosedPipe
	case <-this.chSocketWriteError:
		return nil, this.socketWriteError.Load().(error)
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

func (this *Session) Close() error {
	var once bool
	this.closeOnce.Do(func() {
		close(this.chClose)
		once = true
	})

	if once {
		this.sessionClose(true)
		return this.conn.Close()
	} else {
		return io.ErrClosedPipe
	}
}

func (this *Session) sessionClose(notify bool) {
	this.streamLock.Lock()
	for sid := range this.streams {
		this.streams[sid].fin()
		this.preparseCmd(sid, cmdFIN)
		if notify {
			this.writeHeader(cmdFIN, sid, 0)
		}
	}
	this.streams = map[uint16]*Stream{}
	this.streamLock.Unlock()
}

func (this *Session) notifyReadError(err error) {
	this.socketReadErrorOnce.Do(func() {
		this.socketReadError.Store(err)
		close(this.chSocketReadError)
		this.sessionClose(false)
	})
}

func (this *Session) notifyWriteError(err error) {
	this.socketWriteErrorOnce.Do(func() {
		this.socketWriteError.Store(err)
		close(this.chSocketWriteError)
		this.sessionClose(false)
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
						this.notifyReadError(err)
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
			default:
				this.notifyReadError(ErrInvalidCmd)
				return
			}
		} else {
			this.notifyReadError(err)
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
	b     []byte
	doing int32
}

func (this *Session) doWrite(req *writeRequest) <-chan *writeResult {
	this.writeLock.Lock()
	defer this.writeLock.Unlock()

	retch := make(chan *writeResult, 1)
	if !atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
		// 已经超时返回，不再执行
		return retch
	}

	// 已经获取了锁，本次数据必定发往对端（tcp无错）

	/*
		不将超时设置到conn上，可能情况:
		header 写入完成，data 超时。导致对端拆包出错。
	*/

	ret := new(writeResult)
	if _, ret.err = this.conn.Write(newHeader(cmdPSH, req.sid, uint32(len(req.b)))); ret.err == nil {
		ret.n, ret.err = this.conn.Write(req.b)
	}

	if ret.err != nil {
		this.notifyWriteError(ret.err)
	}
	retch <- ret
	return retch
}

// 仅返回写入的数据长度
func (this *Session) writeData(sid uint16, b []byte, deadline <-chan time.Time) (n int, err error) {
	req := &writeRequest{sid: sid, b: b, doing: 0}

	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	case <-this.chSocketWriteError:
		return 0, this.socketWriteError.Load().(error)
	case <-deadline:
		if atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
			return 0, ErrTimeout
		} else {
			return len(b), ErrTimeout
		}
	case ret := <-this.doWrite(req):
		return ret.n, ret.err
	}
}

func (this *Session) writeHeader(cmd byte, sid uint16, length uint32) {
	this.writeLock.Lock()
	defer this.writeLock.Unlock()
	select {
	case <-this.chClose:
		return
	case <-this.chSocketWriteError:
		return
	default:
		_, err := this.conn.Write(newHeader(cmd, sid, length))
		if err != nil {
			this.notifyWriteError(err)
		}
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
