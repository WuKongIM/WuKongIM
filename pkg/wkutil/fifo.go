package wkutil

type FIFO struct {
	data []int
	size int
}

func NewFIFO(size int) *FIFO {
	return &FIFO{
		data: make([]int, 0, size),
		size: size,
	}
}

func (f *FIFO) Push(val int) {
	if len(f.data) == f.size {
		f.data = f.data[1:]
	}
	f.data = append(f.data, val)
}

func (f *FIFO) Pop() int {
	if len(f.data) == 0 {
		return 0
	}
	val := f.data[0]
	f.data = f.data[1:]
	return val
}

func (f *FIFO) Len() int {
	return len(f.data)
}

func (f *FIFO) Data() []int {
	return f.data
}
