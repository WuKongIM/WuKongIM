package clusternet

import "testing"

func TestWillReceiptRPCServiceIdentityAndMaintenance(t *testing.T) {
	if RPCChannelWillReceipt != 103 || transportServiceAlias(RPCChannelWillReceipt) != "channel will receipt" {
		t.Fatal("Will receipt service identity changed")
	}
	if !isForegroundChannelMutationService(RPCChannelWillReceipt) {
		t.Fatal("receipt reads must remain behind maintenance admission")
	}
	if !(&TransportServer{}).serviceOptions(RPCChannelWillReceipt).CancelRunning {
		t.Fatal("receipt reads must honor caller cancellation")
	}
}
