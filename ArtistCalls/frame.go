package artistcalls

type Frame struct {
	Ssrc      uint32
	Data      []byte
	FrameData FrameData
}
