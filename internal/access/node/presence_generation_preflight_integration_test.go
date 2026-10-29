//go:build integration

package node

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type forbiddenGenerationReceiptWire []byte

func (w forbiddenGenerationReceiptWire) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return w, nil
}

// Failure inventory: an owner page or UID reply must reject either activation
// receipt before allocating its UID, even when no result credit is available.
// The supplied wire already exists before allocation measurement. The bound is
// on decoder ownership, not transport buffers, allocator internals or RSS.
func TestGenerationPagePreflightRejectsHiddenReceiptBeforeIdentityAllocation(t *testing.T) {
	for _, op := range []byte{generationPage, generationReadUIDs} {
		for _, field := range []string{"receipt", "register_receipt"} {
			name := map[byte]string{generationPage: "page", generationReadUIDs: "uids"}[op] + "/" + field
			t.Run(name, func(t *testing.T) {
				route := wireRoute()
				route.UID = strings.Repeat("x", 4<<20)
				receipt := &authority.ActivationReceipt{Claim: wireGeneration(), Owner: wireCut(), Identity: route.Identity(), OwnerSeq: route.OwnerSeq}
				resp := generationResponse{Op: op, Status: rpcStatusOK, Claim: wireGeneration(), Owner: wireCut(), Sequence: 1, Exhausted: true}
				if field == "receipt" {
					resp.Receipt = receipt
				} else {
					resp.Register.Receipt = receipt
				}
				body, err := encodeGenerationResponse(resp)
				if err != nil {
					t.Fatal(err)
				}
				budget := presence.NewRecoveryResultBudget(0, 0)
				client := NewClient(forbiddenGenerationReceiptWire(body))
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				for range 3 {
					if op == generationPage {
						_, err = decodeGenerationPage(context.Background(), body, authority.SlotPageRequest{Claim: wireGeneration(), Owner: wireCut(), Sequence: 1, MaxRows: 4096, MaxBytes: 4 << 20}, budget)
					} else {
						_, err = client.ReadPresenceGenerationUIDs(context.Background(), wireGeneration(), wireCut(), []string{"u"}, budget)
					}
					if !errors.Is(err, errGenerationWire) {
						t.Fatalf("forbidden receipt must fail wire validation: %v", err)
					}
				}
				runtime.ReadMemStats(&after)
				runtime.KeepAlive(body)
				if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 512<<10 {
					t.Fatalf("invalid receipt allocated %d bytes before result admission; maximum 512KiB for three rejected frames", allocated)
				}
				if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 {
					t.Fatalf("invalid receipt retained result credit: %+v", stats)
				}
			})
		}
	}
}
