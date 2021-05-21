package smux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	initPeerWindowSize = frameSize
	streamWindowSize   = 512 * 1024
)

type Stream struct {
	sess     *Session
	streamID uint32

	buffer     *Buffer
	bufferLock sync.Mutex

	// notify a read/write event
	chReadEvent  chan struct{}
	chWriteEvent chan struct{}

	// do write
	isWriting  bool
	peerWindow uint16
	writeLock  sync.Mutex

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

func newStream(sid uint32, sess *Session) *Stream {
	return &Stream{
		sess:          sess,
		streamID:      sid,
		buffer:        New(streamWindowSize),
		bufferLock:    sync.Mutex{},
		chReadEvent:   make(chan struct{}, 1),
		chWriteEvent:  make(chan struct{}, 1),
		isWriting:     false,
		peerWindow:    initPeerWindowSize,
		writeLock:     sync.Mutex{},
		readDeadline:  atomic.Value{},
		writeDeadline: atomic.Value{},
		chClose:       make(chan struct{}),
		closeOnce:     sync.Once{},
		chFin:         make(chan struct{}),
		finOnce:       sync.Once{},
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

	this.bufferLock.Lock()
	defer this.bufferLock.Unlock()

	if this.buffer.Empty() {
		return -1, nil
	} else {
		needUPW := streamWindowSize-this.buffer.Len() <= 0
		n, err = this.buffer.Read(b)
		wind := streamWindowSize - this.buffer.Len()
		if err == nil && wind > 0 && needUPW {
			if wind > frameSize {
				wind = frameSize
			}
			this.sess.write(headerBytes(cmdUPW, this.streamID, uint16(wind)))
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
		this.bufferLock.Lock()
		defer this.bufferLock.Unlock()
		if !this.buffer.Empty() {
			return nil
		}
		return io.EOF
	case <-this.sess.chSocketReadError:
		return this.sess.socketReadError.Load().(error)
	case <-deadline:
		return ErrTimeout
	case <-this.chClose:
		return ErrClosedPipe
	}
}

func (this *Stream) pushBytes(b []byte) {
	select {
	case <-this.chClose:
		return
	default:
	}

	this.bufferLock.Lock()
	if this.buffer.Len()+len(b) > this.buffer.Cap() {
		this.buffer.Grow(len(b))
	}
	_, _ = this.buffer.Write(b)
	wind := streamWindowSize - this.buffer.Len()
	this.bufferLock.Unlock()

	notifyEvent(this.chReadEvent)

	if wind <= 0 {
		wind = 0
	}
	if wind > frameSize {
		wind = frameSize
	}
	_, _ = this.sess.write(headerBytes(cmdUPW, this.streamID, uint16(wind)))
}

func (this *Stream) Write(b []byte) (n int, err error) {
	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	case <-this.chFin:
		return 0, ErrBrokenPipe
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
		this.writeLock.Lock()
		if this.isWriting || this.peerWindow == 0 {
			this.writeLock.Unlock()
			select {
			case <-this.chWriteEvent:
			case <-this.chFin:
				return 0, ErrBrokenPipe
			case <-this.sess.chSocketWriteError:
				return 0, this.sess.socketWriteError.Load().(error)
			case <-deadline:
				return n, ErrTimeout
			case <-this.chClose:
				return 0, ErrClosedPipe
			}
		} else {
			sz := blen - n
			if sz > int(this.peerWindow) {
				sz = int(this.peerWindow)
			}
			buf := make([]byte, headerSize+sz)
			headerWrite(cmdPSH, this.streamID, uint16(sz), buf)
			copy(buf[headerSize:], sentb[n:n+sz])
			sendn, err := this.sess.write(buf)
			if err != nil {
				this.writeLock.Unlock()
				return n, err
			}
			this.isWriting = true
			n += sendn - headerSize
			fmt.Println("write", sendn, n)
			this.writeLock.Unlock()
		}
	}
	return
}

func (this *Stream) windowUpdate(win uint16) {
	this.writeLock.Lock()
	this.peerWindow = win
	this.isWriting = false
	this.writeLock.Unlock()

	notifyEvent(this.chWriteEvent)
}

func (this *Stream) Close() error {
	var once bool
	this.closeOnce.Do(func() {
		close(this.chClose)
		once = true
	})

	if once {
		this.sess.closedStream(this.streamID)
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
func (this *Stream) SetReadDeadline(t time.Time) error {
	this.readDeadline.Store(t)
	return nil
}

// SetWriteDeadline sets the write deadline as defined by
// net.Conn.SetWriteDeadline.
// A zero time value disables the deadline.
func (this *Stream) SetWriteDeadline(t time.Time) error {
	this.writeDeadline.Store(t)
	return nil
}

// SetDeadline sets both read and write deadlines as defined by
// net.Conn.SetDeadline.
// A zero time value disables the deadlines.
func (this *Stream) SetDeadline(t time.Time) error {
	if err := this.SetReadDeadline(t); err != nil {
		return err
	}
	if err := this.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// LocalAddr satisfies net.Conn interface
func (this *Stream) LocalAddr() net.Addr {
	return this.sess.conn.LocalAddr()
}

// RemoteAddr satisfies net.Conn interface
func (this *Stream) RemoteAddr() net.Addr {
	return this.sess.conn.RemoteAddr()
}
