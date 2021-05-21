package smux

import "sync"

type Buffer struct {
	r, w  int
	buf   []byte
	cap   int
	empty bool
}

func New(size int) *Buffer {
	return &Buffer{
		buf:   make([]byte, size),
		cap:   size,
		empty: true,
	}
}

func (b *Buffer) Empty() bool {
	return b.empty
}

func (b *Buffer) Full() bool {
	if b.r == b.w && !b.empty {
		return true
	}
	return false
}

func (b *Buffer) Clear() {
	b.r = 0
	b.w = 0
	b.empty = true
}

func (b *Buffer) Len() int {
	if b.w > b.r {
		return b.w - b.r
	} else if b.w < b.r {
		return b.cap - b.r + b.w
	} else {
		if b.empty {
			return 0
		} else {
			return b.cap
		}
	}
}

func (b *Buffer) Cap() int {
	return b.cap
}

func (b *Buffer) Grow(n int) {
	buf := make([]byte, b.cap+n)
	var num int
	if !b.empty {
		if b.w > b.r {
			num = copy(buf, b.buf[b.r:b.w])
		} else {
			num = copy(buf, b.buf[b.r:])
			if b.w > 0 {
				num += copy(buf[num:], b.buf[:b.w])
			}
		}
	}
	b.cap = b.cap + n
	b.buf = buf
	b.r = 0
	b.w = num % b.cap
}

func (b *Buffer) Read(buf []byte) (n int, err error) {
	plen := len(buf)
	if plen == 0 || b.empty {
		return
	}

	if b.w > b.r {
		n = copy(buf, b.buf[b.r:b.w])
		b.r = (b.r + n) % b.cap
	} else {
		n = copy(buf, b.buf[b.r:])
		b.r = (b.r + n) % b.cap
		if plen > n && b.w > 0 {
			b.r = copy(buf[n:], b.buf[:b.w])
			n += b.r
		}
	}

	if b.r == b.w {
		b.empty = true
	}
	return
}

func (b *Buffer) Write(buf []byte) (n int, err error) {
	plen := len(buf)
	if plen == 0 || (b.r == b.w && !b.empty) {
		return
	}

	if b.w < b.r {
		n = copy(b.buf[b.w:b.r], buf)
		b.w = (b.w + n) % b.cap
	} else {
		n = copy(b.buf[b.w:], buf)
		b.w = (b.w + n) % b.cap
		if plen > n && b.r > 0 {
			b.w = copy(b.buf[:b.r], buf[n:])
			n += b.w
		}
	}

	if n > 0 {
		b.empty = false
	}
	return
}

func (b *Buffer) Bytes(buf []byte) int {
	if !b.empty {
		var num int
		if b.w > b.r {
			num = copy(buf, b.buf[b.r:b.w])
		} else {
			num = copy(buf, b.buf[b.r:])
			if b.w > 0 {
				num += copy(buf[num:], b.buf[:b.w])
			}
		}
		return num
	}
	return 0
}

func (b *Buffer) Reset(n int) {
	if n > b.Len() {
		n = b.Len()
	}

	if n > 0 {
		b.r = (b.r + n) % b.cap
		if b.r == b.w {
			b.empty = true
		}
	}

}

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 1<<16)
	},
}
