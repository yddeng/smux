package amux

import "errors"

var (
	ErrServiceClosed = errors.New("aioService is closed. ")
)
