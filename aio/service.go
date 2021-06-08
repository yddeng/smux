package aio

import "github.com/yddeng/smux"

type SmuxAIOService struct {
	fd2Stream map[uint16]*smux.AIOStream
}

func (this *SmuxAIOService)()  {
	
}