package presence

import "context"

func (a *App) RegisterAuthoritative(ctx context.Context, cmd RegisterAuthoritativeCommand) (RegisterAuthoritativeResult, error) {
	_ = ctx
	return RegisterAuthoritativeResult{
		Actions: a.dir.register(cmd.SlotID, cmd.Route, a.now().Unix()),
	}, nil
}

func (a *App) UnregisterAuthoritative(ctx context.Context, cmd UnregisterAuthoritativeCommand) error {
	_ = ctx
	a.dir.unregister(cmd.SlotID, cmd.Route, a.now().Unix())
	return nil
}

func (a *App) HeartbeatAuthoritative(ctx context.Context, cmd HeartbeatAuthoritativeCommand) (HeartbeatAuthoritativeResult, error) {
	_ = ctx
	return a.dir.heartbeat(cmd.Lease, a.now().Unix()), nil
}

func (a *App) ReplayAuthoritative(ctx context.Context, cmd ReplayAuthoritativeCommand) error {
	_ = ctx
	a.dir.replay(cmd.Lease, cmd.Routes, a.now().Unix())
	return nil
}

func (a *App) EndpointsByUID(ctx context.Context, uid string) ([]Route, error) {
	_ = ctx
	return a.dir.endpointsByUID(uid, a.now().Unix()), nil
}

func (a *App) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]Route, error) {
	_ = ctx
	return a.dir.endpointsByUIDs(uids, a.now().Unix()), nil
}
