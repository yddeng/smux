package smux

import "sync"

type Event byte

// Event poller 返回事件值
const (
	EV_READ  Event = 1 << 1
	EV_WRITE Event = 1 << 2
	EV_ERROR Event = 1 << 3
)

func (e Event) ReadEvent() bool {
	return e&EV_READ != 0 || e&EV_ERROR != 0
}

func (e Event) WriteEvent() bool {
	return e&EV_WRITE != 0 || e&EV_ERROR != 0
}

type Poller struct {
	session *Session

	filterLock sync.RWMutex
	filter     map[uint16]Event

	events      map[uint16]Event
	eventNotify chan struct{}
	eventLock   sync.Mutex

	die     chan struct{}
	dieOnce sync.Once
}

func (this *Poller) writable(fd uint16) (writable bool) {
	this.session.streamLock.Lock()
	defer this.session.streamLock.Unlock()

	if stream, ok := this.session.streams[fd]; ok {
		_, writable = stream.Writable()
		return
	}
	return
}

func (this *Poller) readable(fd uint16) (readable bool) {
	this.session.streamLock.Lock()
	defer this.session.streamLock.Unlock()

	if stream, ok := this.session.streams[fd]; ok {
		_, readable = stream.Writable()
		return
	}
	return
}

func cmd2Event(cmd byte) Event {
	switch cmd {
	case cmdPSH:
		return EV_READ
	case cmdCFM:
		return EV_WRITE
	default:
		return 0
	}
}

func (this *Poller) appendEvent(fd uint16, event Event) {
	this.eventLock.Lock()
	e, ok := this.events[fd]
	if ok {
		e |= event
	}

	this.events[fd] = e
	this.eventLock.Unlock()

	select {
	case this.eventNotify <- struct{}{}:
	default:
	}
}

func (this *Poller) modFilter(fd uint16, filter Event) {
	this.filterLock.Lock()
	this.filter[fd] = filter
	this.filterLock.Unlock()
}

func (this *Poller) AddRead(fd uint16) {
	this.modFilter(fd, EV_READ)
}

func (this *Poller) Delete(fd uint16) {
	this.filterLock.Lock()
	defer this.filterLock.Unlock()
	if _, ok := this.filter[fd]; ok {
		delete(this.filter, fd)
	}
}

func (this *Poller) Close() {
	this.dieOnce.Do(func() {
		close(this.die)
		this.session.poller = nil
	})
}

func OpenPoller(session *Session) *Poller {
	p := &Poller{
		session:     session,
		filterLock:  sync.RWMutex{},
		filter:      map[uint16]Event{},
		events:      map[uint16]Event{},
		eventNotify: make(chan struct{}, 1),
		eventLock:   sync.Mutex{},
		die:         make(chan struct{}),
		dieOnce:     sync.Once{},
	}

	session.poller = p
	return p
}

func (this *Poller) Polling(callback func(uint16, Event)) {
	for {
		select {
		case <-this.eventNotify:
			this.eventLock.Lock()
			events := this.events
			this.events = map[uint16]Event{}
			this.eventLock.Unlock()

			for id, ev := range events {
				callback(id, ev)
			}
		case <-this.die:
			return
		}

	}
}
