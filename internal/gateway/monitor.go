package gateway

type Monitor interface {
	UpstreamPackageAdd(n int)
	UpstreamTrafficAdd(n int)
	InMsgsInc()
	InBytesAdd(n int64)
	OutMsgsInc()
	OutBytesAdd(n int64)

	DownstreamPackageAdd(n int)
	DownstreamTrafficAdd(n int)
}

type emptyMonitor struct {
}

func (e *emptyMonitor) UpstreamPackageAdd(n int)   {}
func (e *emptyMonitor) UpstreamTrafficAdd(n int)   {}
func (e *emptyMonitor) InMsgsInc()                 {}
func (e *emptyMonitor) InBytesAdd(n int64)         {}
func (e *emptyMonitor) OutMsgsInc()                {}
func (e *emptyMonitor) OutBytesAdd(n int64)        {}
func (e *emptyMonitor) DownstreamPackageAdd(n int) {}
func (e *emptyMonitor) DownstreamTrafficAdd(n int) {}
