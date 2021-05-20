package smux

import (
	"fmt"
	"testing"
)

func TestNew(t *testing.T) {
	b := New(16)
	fmt.Println(b.Empty(), b.Full(), b.Len(), b.Cap())

	d1 := make([]byte, 15)
	n, _ := b.Write(d1)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())

	n, _ = b.Read(d1)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())

	d2 := make([]byte, 20)
	n, _ = b.Write(d2)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())

	n, _ = b.Read(d1)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())

	//b.Grow(4)
	n, _ = b.Write(d2)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())

	b.Reset(4)
	n, _ = b.Write(d2)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())

	b.Reset(4)
	n, _ = b.Write(d2)
	fmt.Println(n, b.Empty(), b.Full(), b.Len())
}
