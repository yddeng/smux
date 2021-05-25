package smux

import (
	"testing"
)

func TestNew(t *testing.T) {
	b := New(16)
	t.Log(b.Empty(), b.Full(), b.Len(), b.Cap())

	d1 := make([]byte, 15)
	n, _ := b.Write(d1)
	t.Log(n, b.Empty(), b.Full(), b.Len())

	n, _ = b.Read(d1)
	t.Log(n, b.Empty(), b.Full(), b.Len())

	d2 := make([]byte, 20)
	n, _ = b.Write(d2)
	t.Log(n, b.Empty(), b.Full(), b.Len())

	n, _ = b.Read(d1)
	t.Log(n, b.Empty(), b.Full(), b.Len())

	//b.Grow(4)
	n, _ = b.Write(d2)
	t.Log(n, b.Empty(), b.Full(), b.Len())

}
