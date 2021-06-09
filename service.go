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
	session *Session

	fd2Callback map[uint16]func(event Event)
	fdLock      sync.RWMutex

	taskQueue chan task

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
		this.taskQueue <- task{
			fd:    fd,
			event: event,
		}
	}
}

func (this *AIOService) trigger(fd uint16) {
	stream := this.session.GetStream(fd)
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
		this.session.aioServiceLocker.Lock()
		this.session.aioService = nil
		this.session.aioServiceLocker.Unlock()
		this.session = nil
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

func (this *Session) OpenAIOService(worker int) *AIOService {
	this.aioServiceLocker.Lock()
	defer this.aioServiceLocker.Unlock()

	s := &AIOService{
		session:     this,
		fd2Callback: map[uint16]func(event Event){},
		fdLock:      sync.RWMutex{},
		taskQueue:   make(chan task, 1024),
	}
	this.aioService = s

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

func (this *Session) preparseCmd(fd uint16, cmd byte) {
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
	default:
		return
	}

	this.aioService.appendEvent(fd, event)
}
