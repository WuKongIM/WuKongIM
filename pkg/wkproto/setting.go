package wkproto

type Setting uint8

const (
	SettingUnknown        Setting = 0
	SettingReceiptEnabled Setting = 1 << 7 // 是否开启回执
	SettingSignal         Setting = 1 << 5 // 是否开启signal加密
	SettingNoEncrypt      Setting = 1 << 4 // 是否不加密
	SettingTopic          Setting = 1 << 3 // 是否有topic
	SettingStream         Setting = 1 << 2 // 是否开启流

)

func (s Setting) IsSet(v Setting) bool {
	return s&v != 0
}

func (s *Setting) Clear(v Setting) {
	*s &= ^v
}

func (s *Setting) Set(v Setting) Setting {
	*s |= v
	return *s
}

func (s Setting) Uint8() uint8 {
	return uint8(s)
}
