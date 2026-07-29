package channels

import (
	"context"
	"testing"

	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
)

func TestServiceGatewayRoutesReplicationToReplacementRuntime(t *testing.T) {
	firstRuntime := &fakeRuntime{
		pull: channeltransport.PullResponse{LeaderHW: 1},
	}
	first, err := NewService(Config{Runtime: firstRuntime})
	if err != nil {
		t.Fatalf("NewService(first): %v", err)
	}
	secondRuntime := &fakeRuntime{
		pull: channeltransport.PullResponse{LeaderHW: 2},
	}
	second, err := NewService(Config{Runtime: secondRuntime})
	if err != nil {
		t.Fatalf("NewService(second): %v", err)
	}
	gateway := NewServiceGateway(first)

	response, err := gateway.HandlePull(
		context.Background(), channeltransport.PullRequest{},
	)
	if err != nil || response.LeaderHW != 1 || firstRuntime.pullCalls != 1 {
		t.Fatalf("first HandlePull() = %#v, %v", response, err)
	}
	gateway.Replace(second)
	response, err = gateway.HandlePull(
		context.Background(), channeltransport.PullRequest{},
	)
	if err != nil || response.LeaderHW != 2 ||
		firstRuntime.pullCalls != 1 || secondRuntime.pullCalls != 1 {
		t.Fatalf("replacement HandlePull() = %#v, %v", response, err)
	}
}
