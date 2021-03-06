package smux

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type MuxSession struct {
	conn    net.Conn
	idAlloc *idBitmap

	connections map[uint16]*MuxConn
	connWaitFin map[uint16]struct{}
	connLock    sync.Mutex

	chAccepts chan *MuxConn

	chClose     chan struct{}
	closeOnce   sync.Once
	closeReason atomic.Value

	chReqQueue    chan *writeRequest
	buffer        []byte
	br, bw        int
	bufferLock    sync.Mutex
	chBufferWrite chan struct{}
	chBufferRead  chan struct{}

	lastPingTime atomic.Value // time.Time

	aioService       *AIOService // epoll
	aioServiceLocker sync.Mutex
}

func NewMuxSession(conn net.Conn) *MuxSession {
	mux := &MuxSession{
		conn:          conn,
		idAlloc:       NewIDBitmap(),
		connections:   map[uint16]*MuxConn{},
		connWaitFin:   map[uint16]struct{}{},
		connLock:      sync.Mutex{},
		chAccepts:     make(chan *MuxConn, 1024),
		chClose:       make(chan struct{}),
		closeOnce:     sync.Once{},
		chReqQueue:    make(chan *writeRequest),
		buffer:        make([]byte, muxConnWindowSize),
		chBufferRead:  make(chan struct{}, 1),
		chBufferWrite: make(chan struct{}, 1),
	}
	mux.lastPingTime.Store(time.Now())

	go mux.readLoop()
	go mux.writeLoop()
	go mux.writeR()
	go mux.keepalive()
	return mux
}

func (this *MuxSession) Accept() (*MuxConn, error) {
	select {
	case conn := <-this.chAccepts:
		return conn, nil
	case <-this.chClose:
		return nil, this.closeReason.Load().(error)
	}
}

func (this *MuxSession) Open() (*MuxConn, error) {
	select {
	case <-this.chClose:
		return nil, this.closeReason.Load().(error)
	default:
	}

	this.connLock.Lock()
	defer this.connLock.Unlock()

	cid, err := this.idAlloc.Get()
	if err != nil {
		return nil, err
	}

	c := newMuxConn(cid, this)
	this.connections[cid] = c

	this.writeHeader(cmdSYN, cid, 0)

	return c, nil
}

// IsClosed does a safe check to see if we have shutdown
func (this *MuxSession) IsClosed() bool {
	select {
	case <-this.chClose:
		return true
	default:
		return false
	}
}

func (this *MuxSession) NumMuxConn() int {
	select {
	case <-this.chClose:
		return 0
	default:
		this.connLock.Lock()
		defer this.connLock.Unlock()
		return len(this.connections)
	}
}

func (this *MuxSession) GetMuxConn(connID uint16) *MuxConn {
	select {
	case <-this.chClose:
		return nil
	default:
		this.connLock.Lock()
		defer this.connLock.Unlock()
		return this.connections[connID]
	}
}

func (this *MuxSession) Addr() net.Addr {
	return this.conn.LocalAddr()
}

func (this *MuxSession) Close() {
	this.close(errors.New("closed smux. "))
}

func (this *MuxSession) close(err error) {
	this.closeOnce.Do(func() {
		this.closeReason.Store(err)
		close(this.chClose)
		this.conn.Close()

		this.connLock.Lock()
		for cid := range this.connections {
			this.connections[cid].fin()
			this.preparseCmd(cid, cmdFIN)
		}
		this.connections = map[uint16]*MuxConn{}
		this.connLock.Unlock()

		this.aioServiceLocker.Lock()
		if this.aioService != nil {
			this.aioService.close()
		}
		this.aioServiceLocker.Unlock()
	})
}

func (this *MuxSession) readLoop() {
	var (
		buffer = make([]byte, muxConnWindowSize)
		r, w   int
	)
	for {
		if n, err := this.conn.Read(buffer[w:]); err != nil {
			this.close(err)
			return
		} else {
			w += n
		loop:
			for w-r >= headerSize {
				cmd, cid, length := unpackHeader(buffer[r : r+headerSize])
				r += headerSize

				switch cmd {
				case cmdSYN:
					this.connLock.Lock()
					if _, ok := this.connections[cid]; !ok {
						this.idAlloc.Set(cid)
						conn := newMuxConn(cid, this)
						this.connections[cid] = conn
						select {
						case this.chAccepts <- conn:
						case <-this.chClose:
						}
					}
					this.connLock.Unlock()
				case cmdFIN:
					this.connLock.Lock()
					if _, ok := this.connWaitFin[cid]; ok {
						/*
						 1. A????????????B?????????conn.fin()?????????fin???
						 2. ???????????????????????????fin???????????????????????????????????????fin???
						*/
						this.idAlloc.Put(cid)
						delete(this.connections, cid)
						delete(this.connWaitFin, cid)
					} else if conn, ok := this.connections[cid]; ok {
						// ????????????
						conn.fin()
						delete(this.connections, cid)
						this.writeHeader(cmdFIN, cid, 0)
						this.preparseCmd(cid, cmdFIN)
					}
					this.connLock.Unlock()
				case cmdPSH:
					if w-r >= int(length) {
						this.connLock.Lock()
						if conn, ok := this.connections[cid]; ok {
							conn.pushBytes(buffer[r : r+int(length)])
							this.preparseCmd(conn.connID, cmdPSH) // epoll
						}
						this.connLock.Unlock()
						r += int(length)
					} else {
						r -= headerSize
						break loop
					}
				case cmdCFM:
					this.connLock.Lock()
					if conn, ok := this.connections[cid]; ok {
						conn.bytesConfirm(length)
						this.preparseCmd(conn.connID, cmdCFM) // epoll
					}
					this.connLock.Unlock()
				case cmdVRM:
					v11, v22 := verifyCode(cid, length)
					this.writeHeader(cmdVRM, v11, v22)
				case cmdPIN:
					this.lastPingTime.Store(time.Now())
				default:
					this.close(errors.New("invalid command. "))
					return
				}
			}

			if r != 0 {
				w = copy(buffer[0:], buffer[r:w])
				r = 0
			}
		}

	}
}

