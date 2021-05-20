package smux

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type Session struct {
	conn *net.TCPConn

	nextStreamID     uint32 // next stream identifier
	nextStreamIDLock sync.Mutex

	streams    map[uint32]*Stream
	streamLock sync.Mutex

	chWriten chan []byte

	chAccepts chan *Stream

	socketReadError      atomic.Value
	socketWriteError     atomic.Value
	chSocketReadError    chan struct{}
	chSocketWriteError   chan struct{}
	socketReadErrorOnce  sync.Once
	socketWriteErrorOnce sync.Once

	die     chan struct{}
	dieOnce sync.Once
}

func newSession(conn *net.TCPConn, netStreamID uint32) *Session {
	sess := &Session{
		conn:                 conn,
		nextStreamID:         netStreamID,
		nextStreamIDLock:     sync.Mutex{},
		streams:              map[uint32]*Stream{},
		streamLock:           sync.Mutex{},
		chWriten:             make(chan []byte, 1024),
		chAccepts:            make(chan *Stream, 1024),
		socketReadError:      atomic.Value{},
		socketWriteError:     atomic.Value{},
		chSocketReadError:    make(chan struct{}),
		chSocketWriteError:   make(chan struct{}),
		socketReadErrorOnce:  sync.Once{},
		socketWriteErrorOnce: sync.Once{},
		die:                  make(chan struct{}),
		dieOnce:              sync.Once{},
	}

	go sess.readLoop()
	go sess.writeLoop()
	return sess
}

func (this *Session) Accept() (*Stream, error) {
	select {
	case stream := <-this.chAccepts:
		return stream, nil
	case <-this.chSocketReadError:
		return nil, this.socketReadError.Load().(error)
	case <-this.die:
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

	this.pushWrite(headerBytes(cmdSYN, sid, 0))
	return s, nil
}

// IsClosed does a safe check to see if we have shutdown
func (s *Session) IsClosed() bool {
	select {
	case <-s.die:
		return true
	default:
		return false
	}
}

func (s *Session) Close() error {
	var once bool
	s.dieOnce.Do(func() {
		close(s.die)
		once = true
	})

	if once {
		s.streamLock.Lock()
		for k := range s.streams {
			s.streams[k].fin()
		}
		s.streamLock.Unlock()
		return s.conn.Close()
	} else {
		return io.ErrClosedPipe
	}
}

func (s *Session) notifyReadError(err error) {
	s.socketReadErrorOnce.Do(func() {
		s.socketReadError.Store(err)
		close(s.chSocketReadError)
	})
}

func (s *Session) notifyWriteError(err error) {
	s.socketWriteErrorOnce.Do(func() {
		s.socketWriteError.Store(err)
		close(s.chSocketWriteError)
	})
}

func (s *Session) readLoop() {
	var (
		hdr    header
		buffer = make([]byte, frameSize)
	)
	for {
		// read header first
		if _, err := io.ReadFull(s.conn, hdr[:]); err == nil {
			cmd := hdr.Cmd()
			sid := hdr.StreamID()
			length := hdr.Length()
			switch cmd {
			case cmdSYN:
				s.streamLock.Lock()
				if _, ok := s.streams[sid]; !ok {
					stream := newStream(sid, s)
					s.streams[sid] = stream
					select {
					case s.chAccepts <- stream:
					case <-s.die:
					}
				}
				s.streamLock.Unlock()
			case cmdFIN:
				s.streamLock.Lock()
				if stream, ok := s.streams[sid]; ok {
					stream.fin()
				}
				s.streamLock.Unlock()
			case cmdPSH:
				if length > 0 {
					if _, err := io.ReadFull(s.conn, buffer[:length]); err == nil {
						s.streamLock.Lock()
						if stream, ok := s.streams[sid]; ok {
							stream.pushBytes(buffer[:length])
						} else {
							s.pushWrite(headerBytes(cmdFIN, sid, 0))
						}
						s.streamLock.Unlock()
					} else {
						s.notifyReadError(err)
						return
					}
				}
			case cmdUPW:
				s.streamLock.Lock()
				if stream, ok := s.streams[sid]; ok {
					s.streamLock.Unlock()
					stream.updateWindows(length)
				} else {
					s.streamLock.Unlock()
				}
			default:
				s.notifyReadError(ErrInvalidCmd)
				return
			}
		} else {
			s.notifyReadError(err)
			return
		}
	}
}

func (this *Session) writeLoop() {
	for {
		select {
		case data := <-this.chWriten:
			_, err := this.conn.Write(data)
			if err != nil {
				this.notifyWriteError(err)
				return
			}
		}
	}
}

func (this *Session) pushWrite(b []byte) {
	this.chWriten <- b
}

func (this *Session) closeStream(sid uint32) {
	this.streamLock.Lock()
	defer this.streamLock.Unlock()
	if _, ok := this.streams[sid]; ok {
		this.pushWrite(headerBytes(cmdFIN, sid, 0))
		delete(this.streams, sid)
	}
}
