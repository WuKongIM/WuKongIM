package store

type CmdVersion uint16

const (
	// CmdVersionChannelInfo is the version of the command that contains channel info
	CmdVersionChannelInfo CmdVersion = 3
	// CmdVersionStreamV2 is the version of the command that contains stream v2 info
	CmdVersionStreamV2 CmdVersion = 1
)

func (c CmdVersion) Uint16() uint16 {
	return uint16(c)
}
