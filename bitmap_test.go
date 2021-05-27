package smux

import (
	"testing"
)

func TestNewBitmap(t *testing.T) {
	bm := NewBitmap(8)

	t.Log(bm.Max(), bm.Len())

	n1, _ := bm.Get()
	t.Log(bm.Max(), bm.Len(), n1)

	for {
		n, err := bm.Get()
		if err != nil {
			t.Log(bm.Max(), bm.Len(), n, err)
			break
		}
		t.Log(bm.Max(), bm.Len(), n)
	}

	has := bm.Has(n1)
	t.Log(bm.Max(), bm.Len(), n1, has)

	bm.Put(n1)
	t.Log(bm.Max(), bm.Len(), n1)

	has = bm.Has(n1)
	t.Log(bm.Max(), bm.Len(), n1, has)

	for {
		n, err := bm.Get()
		if err != nil {
			t.Log(bm.Max(), bm.Len(), n, err)
			break
		}
		t.Log(bm.Max(), bm.Len(), n)
	}
}
