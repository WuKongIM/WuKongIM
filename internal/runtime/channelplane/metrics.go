package channelplane

func observeAppendQueued(observer Observer, req AppendBatchRequest) {
	if observer != nil {
		observer.OnAppendQueued(AppendEvent{ChannelID: req.ChannelID})
	}
}

func observeAppendCompleted(observer Observer, req AppendBatchRequest, route ChannelRoute, err error) {
	if observer != nil {
		observer.OnAppendCompleted(AppendEvent{ChannelID: req.ChannelID, RouteGeneration: route.RouteGeneration, Err: err})
	}
}
