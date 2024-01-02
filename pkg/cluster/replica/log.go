package replica

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

type Log struct {
	Index uint64 // 日志下标
	Data  []byte // 日志数据
}

func (l *Log) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(l.Index)
	enc.WriteBinary(l.Data)
	return enc.Bytes(), nil
}

func (l *Log) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if l.Index, err = dec.Uint64(); err != nil {
		return err
	}
	if l.Data, err = dec.Binary(); err != nil {
		return err
	}
	return nil
}
