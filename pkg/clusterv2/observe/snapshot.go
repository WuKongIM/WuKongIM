package observe

// RuntimeSnapshot summarizes clusterv2 readiness gates.
type RuntimeSnapshot struct {
	// ControlReady reports whether a valid control snapshot exists.
	ControlReady bool
	// RoutesReady reports whether routing is initialized.
	RoutesReady bool
	// SlotsReady reports whether local Slot reconciliation completed.
	SlotsReady bool
	// ChannelsReady reports whether ChannelV2 is initialized.
	ChannelsReady bool
}

// Ready reports whether all foreground runtime gates are ready.
func (s RuntimeSnapshot) Ready() bool {
	return s.ControlReady && s.RoutesReady && s.SlotsReady && s.ChannelsReady
}
