package smux

import (
	"errors"
	"math"
	"sync"
)

var ErrIDBitmapMax = errors.New("id bitmap max")

type idBitmap struct {
	bits   []byte
	next   uint16
	length int
	lock   sync.Mutex
}

func NewIDBitmap() *idBitmap {
	return &idBitmap{bits: make([]byte, 65536/8)}
}

// NewBitmap return [0, 65535]
func (this *idBitmap) Get() (uint16, error) {
	this.lock.Lock()
	defer this.lock.Unlock()
	if this.length == 65536 {
		return 0, ErrIDBitmapMax
	}

	for {
		idx, bit := this.next/8, this.next%8
		if (this.bits[idx] & (1 << bit)) == 0 {
			this.bits[idx] |= 1 << bit
			this.length++
			return this.next, nil
		}

		if this.next == math.MaxUint16 {
			this.next = 0
		} else {
			this.next++
		}
	}
}
func (this *idBitmap) Set(num uint16) bool {
	this.lock.Lock()
	defer this.lock.Unlock()

	idx, bit := num/8, num%8
	if (this.bits[idx] & (1 << bit)) == 0 {
		this.bits[idx] |= 1 << bit
		this.length++
		return true
	}
	return false
}

func (this *idBitmap) Put(num uint16) bool {
	this.lock.Lock()
	defer this.lock.Unlock()

	idx, bit := num/8, num%8
	if (this.bits[idx] & (1 << bit)) != 0 {
		this.bits[idx] &^= 1 << bit
		this.length--
		return true
	}
	return false
}

func (this *idBitmap) Has(num uint16) bool {
	this.lock.Lock()
	defer this.lock.Unlock()
	idx, bit := num/8, num%8
	return (this.bits[idx] & (1 << bit)) != 0
}

func (this *idBitmap) Len() int {
	this.lock.Lock()
	defer this.lock.Unlock()
	return this.length
}
