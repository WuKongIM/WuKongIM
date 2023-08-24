package wkserver

type Options struct {
	Addr            string
	RequestPoolSize int
}

func NewOptions() *Options {

	return &Options{
		Addr:            "tcp://0.0.0.0:12000",
		RequestPoolSize: 1000,
	}
}
