package store

type CmdVersion uint16

const (
	// CmdVersionChannelInfo is the version of the command that contains channel info
	CmdVersionChannelInfo CmdVersion = 3
	// CmdVersionMessageEvent is the version of the command that contains message event info
	CmdVersionMessageEvent CmdVersion = 1
)

func (c CmdVersion) Uint16() uint16 {
	return uint16(c)
}
