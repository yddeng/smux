package smux

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

/*
 只有在非堵塞模式下才有的返回错误
 读情况下，没有数据可读；写情况下，不能写且已发送为0
*/
var ErrNonblock = errors.New("nonblock. ")

type MuxConn struct {
	mux    *MuxSession
	connID uint16

	bufferRead uint32
	buffer     *Buffer
	bufferLock sync.Mutex
	readLock   sync.Mutex

	// notify a read/write event
	chReadEvent  chan struct{}
	chWriteEvent chan struct{}

	// 已发送待确认的字节数
	waitConfirm uint32
	writeLock   sync.Mutex

	// deadlines
	readDeadline  atomic.Value
	writeDeadline atomic.Value

	// 主动关闭调用 close
	chClose   chan struct{}
	closeOnce sync.Once

	// 被动关闭调用 fin
	chFin   chan struct{}
	finOnce sync.Once

	nonblock        uint32 // 堵塞，默认堵塞
	chNonblockEvent chan struct{}
}

func newMuxConn(cid uint16, mux *Mux) *MuxConn {
	return &MuxConn{
		mux:             mux,
		connID:          cid,
		buffer:          NewBuffer(muxConnWindowSize),
		bufferLock:      sync.Mutex{},
		chReadEvent:     make(chan struct{}, 1),
		chWriteEvent:    make(chan struct{}, 1),
		readDeadline:    atomic.Value{},
		writeDeadline:   atomic.Value{},
		chClose:         make(chan struct{}),
		closeOnce:       sync.Once{},
		chFin:           make(chan struct{}),
		finOnce:         sync.Once{},
		chNonblockEvent: make(chan struct{}, 1),
	}
}

func (this *MuxConn) ID() uint16 {
	return this.connID
}

func (this *MuxConn) SetNonblock(nonblocking bool) {
	if nonblocking {
		if atomic.CompareAndSwapUint32(&this.nonblock, 0, 1) {
			notifyEvent(this.chNonblockEvent)
		}
	} else {
		atomic.CompareAndSwapUint32(&this.nonblock, 1, 0)
	}
}

// Readable returns length of read buffer
func (this *MuxConn) Readable() (int, bool) {
	this.bufferLock.Lock()
	defer this.bufferLock.Unlock()
	return this.buffer.Len(), !this.buffer.Empty()
}

func (this *MuxConn) Read(b []byte) (n int, err error) {
	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	default:
	}

	this.readLock.Lock()
	defer this.readLock.Unlock()

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

func (this *MuxConn) tryRead(b []byte) (n int, err error) {
	if len(b) == 0 {
		return
	}

	this.bufferLock.Lock()
	defer this.bufferLock.Unlock()

	if this.buffer.Empty() {
		return -1, nil
	} else {
		n, err = this.buffer.Read(b)
		this.bufferRead += uint32(n)
		if this.bufferRead >= muxConnWindowSize/2 {
			this.mux.writeHeader(cmdCFM, this.connID, this.bufferRead)
			this.bufferRead = 0
		}
		return
	}
}

func (this *MuxConn) waitRead() error {
	var timer *time.Timer
	var deadline <-chan time.Time
	if d, ok := this.readDeadline.Load().(time.Time); ok && !d.IsZero() {
		timer = time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}

	var nonblockC chan struct{}
	if atomic.LoadUint32(&this.nonblock) == 1 {
		nonblockC = make(chan struct{}, 1)
		nonblockC <- struct{}{}
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
	case <-deadline:
		return ErrTimeout
	case <-this.chClose:
		return this.mux.closeReason.Load().(error)
	case <-this.chNonblockEvent:
		return nil
	case <-nonblockC:
		// 怎样降低优先级
		return ErrNonblock
	}
}

func (this *MuxConn) pushBytes(b []byte) {
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
	notifyEvent(this.chReadEvent)
	this.bufferLock.Unlock()
}

// Writable returns write windows
func (this *MuxConn) Writable() (int, bool) {
	writeWindows := muxConnWindowSize - atomic.LoadUint32(&this.waitConfirm)
	return int(writeWindows), writeWindows > 0
}

func (this *MuxConn) Write(b []byte) (n int, err error) {
	select {
	case <-this.chClose:
		return 0, ErrClosedPipe
	case <-this.chFin:
		return 0, ErrBrokenPipe
	default:
	}

	this.writeLock.Lock()
	defer this.writeLock.Unlock()

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
		wSize := muxConnWindowSize - atomic.LoadUint32(&this.waitConfirm)
		if wSize <= 0 {
			var nonblockC chan struct{}
			if atomic.LoadUint32(&this.nonblock) == 1 {
				nonblockC = make(chan struct{}, 1)
				nonblockC <- struct{}{}
			}

			select {
			case <-this.chWriteEvent:
			case <-this.chFin:
				return 0, ErrBrokenPipe
			case <-deadline:
				return n, ErrTimeout
			case <-this.chClose:
				return 0, ErrClosedPipe
			case <-this.chNonblockEvent:
			case <-nonblockC:
				if n == 0 {
					err = ErrNonblock
				}
				return
			}
		} else {
			sz := blen - n
			if sz > int(wSize) {
				sz = int(wSize)
			}
			if sz > frameSize {
				sz = frameSize
			}

			sendn, err := this.mux.writeData(this.connID, sentb[n:n+sz], deadline)
			if sendn > 0 {
				atomic.AddUint32(&this.waitConfirm, uint32(sendn))
				n += sendn
			}
			if err != nil {
				return n, err
			}

		}
	}
	return
}

func (this *MuxConn) bytesConfirm(count uint32) uint32 {
	windows := muxConnWindowSize - atomic.AddUint32(&this.waitConfirm, -count)
	notifyEvent(this.chWriteEvent)
	return windows
}

func (this *MuxConn) Close() error {
	var once bool
	this.closeOnce.Do(func() {
		close(this.chClose)
		once = true
	})

	if once {
		this.mux.closedMuxConn(this.connID)
		return nil
	} else {
		return ErrClosedPipe
	}
}

func (this *MuxConn) fin() {
	this.finOnce.Do(func() {
		close(this.chFin)
	})
}

// SetReadDeadline sets the read deadline as defined by
// net.Conn.SetReadDeadline.
// A zero time value disables the deadline.
func (this *MuxConn) SetReadDeadline(t time.Time) error {
	this.readDeadline.Store(t)
	return nil
}

// SetWriteDeadline sets the write deadline as defined by
// net.Conn.SetWriteDeadline.
// A zero time value disables the deadline.
func (this *MuxConn) SetWriteDeadline(t time.Time) error {
	this.writeDeadline.Store(t)
	return nil
}

// SetDeadline sets both read and write deadlines as defined by
// net.Conn.SetDeadline.
// A zero time value disables the deadlines.
func (this *MuxConn) SetDeadline(t time.Time) error {
	if err := this.SetReadDeadline(t); err != nil {
		return err
	}
	if err := this.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// LocalAddr satisfies net.Conn interface
func (this *MuxConn) LocalAddr() net.Addr {
	return this.mux.conn.LocalAddr()
}

// RemoteAddr satisfies net.Conn interface
func (this *MuxConn) RemoteAddr() net.Addr {
	return this.mux.conn.RemoteAddr()
}
