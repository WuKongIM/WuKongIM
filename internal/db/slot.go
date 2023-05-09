package db

type Slot struct {
	f *FileDB
}

func NewSlot(f *FileDB) *Slot {
	return &Slot{
		f: f,
	}
}
