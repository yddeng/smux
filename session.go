package smux

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type Session struct {
	conn *net.TCPConn

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

func newSession(conn *net.TCPConn, netStreamID uint32) *Session {
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

	this.write(newHeader(cmdSYN, sid, 0), nil)
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
			_, _ = this.write(newHeader(cmdFIN, sid, 0), nil)
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

func (this *Session) write(h []byte, b []byte) (n int, err error) {
	defer putHeader(h)
	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	case <-this.chSocketWriteError:
		return 0, this.socketWriteError.Load().(error)
	default:
		this.writeLock.Lock()
		defer this.writeLock.Unlock()
		_, err = this.conn.Write(h)
		if err != nil {
			this.notifyWriteError(err)
			return
		}
		if b != nil && len(b) != 0 {
			n, err = this.conn.Write(b)
			if err != nil {
				this.notifyWriteError(err)
				return
			}
		}
		return
	}
}

func (this *Session) closedStream(sid uint32) {
	this.streamLock.Lock()
	defer this.streamLock.Unlock()
	if _, ok := this.streams[sid]; ok {
		_, _ = this.write(newHeader(cmdFIN, sid, 0), nil)
		delete(this.streams, sid)
	}
}
