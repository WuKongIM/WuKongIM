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

	ConnInc() // 连接数量递增
	ConnDec() // 连接数递减
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

func (e *emptyMonitor) ConnInc() {} // 连接数量递增
func (e *emptyMonitor) ConnDec() {} // 连接数递减
