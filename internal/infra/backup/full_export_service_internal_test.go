package backup

import "testing"

func TestFullExportServiceSharesOneLimiterPerNodeBackup(t *testing.T) {
	service := &FullExportService{}
	first := service.limiterFor("backup-a", 50<<20)
	second := service.limiterFor("backup-a", 50<<20)
	if first == nil || first != second {
		t.Fatal("same node backup did not share one aggregate limiter")
	}
	if replacement := service.limiterFor("backup-b", 50<<20); replacement == first {
		t.Fatal("new backup reused the prior job limiter")
	}
}
