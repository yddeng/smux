package smux

type AIOStream struct {
	stream *Stream
}

func CreateAIOStream(stream *Stream) *AIOStream {
	return &AIOStream{stream: stream}
}

func (this *AIOStream) StreamID() uint16 {
	return this.stream.StreamID()
}

func (this *AIOStream) Close() error {
	return this.stream.Close()
}

func (this *AIOStream) Writable() (writable bool) {
	_, writable = this.stream.Writable()
	return
}

func (this *AIOStream) Readable(fd uint16) (readable bool) {
	_, readable = this.stream.Writable()
	return
}

func (this *AIOStream) Read(b []byte) (n int, err error) {
	_, readable := this.stream.Readable()
	if !readable {
		return
	}
	n, err = this.stream.Read(b)
	return
}

func (this *AIOStream) Write(b []byte) (n int, err error) {
	length, writable := this.stream.Writable()
	if !writable {
		return
	}

	var data []byte
	if len(b) > int(length) {
		data = b[:length]
	} else {
		data = b
	}

	n, err = this.stream.Write(data)
	return
}
