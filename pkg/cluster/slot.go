package cluster

type Slot struct {
	slotLog *slotLog
	slotID  uint32
	dataDir string
}

func NewSlot(slotID uint32, dataDir string) *Slot {
	return &Slot{
		slotID:  slotID,
		dataDir: dataDir,
		slotLog: newSlotLog(slotID, dataDir),
	}
}

func (s *Slot) Open() error {
	return s.slotLog.Open()
}

func (s *Slot) Close() error {
	return s.slotLog.Close()
}

func (s *Slot) Append(logIndex uint64, data []byte) error {
	return s.slotLog.Append(logIndex, data)
}

func (s *Slot) LastIndex() (uint64, error) {
	return s.slotLog.LastIndex()
}
