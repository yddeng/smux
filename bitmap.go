package smux

import (
	"errors"
)

/*
	位图
*/

var ErrBitmapMax = errors.New("bitmap max")

type Bitmap struct {
	max    uint16
	words  []byte
	next   uint16
	length uint16
}

// NewBitmap return [0, max-1]
func NewBitmap(max uint16) *Bitmap {
	n := max / 8
	if max%8 != 0 {
		n++
	}
	return &Bitmap{
		max:   max,
		words: make([]byte, n),
	}
}

func (this *Bitmap) Get() (uint16, error) {
	if this.length == this.max {
		return 0, ErrBitmapMax
	}

	for {
		idx, bit := this.next/8, this.next%8
		if (this.words[idx] & (1 << bit)) == 0 {
			this.words[idx] |= 1 << bit
			this.length++
			return this.next, nil
		}
		this.next = (this.next + 1) % this.max
	}
}

func (this *Bitmap) Put(num uint16) {
	if num >= this.max {
		return
	}

	idx, bit := num/8, num%8
	if (this.words[idx] & (1 << bit)) != 0 {
		this.words[idx] &^= 1 << bit
		this.length--
	}
}

func (this *Bitmap) Has(num uint16) bool {
	if num >= this.max {
		return false
	}
	idx, bit := num/8, num%8
	return (this.words[idx] & (1 << bit)) != 0
}

func (this *Bitmap) Len() uint16 {
	return this.length
}

func (this *Bitmap) Max() uint16 {
	return this.max
}
