package smux

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Session struct {
	conn net.Conn

	nextStreamID     uint32
	nextStreamIDLock sync.Mutex

	streams    map[uint32]*Stream
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
}

func newSession(conn net.Conn, netStreamID uint32) *Session {
	sess := &Session{
		conn:                 conn,
		nextStreamID:         netStreamID,
		nextStreamIDLock:     sync.Mutex{},
		streams:              map[uint32]*Stream{},
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
	this.nextStreamIDLock.Lock()
	sid := this.nextStreamID
	this.nextStreamID += 2
	this.nextStreamIDLock.Unlock()
	s := newStream(sid, this)

	this.streamLock.Lock()
	this.streams[sid] = s
	this.streamLock.Unlock()

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

func (this *Session) Close() error {
	var once bool
	this.closeOnce.Do(func() {
		close(this.chClose)
		once = true
	})

	if once {
		this.streamLock.Lock()
		for sid := range this.streams {
			this.streams[sid].fin()
			this.writeHeader(cmdFIN, sid, 0)
		}
		this.streamLock.Unlock()
		return this.conn.Close()
	} else {
		return io.ErrClosedPipe
	}
}

func (this *Session) notifyReadError(err error) {
	this.socketReadErrorOnce.Do(func() {
		this.socketReadError.Store(err)
		close(this.chSocketReadError)
	})
}

func (this *Session) notifyWriteError(err error) {
	this.socketWriteErrorOnce.Do(func() {
		this.socketWriteError.Store(err)
		close(this.chSocketWriteError)
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
				if stream, ok := this.streams[sid]; ok {
					stream.fin()
					delete(this.streams, sid)
				}
				this.streamLock.Unlock()
			case cmdPSH:
				if length > 0 {
					if _, err := io.ReadFull(this.conn, buffer[:length]); err == nil {
						this.streamLock.Lock()
						if stream, ok := this.streams[sid]; ok {
							stream.pushBytes(buffer[:length])
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
				}
				this.streamLock.Unlock()
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
	sid   uint32
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

	_, err := this.conn.Write(newHeader(cmdPSH, req.sid, uint32(len(req.b))))
	if err != nil {
		retch <- &writeResult{err: err}
		return retch
	}

	n, err := this.conn.Write(req.b)
	if err != nil {
		retch <- &writeResult{n: n, err: err}
		return retch
	}

	retch <- &writeResult{n: n}
	return retch
}

// 仅返回写入的数据长度
func (this *Session) writeData(sid uint32, b []byte, deadline <-chan time.Time) (n int, err error) {
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
		if ret.err != nil {
			this.notifyWriteError(err)
		}
		return ret.n, ret.err
	}
}

func (this *Session) writeHeader(cmd byte, sid uint32, length uint32) {
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

func (this *Session) closedStream(sid uint32) {
	this.streamLock.Lock()
	defer this.streamLock.Unlock()
	if _, ok := this.streams[sid]; ok {
		this.writeHeader(cmdFIN, sid, 0)
		delete(this.streams, sid)
	}
}
