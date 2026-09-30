package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const sendPermissionRPCServiceID = clusternet.RPCNodeSendPermissions
const sendPermissionMaxBytes = 1 << 20

// Execution must cover independent callers waiting on fresh quorum barriers:
// sixteen envelopes saturate the 500 SEND/s lifecycle workload when ReadIndex
// round trips slow down. Sixty-four remain a hard node-wide decode/read bound
// (at most 256 Slot workers); each request/reply is separately capped at 1 MiB.
// Waiting retains its independent count, undecoded-byte and two-second bounds.
const sendPermissionMaxExecuting = 64
const sendPermissionMaxWaiting = 1024
const sendPermissionMaxWaitingBytes = 16 * sendPermissionMaxBytes
const sendPermissionMaxWait = 2 * time.Second

var ErrPermissionBusy = errors.New("permission read admission busy")

// SendPermissionFence is the complete distributed authority identity of a Slot.
// Hash Slot ownership is carried separately on each read and rederived remotely.
type SendPermissionFence struct {
	SlotID        uint64
	LeaderNodeID  uint64
	LeaderTerm    uint64
	ConfigEpoch   uint64
	RouteRevision uint64
}

// SendPermissionRoute is an aligned route from one immutable publication.
type SendPermissionRoute struct {
	Fence    SendPermissionFence
	HashSlot uint16
	Err      error
}
type sendPermissionRouter interface {
	SendPermissionRoutes([]string) []SendPermissionRoute
}
type sendPermissionRead struct {
	Index    int
	HashSlot uint16
	Read     PermissionMetadataRead
}
type sendPermissionGroup struct {
	Fence SendPermissionFence
	Reads []sendPermissionRead
}
type sendPermissionRequest struct {
	Format int                   `json:"format"`
	Groups []sendPermissionGroup `json:"groups"`
}
type sendPermissionValue struct {
	Kind  PermissionMetadataReadKind
	Index int
	Fact  metadb.PermissionFact
}
type sendPermissionGroupReply struct {
	Fence   SendPermissionFence
	Status  string
	Results []sendPermissionValue
}
type sendPermissionReply struct {
	Error  string                     `json:"error,omitempty"`
	Format int                        `json:"format"`
	Groups []sendPermissionGroupReply `json:"groups"`
}

func permissionReadKey(q PermissionMetadataRead) (string, error) {
	switch q.Kind {
	case PermissionMetadataReadUserSendPolicy:
		if q.UID == "" || q.ChannelID != "" || q.ChannelType != 0 {
			return "", metadb.ErrInvalidArgument
		}
		return q.UID, nil
	case PermissionMetadataReadChannel, PermissionMetadataReadSubscriberContains, PermissionMetadataReadSubscriberHasAny:
		if q.ChannelID == "" || q.ChannelType < 1 || q.ChannelType > 255 {
			return "", metadb.ErrInvalidArgument
		}
		if q.Kind == PermissionMetadataReadSubscriberContains && q.UID == "" {
			return "", metadb.ErrInvalidArgument
		}
		return q.ChannelID, nil
	default:
		return "", metadb.ErrInvalidArgument
	}
}