type writeRequest struct {
	cmd   byte
	v1    uint16
	v2    uint32
	b     []byte
	doing int32
	done  chan struct{}
}

func (this *MuxSession) writeR() {
	for {
		select {
		case <-this.chClose:
			return
		case req := <-this.chReqQueue:
			length := headerSize + len(req.b)

			this.bufferLock.Lock()
			for this.bw+length > muxConnWindowSize {
				this.bufferLock.Unlock()
				// notify
				notifyEvent(this.chBufferWrite)

				select {
				case <-this.chClose:
					return
				case <-this.chBufferRead:
					this.bufferLock.Lock()
				}
			}

			if !atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
				// ?????????????????????????????????
				this.bufferLock.Unlock()
				break
			}
			close(req.done)

			packHeader(this.buffer[this.bw:], req.cmd, req.v1, req.v2)
			if len(req.b) > 0 {
				copy(this.buffer[this.bw+headerSize:], req.b)
			}

			this.bw += length
			this.bufferLock.Unlock()

			notifyEvent(this.chBufferWrite)
		}
	}
}

func (this *MuxSession) writeLoop() {
	for {
		select {
		case <-this.chClose:
			return
		case <-this.chBufferWrite:
			this.bufferLock.Lock()
			if this.bw-this.br == 0 {
				this.bufferLock.Unlock()
				break
			}

			data := this.buffer[this.br:this.bw]
			this.bufferLock.Unlock()

			n, err := this.conn.Write(data)
			if err != nil {
				this.close(err)
				return
			}

			this.bufferLock.Lock()
			this.br += n
			if this.bw > muxConnWindowSize/2 && this.br != 0 {
				this.bw = copy(this.buffer, this.buffer[this.br:this.bw])
				this.br = 0
			}
			this.bufferLock.Unlock()
			notifyEvent(this.chBufferRead)

		}

	}
}

//func (this *MuxSession) writeLoop() {
//	for {
//		select {
//		case <-this.chClose:
//			return
//		case req := <-this.chReqQueue:
//			if !atomic.CompareAndSwapInt32(&req.doing, 0, 1) {
//				// ?????????????????????????????????
//				return
//			}
//
//			// ?????????????????????????????????tcp?????????
//			/*
//				?????????????????????conn??????????????????:
//				header ???????????????data ????????????????????????????????????
//			*/
//
//			data := make([]byte, headerSize+len(req.b))
//
//			packHeader(data, req.cmd, req.v1, req.v2)
//			if len(req.b) > 0 {
//				copy(data[headerSize:], req.b)
//			}
//
//			ret := new(writeResult)
//			ret.n, ret.err = this.conn.Write(data)
//			ret.n -= headerSize
//
//			req.done <- ret
//			close(req.done)
//
//			if ret.err != nil {
//				this.close(ret.err)
//				return
//			}
//		}
//	}
//}

func (this *MuxSession) keepalive() {
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

// ??????????????????????????????
func (this *MuxSession) writeData(cid uint16, b []byte, deadline <-chan time.Time) (n int, err error) {
	req := &writeRequest{
		cmd: cmdPSH, v1: cid, v2: uint32(len(b)),
		b: b, done: make(chan struct{}),
	}

	select {
	case <-this.chClose:
		return 0, this.closeReason.Load().(error)
	case this.chReqQueue <- req:
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
	case <-req.done:
		return len(b), nil
	}
}

func (this *MuxSession) writeHeader(cmd byte, cid uint16, length uint32) {
	req := &writeRequest{
		cmd: cmd, v1: cid, v2: length,
		done: make(chan struct{}),
	}

	select {
	case <-this.chClose:
	case this.chReqQueue <- req:
	}
}

func (this *MuxSession) closedMuxConn(cid uint16) {
	this.connLock.Lock()
	defer this.connLock.Unlock()
	if _, ok := this.connections[cid]; ok {
		this.connWaitFin[cid] = struct{}{}
		this.writeHeader(cmdFIN, cid, 0)
		this.preparseCmd(cid, cmdFIN)
	}
}
