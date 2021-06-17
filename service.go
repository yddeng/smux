package smux

import (
	"sync"
)

type Event byte

// Event poller 返回事件值
const (
	EV_READ  Event = 1 << 1
	EV_WRITE Event = 1 << 2
	EV_ERROR Event = 1 << 3
)

func (e Event) Readable() bool {
	return e&EV_READ != 0 || e&EV_ERROR != 0
}

func (e Event) Writable() bool {
	return e&EV_WRITE != 0 || e&EV_ERROR != 0
}

type AIOService struct {
	mux *MuxSession

	fd2Callback map[uint16]func(event Event)
	fdLock      sync.RWMutex

	taskQueue chan *task

	die     chan struct{}
	dieOnce sync.Once
}

type task struct {
	fd    uint16
	event Event
}

func (this *AIOService) appendEvent(fd uint16, event Event) {
	this.fdLock.RLock()
	defer this.fdLock.RUnlock()
	if _, ok := this.fd2Callback[fd]; ok {
		t := &task{fd: fd, event: event}
		this.taskQueue <- t
	}
}

func (this *AIOService) trigger(fd uint16) {
	stream := this.mux.GetMuxConn(fd)
	if stream != nil {
		var event Event
		if _, ok := stream.Readable(); ok {
			event |= EV_READ
		}

		if _, ok := stream.Writable(); ok {
			event |= EV_WRITE
		}

		if event != 0 {
			this.appendEvent(fd, event)
		}
	}
}

func (this *AIOService) Watch(fd uint16, callback func(event Event)) {
	if this.isClosed() {
		return
	}

	this.fdLock.Lock()
	if _, ok := this.fd2Callback[fd]; !ok {
		this.fd2Callback[fd] = callback
		this.fdLock.Unlock()
		this.trigger(fd)
	} else {
		this.fdLock.Unlock()
	}
}

func (this *AIOService) Unwatch(fd uint16) {
	this.fdLock.Lock()
	defer this.fdLock.Unlock()
	delete(this.fd2Callback, fd)
}

func (this *AIOService) Close() {
	this.dieOnce.Do(func() {
		close(this.die)
		this.mux.aioServiceLocker.Lock()
		this.mux.aioService = nil
		this.mux.aioServiceLocker.Unlock()
		this.mux = nil
	})
}

func (this *AIOService) isClosed() bool {
	select {
	case <-this.die:
		return true
	default:
		return false
	}
}

func OpenAIOService(mux *MuxSession, worker int) *AIOService {
	mux.aioServiceLocker.Lock()
	defer mux.aioServiceLocker.Unlock()

	s := &AIOService{
		mux:         mux,
		fd2Callback: map[uint16]func(event Event){},
		fdLock:      sync.RWMutex{},
		taskQueue:   make(chan *task, 1024),
	}
	mux.aioService = s

	if worker <= 0 {
		worker = 1
	}
	for i := 0; i < worker; i++ {
		go func() {
			for {
				select {
				case <-s.die:
					return
				case t := <-s.taskQueue:
					s.fdLock.Lock()
					callback, ok := s.fd2Callback[t.fd]
					if ok {
						s.fdLock.Unlock()
						callback(t.event)
					} else {
						s.fdLock.Unlock()
					}
				}
			}
		}()
	}
	return s
}

func (this *MuxSession) preparseCmd(fd uint16, cmd byte) {
	this.aioServiceLocker.Lock()
	defer this.aioServiceLocker.Unlock()
	if this.aioService == nil || this.aioService.isClosed() {
		return
	}

	var event Event
	switch cmd {
	case cmdPSH:
		event = EV_READ
	case cmdCFM:
		event = EV_WRITE
	case cmdFIN:
		event = EV_ERROR
	default:
		return
	}

	this.aioService.appendEvent(fd, event)
}
