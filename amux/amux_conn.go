package amux

import (
	"github.com/yddeng/smux"
	"sync"
)

var (
	defaultService *smux.AIOService
	createOnce     sync.Once
)

type AmuxConn struct {
	conn *smux.MuxConn
	serv *smux.AIOService

	closeOnce sync.Once
}

func (this *AmuxConn) Send(o interface{}) {

}

func (this *AmuxConn) Receive(onData func(conn *AmuxConn, data []byte)) {

}

func (this *AmuxConn) onEventCallback(e smux.Event) {

}

func CreateAIOConn(conn *smux.MuxConn) (*AmuxConn, error) {
	aioConn := &AmuxConn{
		conn: conn,
		serv: defaultService,
	}

	conn.SetNonblock(true)
	return aioConn, defaultService.Watch(conn.ID(), aioConn.onEventCallback)
}

func OpenAIOService(sess *smux.MuxSession, worker int) {
	createOnce.Do(func() {
		defaultService = smux.OpenAIOService(sess, worker)
	})
}
