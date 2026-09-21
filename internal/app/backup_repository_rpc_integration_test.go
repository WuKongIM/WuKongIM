//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// This exercises production app wiring, the real RPC codec, target-local
// Controller resolution, credential decryption and signed S3 reads/writes.
func TestBackupRepositoryRPCUsesTargetCredentialsAndRejectsRotation(t *testing.T) {
	const accessKey = "fixture-access-key"
	marker := []byte("original coordinator marker")
	var mu sync.Mutex
	objects := map[string][]byte{"/archive/cluster/probes/one/marker": marker}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if !strings.Contains(r.Header.Get("Authorization"), "Credential="+accessKey+"/") {
			t.Error("object request was not signed with the target-local credential")
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", `"fixture-etag"`)
		w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		switch r.Method {
		case http.MethodHead, http.MethodGet:
			body, ok := objects[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
		case http.MethodPut:
			if _, exists := objects[r.URL.Path]; exists && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			var reader io.Reader = r.Body
			if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") || strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				reader = httputil.NewChunkedReader(r.Body)
			}
			body, err := io.ReadAll(io.LimitReader(reader, 4096))
			if err != nil {
				t.Error("read signed request body")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			objects[r.URL.Path] = body
		default:
			t.Errorf("unexpected object operation %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	cipher, err := backupinfra.NewCredentialCipher("backup-installation-secret", "cluster-backup")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Seal(backupinfra.ObjectStoreCredentials{AccessKey: accessKey, SecretKey: "fixture-secret-key"})
	if err != nil {
		t.Fatal("seal target credential")
	}
	node := &backupRepositoryWiringNode{backupWiringNode: newBackupWiringNode()}
	ids, err := newNodeMessageIDs(1)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	app := &App{cfg: Config{DataDir: dir, Manager: ManagerConfig{JWTSecret: "backup-installation-secret"}}, cluster: node, messageIDs: ids, logger: wklog.NewNop()}
	if err := app.wireBackup(clusterpkg.Config{NodeID: 1, DataDir: dir, Control: clusterpkg.ControlConfig{ClusterID: "cluster-backup"}}); err != nil {
		t.Fatal(err)
	}
	store := backupcontract.StoreConfig{Kind: backupcontract.StoreKindS3, Endpoint: server.URL, Region: "us-east-1", Bucket: "archive", Prefix: "cluster", PathStyle: true, CredentialRevision: 7, CredentialCiphertext: encrypted}
	stateStore, err := backupinfra.NewScheduledControllerStateStore(node)
	if err != nil {
		t.Fatal(err)
	}
	state := backupcontract.SystemState{Plan: &backupcontract.Plan{Store: store}}
	if err := stateStore.CompareAndSwap(context.Background(), 0, state); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(marker)
	command := backupcontract.RepositoryProbeCommand{Store: store, MarkerKey: "probes/one/marker", MarkerSHA256: hex.EncodeToString(sum[:]), ReceiptKey: "probes/one/node-1", ReceiptContent: "1:one"}
	// An unusable sender ciphertext proves that the receiver opened its own copy.
	command.Store.CredentialCiphertext = []byte("invalid-sender-ciphertext")
	client := accessnode.NewClient(&backupRepositoryRPCTransport{t: t, node: node})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.ProbeBackupRepository(ctx, 1, command); err != nil {
		t.Fatalf("probe through app-wired RPC: %v", err)
	}
	mu.Lock()
	receipt := bytes.Clone(objects["/archive/cluster/probes/one/node-1"])
	before := requests
	mu.Unlock()
	if string(receipt) != command.ReceiptContent {
		t.Fatal("target did not read marker and publish its receipt")
	}
	state.Plan.Store.CredentialRevision++
	if err := stateStore.CompareAndSwap(context.Background(), 1, state); err != nil {
		t.Fatal(err)
	}
	command.ReceiptKey = "probes/one/stale"
	if err := client.ProbeBackupRepository(ctx, 1, command); err == nil {
		t.Fatal("stale sender credential revision was accepted")
	}
	mu.Lock()
	after := requests
	mu.Unlock()
	if after != before {
		t.Fatal("stale credential reached object storage")
	}
	command.Store.CredentialRevision++
	command.ReceiptKey = "probes/one/rotated"
	if err := client.ProbeBackupRepository(ctx, 1, command); err != nil {
		t.Fatalf("matching rotated revision: %v", err)
	}
}

type backupRepositoryWiringNode struct {
	*backupWiringNode
	state controller.ClusterState
}

func (n *backupRepositoryWiringNode) LocalState(context.Context) (controller.ClusterState, error) {
	return n.state.Clone(), nil
}
func (n *backupRepositoryWiringNode) ReplaceScheduledBackupState(_ context.Context, expected uint64, next controller.ScheduledBackupState) error {
	if n.state.Revision != expected {
		return controller.ErrExpectedRevisionMismatch
	}
	n.state.Revision++
	state := next.Clone()
	n.state.ScheduledBackup = &state
	return nil
}

type backupRepositoryRPCTransport struct {
	t    *testing.T
	node *backupRepositoryWiringNode
}

func (n *backupRepositoryRPCTransport) CallRPC(ctx context.Context, id uint64, service uint8, body []byte) ([]byte, error) {
	if id != 1 {
		return nil, fmt.Errorf("unexpected node")
	}
	if bytes.Contains(body, []byte("credential_ciphertext")) {
		n.t.Error("credential crossed node RPC")
	}
	return n.node.rpc[service].HandleRPC(ctx, body)
}
