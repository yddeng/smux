package smux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const streamWindowSize = 1024 * 512

type Stream struct {
	session  *Session
	streamID uint32

	readBuffer     *Buffer
	readBufferLock sync.Mutex

	writeBuffer     *Buffer
	writeBufferLock sync.Mutex

	// notify a read/write event
	chReadEvent  chan struct{}
	chWriteEvent chan struct{}

	// do write
	isWriting bool
	rWindow   uint16
	sendLock  sync.Mutex

	// deadlines
	readDeadline  atomic.Value
	writeDeadline atomic.Value

	// 主动关闭调用 close
	chClose   chan struct{}
	closeOnce sync.Once

	// 被动关闭调用 fin
	chFin   chan struct{}
	finOnce sync.Once
}

func newStream(sid uint32, s *Session) *Stream {
	return &Stream{
		session:         s,
		streamID:        sid,
		readBuffer:      New(streamWindowSize),
		readBufferLock:  sync.Mutex{},
		writeBuffer:     New(streamWindowSize),
		writeBufferLock: sync.Mutex{},
		chReadEvent:     make(chan struct{}, 1),
		chWriteEvent:    make(chan struct{}, 1),
		isWriting:       false,
		rWindow:         frameSize,
		sendLock:        sync.Mutex{},
		readDeadline:    atomic.Value{},
		writeDeadline:   atomic.Value{},
		chClose:         make(chan struct{}),
		closeOnce:       sync.Once{},
		chFin:           make(chan struct{}),
		finOnce:         sync.Once{},
	}
}

func (this *Stream) StreamID() uint32 {
	return this.streamID
}

func (this *Stream) Read(b []byte) (n int, err error) {
	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	default:
	}

	for {
		if n, err = this.tryRead(b); err != nil {
			return 0, err
		} else if n == -1 {
			if err = this.waitRead(); err != nil {
				return 0, err
			}
		} else {
			return
		}
	}
}

func (this *Stream) tryRead(b []byte) (n int, err error) {
	if len(b) == 0 {
		return
	}

	this.readBufferLock.Lock()
	defer this.readBufferLock.Unlock()

	if this.readBuffer.Empty() {
		return -1, nil
	} else {
		needRSH := streamWindowSize-this.readBuffer.Len() <= 0
		n, err = this.readBuffer.Read(b)
		wind := streamWindowSize - this.readBuffer.Len()
		if err == nil && wind > 0 && needRSH {
			if wind > frameSize {
				wind = frameSize
			}
			this.session.pushWrite(headerBytes(cmdRSH, this.streamID, uint16(wind)))
		}
		return
	}
}

func (this *Stream) waitRead() error {
	var timer *time.Timer
	var deadline <-chan time.Time
	if d, ok := this.readDeadline.Load().(time.Time); ok && !d.IsZero() {
		timer = time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}

	select {
	case <-this.chReadEvent:
		return nil
	case <-this.chFin:
		this.readBufferLock.Lock()
		defer this.readBufferLock.Unlock()
		if !this.readBuffer.Empty() {
			return nil
		}
		return io.EOF
	case <-this.session.chSocketReadError:
		return this.session.socketReadError.Load().(error)
	case <-deadline:
		return ErrTimeout
	case <-this.chClose:
		return ErrClosedPipe
	}
}

func (this *Stream) pushBytes(b []byte) {
	select {
	case <-this.chClose:
		this.session.pushWrite(headerBytes(newCmd(cmdPSH, err_broken_pipe, true), this.streamID, 0))
		return
	default:
	}

	this.readBufferLock.Lock()
	if this.readBuffer.Len()+len(b) > this.readBuffer.Cap() {
		this.readBuffer.Grow(len(b))
	}
	this.readBuffer.Write(b)
	wind := streamWindowSize - this.readBuffer.Len()
	this.readBufferLock.Unlock()

	notifyEvent(this.chReadEvent)

	if wind <= 0 {
		wind = 0
	}
	if wind > frameSize {
		wind = frameSize
	}
	this.session.pushWrite(headerBytes(newCmd(cmdPSH, err_ok, true), this.streamID, uint16(wind)))
}

func (this *Stream) Write(b []byte) (n int, err error) {
	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	default:
	}

	var timer *time.Timer
	var deadline <-chan time.Time
	if d, ok := this.writeDeadline.Load().(time.Time); ok && !d.IsZero() {
		timer = time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}

	sentb := b
	blen := len(sentb)
	for n < blen {
		this.writeBufferLock.Lock()
		if this.writeBuffer.Full() {
			this.writeBufferLock.Unlock()
			fmt.Println("write wait")
			select {
			case <-this.chWriteEvent:
			case <-this.chFin:
				return 0, ErrBrokenPipe
			case <-this.session.chSocketWriteError:
				return 0, this.session.socketWriteError.Load().(error)
			case <-deadline:
				return n, ErrTimeout
			case <-this.chClose:
				return 0, ErrClosedPipe
			}
		} else {
			writen, _ := this.writeBuffer.Write(sentb[n:])
			n += writen
			this.writeBufferLock.Unlock()
			this.doWrite()
		}
	}
	return
}

func (this *Stream) updateWindows(cmd byte, win uint16) {
	fmt.Println("update", win)
	switch cmd {
	case cmdPSH:
		this.sendLock.Lock()
		this.rWindow = win
		this.isWriting = false
		this.sendLock.Unlock()
	case cmdRSH:
		this.sendLock.Lock()
		this.rWindow = win
		this.isWriting = false
		this.sendLock.Unlock()
	}
	notifyEvent(this.chWriteEvent)
	this.doWrite()
}

func (this *Stream) doWrite() {
	this.sendLock.Lock()
	defer this.sendLock.Unlock()

	this.writeBufferLock.Lock()
	defer this.writeBufferLock.Unlock()

	if this.writeBuffer.Empty() {
		select {
		case <-this.chClose:
			this.session.closeStream(this.streamID)
		default:
			return
		}
	}

	if this.isWriting || this.rWindow == 0 {
		return
	}

	wind := this.writeBuffer.Len()
	if wind > int(this.rWindow) {
		wind = int(this.rWindow)
	}
	buf := make([]byte, headerSize+wind)
	n, _ := this.writeBuffer.Read(buf[headerSize:])
	copy(buf, headerBytes(cmdPSH, this.streamID, uint16(n)))
	this.isWriting = true
	this.session.pushWrite(buf[:headerSize+n])
}

func (s *Stream) Close() error {
	var once bool
	s.closeOnce.Do(func() {
		close(s.chClose)
		once = true
	})

	if once {
		s.doWrite()
		return nil
	} else {
		return ErrClosedPipe
	}
}

func (this *Stream) fin() {
	this.finOnce.Do(func() {
		close(this.chFin)
	})
}

// SetReadDeadline sets the read deadline as defined by
// net.Conn.SetReadDeadline.
// A zero time value disables the deadline.
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.readDeadline.Store(t)
	return nil
}

// SetWriteDeadline sets the write deadline as defined by
// net.Conn.SetWriteDeadline.
// A zero time value disables the deadline.
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline.Store(t)
	return nil
}

// SetDeadline sets both read and write deadlines as defined by
// net.Conn.SetDeadline.
// A zero time value disables the deadlines.
func (s *Stream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	if err := s.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// LocalAddr satisfies net.Conn interface
func (s *Stream) LocalAddr() net.Addr {
	return s.session.conn.LocalAddr()
}

// RemoteAddr satisfies net.Conn interface
func (s *Stream) RemoteAddr() net.Addr {
	return s.session.conn.RemoteAddr()
}
