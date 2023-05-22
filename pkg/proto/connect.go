package proto

type ConnectSetting uint8

type Connect struct {
	Framer
	ConnectSetting ConnectSetting
}