// ApplySendBan forwards one entity-scoped mutation to Raft and returns the
// actual atomic result, never a speculative read-modify-write from the ingress.
func (s *Store) ApplySendBan(ctx context.Context, q metadb.SendBanMutation) (metadb.SendBanResult, error) {
	var out metadb.SendBanResult
	raw, err := metafsm.EncodeSendBanCommand(q)
	if err != nil {
		return out, err
	}
	key := q.UID
	if key == "" {
		key = q.ChannelID
	}
	data, err := proposeWithHashSlotResult(ctx, s.cluster, s.cluster.SlotForKey(key), hashSlotForKey(s.cluster, key), raw)
	if err != nil {
		return out, err
	}
	if string(data) == metafsm.ApplyResultHashSlotFenced || string(data) == metafsm.ApplyResultStaleMeta {
		return out, metadb.ErrStaleMeta
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	if out.Status == "" {
		return out, metadb.ErrCorruptValue
	}
	return out, nil
}

// readSendPermissionMetadataBatch coalesces different Slots at the same leader
// into node-scoped envelopes. Successful facts are request-scoped only; each
// serving Slot establishes a fresh quorum/apply barrier and snapshot.
func (s *Store) readSendPermissionMetadataBatch(ctx context.Context, reads []PermissionMetadataRead) []PermissionMetadataReadResult {
	out := make([]PermissionMetadataReadResult, len(reads))
	if len(reads) == 0 {
		return out
	}
	if s == nil || s.cluster == nil || s.db == nil || len(reads) > permissionBatchMaxReads {
		return permissionMetadataErrorResults(out, metadb.ErrInvalidArgument)
	}
	router, ok := s.cluster.(sendPermissionRouter)
	if !ok {
		return permissionMetadataErrorResults(out, ErrReadStaleRoute)
	}
	keys := make([]string, len(reads))
	pending := make([]int, 0, len(reads))
	for i, q := range reads {
		key, err := permissionReadKey(q)
		keys[i] = key
		if err != nil {
			out[i].Err = err
		} else {
			pending = append(pending, i)
		}
	}
	for attempt := 0; attempt < 2 && len(pending) > 0; attempt++ {
		if err := ctx.Err(); err != nil {
			for _, i := range pending {
				out[i].Err = err
			}
			break
		}
		routeStarted := s.permissionStart()
		subset := make([]string, len(pending))
		for i, index := range pending {
			subset[i] = keys[index]
		}
		routes := router.SendPermissionRoutes(subset)
		if len(routes) != len(pending) {
			for _, i := range pending {
				out[i].Err = metadb.ErrCorruptValue
			}
			break
		}
		groups := make(map[SendPermissionFence][]sendPermissionRead)
		for j, i := range pending {
			r := routes[j]
			if r.Err != nil {
				out[i].Err = r.Err
				continue
			}
			if r.Fence.SlotID == 0 || r.Fence.LeaderNodeID == 0 {
				out[i].Err = ErrReadStaleRoute
				continue
			}
			groups[r.Fence] = append(groups[r.Fence], sendPermissionRead{Index: i, HashSlot: r.HashSlot, Read: reads[i]})
		}
		fences := make([]SendPermissionFence, 0, len(groups))
		for f := range groups {
			fences = append(fences, f)
		}
		sort.Slice(fences, func(i, j int) bool {
			if fences[i].LeaderNodeID != fences[j].LeaderNodeID {
				return fences[i].LeaderNodeID < fences[j].LeaderNodeID
			}
			return fences[i].SlotID < fences[j].SlotID
		})
		var envelopes []sendPermissionRequest
		var current sendPermissionRequest
		var node uint64
		estimated := 0
		flush := func() {
			if len(current.Groups) > 0 {
				envelopes = append(envelopes, current)
			}
			current = sendPermissionRequest{Format: 1}
			estimated = 128
		}
		flush()
		for _, f := range fences {
			if node != f.LeaderNodeID {
				flush()
				node = f.LeaderNodeID
			}
			for _, read := range groups[f] {
				raw, err := json.Marshal(read)
				// Reserve reply bytes as well as request bytes before allocating an envelope.
				cost := 2*len(raw) + 1024
				if err != nil || cost > sendPermissionMaxBytes/2 {
					out[read.Index].Err = metadb.ErrInvalidArgument
					continue
				}
				if estimated+cost > sendPermissionMaxBytes/2 {
					flush()
				}
				if len(current.Groups) == 0 || current.Groups[len(current.Groups)-1].Fence != f {
					current.Groups = append(current.Groups, sendPermissionGroup{Fence: f})
					estimated += 256
				}
				g := &current.Groups[len(current.Groups)-1]
				g.Reads = append(g.Reads, read)
				estimated += cost
			}
		}
		flush()
		s.permissionStage("route", "ok", routeStarted)
		runSlotMetadataBatchWorkers(goruntimeregistry.TaskSlotPermissionBatch, len(envelopes), func(i int) {
			q := envelopes[i]
			reply, err := s.callSendPermission(ctx, q)
			if err == nil {
				err = validateSendPermissionReply(q, reply)
			}
			if err != nil {
				for _, g := range q.Groups {
					for _, r := range g.Reads {
						out[r.Index].Err = err
					}
				}
				return
			}
			for j, g := range reply.Groups {
				var groupErr error
				switch g.Status {
				case "ok":
				case "stale_route":
					groupErr = ErrReadStaleRoute
				case "busy":
					groupErr = ErrPermissionBusy
				default:
					groupErr = errNoLeader
				}
				for k, r := range q.Groups[j].Reads {
					if groupErr != nil {
						out[r.Index].Err = groupErr
						continue
					}
					fact := g.Results[k].Fact
					out[r.Index] = PermissionMetadataReadResult{Channel: fact.Channel, Found: fact.Found, Value: fact.Value, UserPolicy: fact.UserPolicy}
				}
			}
		})
		retry := pending[:0]
		for _, i := range pending {
			if errors.Is(out[i].Err, ErrReadStaleRoute) {
				retry = append(retry, i)
			}
		}
		pending = retry
	}
	return out
}

func validateSendPermissionReply(q sendPermissionRequest, r sendPermissionReply) error {
	if r.Error != "" {
		if r.Format == 1 && r.Error == "busy" && len(r.Groups) == 0 {
			return ErrPermissionBusy
		}
		return metadb.ErrCorruptValue
	}
	if r.Format != 1 || len(q.Groups) != len(r.Groups) {
		return metadb.ErrCorruptValue
	}
	for i, g := range q.Groups {
		got := r.Groups[i]
		if got.Fence != g.Fence {
			return metadb.ErrCorruptValue
		}
		if got.Status != "ok" {
			if got.Status != "stale_route" && got.Status != "unavailable" && got.Status != "busy" {
				return metadb.ErrCorruptValue
			}
			if len(got.Results) != 0 {
				return metadb.ErrCorruptValue
			}
			continue
		}
		if len(g.Reads) != len(got.Results) {
			return metadb.ErrCorruptValue
		}
		for j, read := range g.Reads {
			if got.Results[j].Index != read.Index || got.Results[j].Kind != read.Read.Kind || !validSendPermissionFact(read.Read, got.Results[j].Fact) {
				return metadb.ErrCorruptValue
			}
		}
	}
	return nil
}

func (s *Store) callSendPermission(ctx context.Context, q sendPermissionRequest) (reply sendPermissionReply, err error) {
	if s.cluster.IsLocal(multiraft.NodeID(q.Groups[0].Fence.LeaderNodeID)) {
		s.permissionCount("local_envelopes", 1)
		return s.serveSendPermissions(ctx, q)
	}
	started := s.permissionStart()
	defer func() { s.permissionStage("rpc", sendPermissionErrorClass(err), started) }()
	raw, err := json.Marshal(q)
	if err != nil || len(raw) > sendPermissionMaxBytes {
		return sendPermissionReply{}, metadb.ErrInvalidArgument
	}
	s.permissionCount("node_envelopes", 1)
	s.permissionCount("request_bytes", len(raw))
	body, err := s.cluster.RPCService(ctx, multiraft.NodeID(q.Groups[0].Fence.LeaderNodeID), 0, sendPermissionRPCServiceID, raw)
	if err != nil {
		return sendPermissionReply{}, err
	}
	s.permissionCount("response_bytes", len(body))
	var out sendPermissionReply
	if err = decodeSendPermissionJSON(body, &out); err != nil {
		return out, err
	}
	return out, nil
}
func decodeSendPermissionJSON(raw []byte, out any) error {
	if len(raw) > sendPermissionMaxBytes {
		return metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return metadb.ErrInvalidArgument
	}
	if d.Decode(new(any)) != io.EOF {
		return metadb.ErrInvalidArgument
	}
	return nil
}
func (s *Store) handleSendPermissionRPC(ctx context.Context, raw []byte) ([]byte, error) {
	// Bound concurrent decoding as well as barrier/snapshot work. Each admitted
	// wire envelope is limited to 1 MiB before any JSON allocation.
	if len(raw) > sendPermissionMaxBytes {
		return nil, metadb.ErrInvalidArgument
	}
	release, err := s.acquireSendPermissionEnvelope(ctx, len(raw))
	if errors.Is(err, ErrPermissionBusy) {
		return []byte(`{"format":1,"error":"busy","groups":[]}`), nil
	}
	if err != nil {
		return nil, err
	}
	defer release()
	var q sendPermissionRequest
	if err := decodeSendPermissionJSON(raw, &q); err != nil {
		return nil, err
	}
	r, err := s.serveAdmittedSendPermissions(ctx, q)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(body) > sendPermissionMaxBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return body, nil
}

// acquireSendPermissionEnvelope bounds decoding and execution for remote and
// local envelopes together. Waiting is separately bounded by count, retained
// bytes and time; queued work cannot decode or establish a read barrier until
// it owns a permit. waitBytes is the undecoded size a remote envelope keeps
// while queued; local callers pass zero because their request is already owned.
func (s *Store) acquireSendPermissionEnvelope(ctx context.Context, waitBytes int) (func(), error) {
	started := s.permissionStart()
	fail := func(err error) (func(), error) {
		s.permissionStage("admission", sendPermissionErrorClass(err), started)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	s.permissionGateMu.Lock()
	if s.permissionExecuting < sendPermissionMaxExecuting {
		s.permissionExecuting++
		s.permissionGateMu.Unlock()
	} else {
		if len(s.permissionWaiters) == sendPermissionMaxWaiting ||
			s.permissionWaitingBytes+waitBytes > sendPermissionMaxWaitingBytes {
			s.permissionGateMu.Unlock()
			return fail(ErrPermissionBusy)
		}
		waiter := &sendPermissionWaiter{ready: make(chan struct{}), bytes: waitBytes}
		s.permissionWaiters = append(s.permissionWaiters, waiter)
		s.permissionWaitingBytes += waitBytes
		s.permissionWaiting.Add(1)
		timer := time.NewTimer(sendPermissionMaxWait)
		s.permissionGateMu.Unlock()
		var err error
		select {
		case <-waiter.ready:
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
			err = ErrPermissionBusy
		}
		timer.Stop()
		if err != nil {
			s.permissionGateMu.Lock()
			if waiter.granted {
				// Assignment won the race with cancellation/timeout. Return its
				// reserved execution position without decoding or reading facts.
				s.releaseSendPermissionPermitLocked()
			} else {
				for i, queued := range s.permissionWaiters {
					if queued == waiter {
						s.removeSendPermissionWaiterLocked(i)
						break
					}
				}
			}
			s.permissionGateMu.Unlock()
			return fail(err)
		}
	}
	// A cancellation racing with a free permit must return that permit without
	// decoding or beginning a new authority read.
	if err := ctx.Err(); err != nil {
		s.releaseSendPermissionPermit()
		return fail(err)
	}
	s.permissionInflight.Add(1)
	s.permissionStage("admission", "ok", started)
	if s.permissionObserver != nil {
		s.permissionObserver.ObserveSendPermissionInflight(1)
	}
	return func() {
		s.permissionInflight.Add(-1)
		if s.permissionObserver != nil {
			s.permissionObserver.ObserveSendPermissionInflight(-1)
		}
		s.releaseSendPermissionPermit()
	}, nil
}

// sendPermissionWaiter owns one bounded queue position until it is removed or
// assigned a permit. granted is protected by permissionGateMu.
type sendPermissionWaiter struct {
	ready   chan struct{}
	granted bool
	// bytes is the queued wire size charged to permissionWaitingBytes.
	bytes int
}

// removeSendPermissionWaiterLocked releases a queue position and its bytes
// before waking its owner. The queue is bounded by sendPermissionMaxWaiting and
// retains no removed pointers.
func (s *Store) removeSendPermissionWaiterLocked(index int) {
	s.permissionWaitingBytes -= s.permissionWaiters[index].bytes
	copy(s.permissionWaiters[index:], s.permissionWaiters[index+1:])
	last := len(s.permissionWaiters) - 1
	s.permissionWaiters[last] = nil
	s.permissionWaiters = s.permissionWaiters[:last]
	s.permissionWaiting.Add(-1)
}

func (s *Store) releaseSendPermissionPermit() {
	s.permissionGateMu.Lock()
	s.releaseSendPermissionPermitLocked()
	s.permissionGateMu.Unlock()
}

// releaseSendPermissionPermitLocked transfers an execution position directly to
// the oldest waiter, or frees it when none remain. New arrivals cannot observe a
// transferred permit still consuming a waiting position.
func (s *Store) releaseSendPermissionPermitLocked() {
	if len(s.permissionWaiters) == 0 {
		s.permissionExecuting--
		return
	}
	waiter := s.permissionWaiters[0]
	s.removeSendPermissionWaiterLocked(0)
	waiter.granted = true
	close(waiter.ready)
}

func (s *Store) serveSendPermissions(ctx context.Context, q sendPermissionRequest) (sendPermissionReply, error) {
	release, err := s.acquireSendPermissionEnvelope(ctx, 0)
	if err != nil {
		return sendPermissionReply{}, err
	}
	defer release()
	return s.serveAdmittedSendPermissions(ctx, q)
}

func (s *Store) serveAdmittedSendPermissions(ctx context.Context, q sendPermissionRequest) (sendPermissionReply, error) {
	out := sendPermissionReply{Format: 1}
	if q.Format != 1 || len(q.Groups) == 0 || len(q.Groups) > permissionBatchMaxReads {
		return out, metadb.ErrInvalidArgument
	}
	seen := make(map[int]struct{})
	total := 0
	for _, g := range q.Groups {
		if len(g.Reads) == 0 {
			return out, metadb.ErrInvalidArgument
		}
		total += len(g.Reads)
		if total > permissionBatchMaxReads {
			return out, metadb.ErrInvalidArgument
		}
		for _, r := range g.Reads {
			if r.Index < 0 || r.Index >= permissionBatchMaxReads {
				return out, metadb.ErrInvalidArgument
			}
			if _, exists := seen[r.Index]; exists {
				return out, metadb.ErrInvalidArgument
			}
			seen[r.Index] = struct{}{}
			if _, err := permissionReadKey(r.Read); err != nil {
				return out, err
			}
		}
	}
	out.Groups = make([]sendPermissionGroupReply, len(q.Groups))
	s.permissionCount("slot_groups", len(q.Groups))
	runSlotMetadataBatchWorkers(goruntimeregistry.TaskSlotPermissionBatch, len(q.Groups), func(i int) { out.Groups[i] = s.readSendPermissionSlot(ctx, q.Groups[i]) })
	return out, nil
}
func (s *Store) readSendPermissionSlot(ctx context.Context, g sendPermissionGroup) sendPermissionGroupReply {
	out := sendPermissionGroupReply{Fence: g.Fence, Status: "unavailable"}
	admission, ok := s.cluster.(interface {
		AcquireSendPermissionRead(context.Context) (func(), error)
	})
	if !ok {
		return out
	}
	release, err := admission.AcquireSendPermissionRead(ctx)
	if err != nil || release == nil {
		return out
	}
	defer release()

	router, ok := s.cluster.(sendPermissionRouter)
	if !ok {
		return out
	}
	keys := make([]string, len(g.Reads))
	for i, r := range g.Reads {
		keys[i], _ = permissionReadKey(r.Read)
	}
	validate := func() bool {
		routes := router.SendPermissionRoutes(keys)
		if len(routes) != len(keys) {
			return false
		}
		for i, r := range routes {
			if r.Err != nil || r.Fence != g.Fence || r.HashSlot != g.Reads[i].HashSlot || !s.cluster.IsLocal(multiraft.NodeID(r.Fence.LeaderNodeID)) {
				return false
			}
		}
		return true
	}
	if !validate() {
		out.Status = "stale_route"
		return out
	}
	barrier, ok := s.cluster.(interface {
		ReadSlotBarrier(context.Context, multiraft.SlotID) error
	})
	if !ok {
		return out
	}
	barrierStarted := s.permissionStart()
	if err := barrier.ReadSlotBarrier(ctx, multiraft.SlotID(g.Fence.SlotID)); err != nil {
		s.permissionStage("barrier", sendPermissionErrorClass(err), barrierStarted)
		return out
	}
	s.permissionStage("barrier", "ok", barrierStarted)
	reads := make([]metadb.PermissionFactRead, len(g.Reads))
	for i, r := range g.Reads {
		kind := metadb.PermissionFactKind(r.Read.Kind)
		if r.Read.Kind == PermissionMetadataReadUserSendPolicy {
			kind = metadb.PermissionFactUser
		}
		reads[i] = metadb.PermissionFactRead{Kind: kind, HashSlot: r.HashSlot, ChannelID: r.Read.ChannelID, ChannelType: r.Read.ChannelType, UID: r.Read.UID}
	}
	snapshotStarted := s.permissionStart()
	facts, err := s.db.ReadPermissionSnapshot(ctx, reads)
	s.permissionStage("snapshot", sendPermissionErrorClass(err), snapshotStarted)
	if err != nil {
		return out
	}
	if !validate() {
		out.Status = "stale_route"
		return out
	}
	out.Status = "ok"
	out.Results = make([]sendPermissionValue, len(facts))
	for i, f := range facts {
		out.Results[i] = sendPermissionValue{Index: g.Reads[i].Index, Kind: g.Reads[i].Read.Kind, Fact: f}
	}
	return out
}

// UpdateChannelInfo changes flags and optional policy in one Slot proposal.
func (s *Store) UpdateChannelInfo(ctx context.Context, q metadb.ChannelInfoMutation) (metadb.SendBanResult, error) {
	var out metadb.SendBanResult
	raw, err := metafsm.EncodeChannelInfoCommand(q)
	if err != nil {
		return out, err
	}
	data, err := proposeWithHashSlotResult(ctx, s.cluster, s.cluster.SlotForKey(q.ChannelID), hashSlotForKey(s.cluster, q.ChannelID), raw)
	if err != nil {
		return out, err
	}
	if string(data) == metafsm.ApplyResultHashSlotFenced || string(data) == metafsm.ApplyResultStaleMeta {
		return out, metadb.ErrStaleMeta
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	if out.Status == "" {
		return out, metadb.ErrCorruptValue
	}
	return out, nil
}

func validSendPermissionFact(q PermissionMetadataRead, f metadb.PermissionFact) bool {
	switch q.Kind {
	case PermissionMetadataReadChannel:
		if f.Value || f.UserPolicy != (metadb.SendBanResult{}) {
			return false
		}
		if !f.Found {
			return f.Channel == (metadb.Channel{})
		}
		return f.Channel.ChannelID == q.ChannelID && f.Channel.ChannelType == q.ChannelType && (f.Channel.SendBan == 0 || f.Channel.SendBan == 1)
	case PermissionMetadataReadUserSendPolicy:
		if f.Channel != (metadb.Channel{}) || f.Value || f.UserPolicy.Status != "ok" || (f.UserPolicy.SendBan != 0 && f.UserPolicy.SendBan != 1) {
			return false
		}
		return f.Found || (f.UserPolicy.SendBan == 0 && f.UserPolicy.Version == 0)
	default:
		return !f.Found && f.Channel == (metadb.Channel{}) && f.UserPolicy == (metadb.SendBanResult{})
	}
}

func sendPermissionErrorClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrReadStaleRoute):
		return "stale_route"
	case errors.Is(err, ErrPermissionBusy):
		return "busy"
	case errors.Is(err, metadb.ErrInvalidArgument) || errors.Is(err, metadb.ErrCorruptValue):
		return "invalid"
	default:
		return "unavailable"
	}
}
