package message

import "time"

// PermissionObserver measures a sealed admission plan; labels must remain fixed
// and observations must not expose sender/channel identity or block admission.
type PermissionObserver interface {
	ObserveSendPermissionCount(kind string, n int)
	ObserveSendPermissionStage(stage, result string, duration time.Duration)
	ObserveSendBanRejection(scope string, n int)
}

func (a *App) permissionStart() time.Time {
	if a != nil && a.permissionObserver != nil {
		return time.Now()
	}
	return time.Time{}
}
func (a *App) permissionStage(stage, result string, start time.Time) {
	if a != nil && a.permissionObserver != nil {
		a.permissionObserver.ObserveSendPermissionStage(stage, result, time.Since(start))
	}
}
func (a *App) permissionCount(kind string, n int) {
	if a != nil && a.permissionObserver != nil {
		a.permissionObserver.ObserveSendPermissionCount(kind, n)
	}
}
func (a *App) observeSendBan(scope string, n int) {
	if a != nil && a.permissionObserver != nil {
		a.permissionObserver.ObserveSendBanRejection(scope, n)
	}
}
