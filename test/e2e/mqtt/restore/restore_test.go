//go:build e2e

package restore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

// TestRestoreReactivatesPersistentMQTTWithoutProcessRestart observes the same
// process across two restore boundaries, including an exchange saved in flight.
func TestRestoreReactivatesPersistentMQTTWithoutProcessRestart(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) { testRestoreReactivation(t, count, false) })
	}
}

const releaseFault = "wkRestoreArchiveLeaseReleaseUnavailable"

// A positive job receipt cannot depend on another lease-cleanup transaction.
// Both restore cycles must finish while that old cleanup path is unavailable.
func TestRestoreAdmissionSurvivesUnavailableArchiveLeaseCleanup(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary backup gofail product binary")
	}
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) { testRestoreReactivation(t, count, true) })
	}
}

func testRestoreReactivation(t *testing.T, count int, unavailableCleanup bool) {
	const username, password = "mqtt-restore-admin", "mqtt-restore-password"
	users, err := json.Marshal([]map[string]any{{"username": username, "password": password, "permissions": []map[string]any{{"resource": "cluster.backup", "actions": []string{"r", "w"}}, {"resource": "cluster.restore", "actions": []string{"w"}}}}})
	require.NoError(t, err)
	addrs := make([]string, count)
	faults := make([]suite.GofailEndpoint, count)
	opts := []suite.Option{suite.WithManagerHTTP(), suite.WithSharedBackupRepository()}
	if reportRoot := os.Getenv("WK_E2E_MQTT_REPORT_DIR"); reportRoot != "" {
		opts = append(opts, suite.WithNodeLogRootDir(filepath.Join(reportRoot, fmt.Sprintf("%d-node-logs", count))), suite.WithWorkspaceRootDir(filepath.Join(reportRoot, "workspaces")))
	}
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		if unavailableCleanup {
			faults[i] = suite.ReserveGofailEndpoint(t)
			opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env()))
		}
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i], "WK_MANAGER_AUTH_ON": "true", "WK_MANAGER_JWT_SECRET": "mqtt-restore-jwt-secret", "WK_MANAGER_USERS": string(users)}))
	}
	s := suite.New(t)
	var n *suite.StartedNode
	var cluster *suite.StartedCluster
	// Convergence includes 256 partition file operations and fsync; it is not
	// a foreground latency gate. Preserve bounded packet budgets independently.
	phaseTimeout := 4 * time.Minute
	if count == 1 {
		n = s.StartSingleNodeCluster(opts...)
	} else {
		cluster = s.StartThreeNodeCluster(opts...)
		ready, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
		cancel()
		n = cluster.MustNode(1)
		// Three replicas require 768 partition stages/verifications/switches;
		// this bounds restore convergence, not foreground packet latency.
		phaseTimeout = 8 * time.Minute
	}
	diagnostics := n.DumpDiagnostics
	if cluster != nil {
		diagnostics = cluster.DumpDiagnostics
	}
	addr := addrs[count-1]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	phase := "provision"
	cycles, refusals := 0, 0
	failedResponseAdmitted, releaseFaultHits := false, 0
	failedResponseStatus := 0
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = n.Spec.RootDir
	}
	defer func() {
		report := map[string]any{"scenario": "mqtt-restore-reactivation", "nodes": count, "hash_slots": 256, "passed": !t.Failed(), "last_phase": phase, "restore_cycles": cycles, "maintenance_refusals": refusals, "process_restarts": 0}
		if unavailableCleanup {
			report["scenario"] = "restore-admission-with-unavailable-archive-cleanup"
			report["lease_cleanup_fault_enabled"] = true
			report["lease_cleanup_fault_hits"] = releaseFaultHits
			report["failed_response_restore_observed"] = failedResponseAdmitted
			report["failed_response_http_status"] = failedResponseStatus
			report["restore_mutation_retries"] = 0
		}
		if !t.Failed() {
			for _, assertion := range []string{"session_present", "packet_id_preserved", "dup_on_resume", "original_identity_preserved", "old_connection_closed", "post_backup_state_removed", "fresh_delivery_once"} {
				report[assertion] = true
			}
			report["resubscriptions"], report["extra_deliveries"] = 0, 0
		}
		data, e := json.MarshalIndent(report, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.MkdirAll(reportDir, 0755))
		name := "mqtt-restore-reactivation"
		if unavailableCleanup {
			name = "restore-admission-unavailable-cleanup"
		}
		path := filepath.Join(reportDir, fmt.Sprintf("%s-%d.json", name, count))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	}()
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	const group = "mqtt-restore-group"
	_, err = suite.PostJSON(ctx, "http://"+n.APIAddr()+"/channel", map[string]any{"channel_id": group, "channel_type": frame.ChannelTypeGroup, "subscribers": []string{"alice", "bob"}}, nil)
	require.NoError(t, err)
	// Restore acceptance uses an existing native history. Cold first-message
	// subscription admission remains a separate finding outside restore acceptance.
	warmup, err := suite.PostMessageSendEventually(ctx, n.APIAddr(), map[string]any{"from_uid": "alice", "channel_id": group, "channel_type": frame.ChannelTypeGroup, "client_msg_no": "restore-existing-history", "payload": base64.StdEncoding.EncodeToString([]byte("restore-existing-history"))})
	require.NoError(t, err, diagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), warmup.Reason)
	topic := "wk/v1/groups/" + base64.RawURLEncoding.EncodeToString([]byte(group)) + "/messages"
	connect := func(id string) *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addr, "bob", "bob-fixture-token", id, false, 86400, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, diagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	before := connect("restore-bob")
	require.False(t, before.Connack.SessionPresent)
	phase = "initial-subscription"
	sub, err := before.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
	if err != nil {
		if cluster != nil {
			for _, reason := range []string{"conflict", "deadline", "canceled", "unconfirmed", "unknown", "evidence", "fenced"} {
				call, cancel := context.WithTimeout(context.Background(), time.Second)
				value, e := suite.FetchMetricValue(call, cluster.MustNode(3).APIAddr(), "wukongim_mqtt_subscription_closures_total", map[string]string{"operation": "subscribe", "reason": reason})
				cancel()
				t.Logf("initial subscribe closure reason=%s value=%v metric_error=%v", reason, value, e)
			}
			t.Log(cluster.DumpDiagnostics())
		}
	}
	require.NoError(t, err, diagnostics())
	require.Equal(t, []byte{1}, sub.Reasons)
	send := func(no string) suite.MessageSendResponse {
		r, err := suite.PostMessageSendEventually(ctx, n.APIAddr(), map[string]any{"from_uid": "alice", "channel_id": group, "channel_type": frame.ChannelTypeGroup, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))})
		require.NoError(t, err, diagnostics())
		require.Equal(t, uint8(frame.ReasonSuccess), r.Reason)
		return r
	}
	receive := func(c *suite.MQTTClient) *paho.Publish {
		deadline, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		p, err := c.Receive(deadline)
		require.NoError(t, err, diagnostics())
		return p
	}
	identity := func(p *paho.Publish, ack suite.MessageSendResponse, no string) {
		require.Equal(t, []byte(no), p.Payload)
		require.NotNil(t, p.Properties)
		require.Equal(t, strconv.FormatInt(ack.MessageID, 10), p.Properties.User.Get("wk.message_id"))
		require.Equal(t, strconv.FormatUint(ack.MessageSeq, 10), p.Properties.User.Get("wk.message_seq"))
		require.Equal(t, no, p.Properties.User.Get("wk.client_msg_no"))
	}
	phase = "original-exchange"
	originalAck := send("restore-unacked")
	original := receive(before)
	identity(original, originalAck, "restore-unacked")
	require.False(t, original.Duplicate())
	require.NotZero(t, original.PacketID)
	backup := suite.NewBackupClient(n.ManagerAddr(), username, password)
	phase = "backup"
	t.Log("starting backup with unacknowledged exchange")
	var peers []*suite.BackupClient
	if cluster != nil {
		for _, peer := range cluster.Nodes {
			if peer.Spec.ID != n.Spec.ID {
				peers = append(peers, suite.NewBackupClient(peer.ManagerAddr(), username, password))
			}
		}
	}
	require.NoError(t, backup.EnableFilePlan(ctx, peers...), diagnostics())
	d := awaitDashboard(t, ctx, backup, n, phaseTimeout, func(d suite.BackupDashboard) bool {
		return d.State.ActiveBackup == nil && len(d.Archives) == 1 && d.Archives[0].Health == "healthy"
	})
	t.Log("backup published")
	archive := d.Archives[0].ID
	if unavailableCleanup {
		for _, fault := range faults {
			call, stop := context.WithTimeout(ctx, 3*time.Second)
			_, err := fault.WaitListed(call, releaseFault)
			if err == nil {
				err = fault.Enable(call, releaseFault, "return(true)")
			}
			stop()
			require.NoError(t, err)
		}
	}
	epoch := d.State.ManagerSessionEpoch
	late := connect("restore-post-backup-bob")
	require.False(t, late.Connack.SessionPresent)
	phase = "post-backup-message"
	send("restore-after-backup")
	requireHistory(t, ctx, n, group, []string{"restore-existing-history", "restore-unacked", "restore-after-backup"})
	for cycle := range 2 {
		phase = fmt.Sprintf("restore-mirrors-%d", cycle+1)
		// Admission preflight resolves each target's Controller mirror. The prior
		// completed backup/restore revision must be visible before a new mutation.
		for _, peer := range peers {
			awaitDashboard(t, ctx, peer, n, 30*time.Second, func(view suite.BackupDashboard) bool {
				return view.State.Revision >= d.State.Revision && view.State.ActiveBackup == nil && view.State.ActiveRestore == nil
			})
		}
		phase = fmt.Sprintf("maintenance-%d", cycle+1)
		t.Logf("starting restore cycle %d", cycle+1)
		// Race real CONNECT admission with the maintenance boundary. Any accepted
		// old-generation connection must close; all attempts remain bounded.
		type connectResult struct {
			client *suite.MQTTClient
			err    error
		}
		attempts := make(chan connectResult, 16)
		for i := range 16 {
			go func() {
				probe, stop := context.WithTimeout(ctx, 10*time.Second)
				defer stop()
				c, err := suite.ConnectMQTT(probe, addr, "bob", "bob-fixture-token", fmt.Sprintf("restore-race-%d-%d", cycle, i), true, 0)
				attempts <- connectResult{c, err}
			}()
		}
		job, err := backup.Restore(ctx, archive)
		if err != nil {
			var httpFailure *suite.HTTPStatusError
			if errors.As(err, &httpFailure) {
				failedResponseStatus = httpFailure.StatusCode
			}
			if unavailableCleanup {
				observe, stop := context.WithTimeout(ctx, 10*time.Second)
				for observe.Err() == nil {
					view, readErr := backup.Dashboard(observe)
					if readErr == nil && view.State.ActiveRestore != nil {
						failedResponseAdmitted = true
						break
					}
					select {
					case <-observe.Done():
					case <-time.After(100 * time.Millisecond):
					}
				}
				stop()
			}
			observed, readErr := backup.Dashboard(ctx)
			active := observed.State.ActiveRestore
			status := "none"
			if active != nil {
				status = active.Status
			}
			t.Logf("restore response error: observed_active_restore=%t status=%s dashboard_read_error=%t; request not retried", active != nil, status, readErr != nil)
		}
		if unavailableCleanup {
			call, stop := context.WithTimeout(ctx, 3*time.Second)
			hits, readErr := faults[0].Count(call, releaseFault)
			stop()
			require.NoError(t, readErr)
			releaseFaultHits = hits
		}
		require.NoError(t, err, diagnostics())
		if unavailableCleanup {
			require.Zero(t, releaseFaultHits, "known admission consumes the lease in the same transaction")
		}
		require.NotEmpty(t, job.ID)
		awaitDashboard(t, ctx, backup, n, phaseTimeout, func(d suite.BackupDashboard) bool {
			return d.State.ActiveRestore != nil && d.State.ActiveRestore.ID == job.ID && d.State.ActiveRestore.MaintenanceEntered
		})
		select {
		case <-before.Client.Done():
		case <-ctx.Done():
			t.Fatal("previous MQTT connection did not close")
		}
		probeCtx, done := context.WithTimeout(ctx, time.Second)
		denied, err := suite.ConnectMQTT(probeCtx, addr, "bob", "bob-fixture-token", "restore-maintenance-probe", true, 0)
		done()
		if denied != nil {
			_ = denied.Abort()
		}
		require.Error(t, err, "MQTT accepted a connection during restore")
		refusals++
		for range 16 {
			var attempt connectResult
			select {
			case attempt = <-attempts:
			case <-ctx.Done():
				t.Fatal("CONNECT admission did not finish")
			}
			if attempt.client != nil {
				closed, stop := context.WithTimeout(ctx, 3*time.Second)
				select {
				case <-attempt.client.Client.Done():
				case <-closed.Done():
					_ = attempt.client.Abort()
					t.Fatal("old generation CONNECT remained open")
				}
				stop()
				_ = attempt.client.Abort()
			} else {
				require.Error(t, attempt.err)
			}
		}
		phase = fmt.Sprintf("restore-completion-%d", cycle+1)
		d = awaitDashboard(t, ctx, backup, n, phaseTimeout, func(d suite.BackupDashboard) bool {
			if d.State.ActiveRestore != nil {
				return false
			}
			for _, task := range d.State.History {
				if task.ID == job.ID && task.Kind == "restore" {
					require.Equal(t, "succeeded", task.Status, diagnostics())
					return true
				}
			}
			return false
		})
		require.Greater(t, d.State.ManagerSessionEpoch, epoch)
		epoch = d.State.ManagerSessionEpoch
		phase = fmt.Sprintf("restore-readiness-%d", cycle+1)
		// Controller completion precedes asynchronous local maintenance mirrors.
		// Await the public admission contract before one MQTT reconnect attempt.
		ready, stopReady := context.WithTimeout(ctx, 60*time.Second)
		if cluster != nil {
			require.NoError(t, cluster.WaitHTTPReady(ready), diagnostics())
		} else {
			_, readyErr := n.Process.WaitHTTPReady(ready, n.APIAddr(), "/readyz")
			require.NoError(t, readyErr, diagnostics())
		}
		stopReady()
		phase = fmt.Sprintf("mqtt-resume-%d", cycle+1)
		t.Logf("restore cycle %d completed; checking MQTT replay", cycle+1)
		// A completed restore must have automatically reactivated MQTT. No broad
		// reconnect retry conceals a terminal registry or transition failure.
		addr = addrs[cycle%count] // Resume through a different ingress in the three-node cluster.
		after := connect("restore-bob")
		require.True(t, after.Connack.SessionPresent)
		replay := receive(after)
		identity(replay, originalAck, "restore-unacked")
		require.True(t, replay.Duplicate())
		require.Equal(t, original.PacketID, replay.PacketID)
		require.NoError(t, after.Client.Ack(replay))
		requireHistory(t, ctx, n, group, []string{"restore-existing-history", "restore-unacked"})
		postBackup := connect("restore-post-backup-bob")
		require.False(t, postBackup.Connack.SessionPresent)
		require.NoError(t, postBackup.Close())
		freshNo := fmt.Sprintf("restore-fresh-%d", cycle+1)
		freshAck := send(freshNo)
		fresh := receive(after)
		identity(fresh, freshAck, freshNo)
		require.False(t, fresh.Duplicate())
		require.NoError(t, after.Client.Ack(fresh))
		quiet, stop := context.WithTimeout(ctx, 300*time.Millisecond)
		extra, err := after.Receive(quiet)
		stop()
		require.ErrorIs(t, err, context.DeadlineExceeded, "unexpected extra delivery: %v", extra)
		before = after
		cycles++
	}
	phase = "complete"
}

func awaitDashboard(t *testing.T, ctx context.Context, c *suite.BackupClient, n *suite.StartedNode, timeout time.Duration, matches func(suite.BackupDashboard) bool) suite.BackupDashboard {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last suite.BackupDashboard
	for {
		var err error
		last, err = c.Dashboard(ctx)
		require.NoError(t, err, n.DumpDiagnostics())
		if matches(last) {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("backup state did not converge: active_restore=%+v\n%s", last.State.ActiveRestore, n.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireHistory(t *testing.T, ctx context.Context, n *suite.StartedNode, group string, want []string) {
	t.Helper()
	var page struct {
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": "bob", "channel_id": group, "channel_type": frame.ChannelTypeGroup, "start_message_seq": 0, "limit": 10}, &page)
	require.NoError(t, err, n.DumpDiagnostics())
	var got []string
	for _, m := range page.Messages {
		got = append(got, m.ClientMsgNo)
	}
	require.Equal(t, want, got)
}
