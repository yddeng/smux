package smux

import (
	"testing"
)

func TestNewIDBitmap(t *testing.T) {
	bm := NewIDBitmap()

	t.Log(bm.Len())

	n1, _ := bm.Get()
	t.Log(bm.Len(), n1)

	for {
		n, err := bm.Get()
		if err != nil {
			t.Log(bm.Len(), n, err)
			break
		}
		t.Log(bm.Len(), n)
	}

	n1 = 4566
	has := bm.Has(n1)
	t.Log(bm.Len(), n1, has)

	bm.Put(n1)
	t.Log(bm.Len(), n1)

	has = bm.Has(n1)
	t.Log(bm.Len(), n1, has)

	for {
		n, err := bm.Get()
		if err != nil {
			t.Log(bm.Len(), n, err)
			break
		}
		t.Log(bm.Len(), n)
	}
}
