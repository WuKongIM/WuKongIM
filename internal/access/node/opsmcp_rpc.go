package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// OpsMCPRPCServiceID is the cluster RPC service for Manager-to-owner MCP forwarding.
const OpsMCPRPCServiceID uint8 = clusternet.RPCOpsMCP

const (
	opsMCPRPCOpForward      = "forward"
	opsMCPRPCOpProfile      = "profile"
	opsMCPRPCOpProfileLease = "profile_lease"
	opsMCPRPCOpAudits       = "audits"
)

// OpsMCPForwardExecutor executes one token-free request after owner revalidation.
type OpsMCPForwardExecutor interface {
	ExecuteForward(context.Context, opscontract.ForwardRequest) (opscontract.ForwardResponse, error)
}

// OpsMCPProfileExecutor captures one bounded target-local runtime profile.
type OpsMCPProfileExecutor interface {
	CaptureProfile(context.Context, opscontract.ProfileRequest) (opscontract.ProfileResponse, error)
}

// OpsMCPProfileLeaseExecutor consumes owner-local profile authorizations.
type OpsMCPProfileLeaseExecutor interface {
	AuthorizeProfileLease(context.Context, opscontract.ProfileLeaseRequest) error
}

// OpsMCPAuditReader reads recent bounded local audit summaries.
type OpsMCPAuditReader interface {
	RecentAudits(context.Context, int) ([]opscontract.AuditEntry, error)
}

// OpsMCPRPCAdapter adapts typed internal RPC payloads to owner and target runtimes.
type OpsMCPRPCAdapter struct {
	forward OpsMCPForwardExecutor
	profile OpsMCPProfileExecutor
	audits  OpsMCPAuditReader
	leases  OpsMCPProfileLeaseExecutor
}

// NewOpsMCPRPCAdapter creates the bounded operations MCP RPC adapter.
func NewOpsMCPRPCAdapter(forward OpsMCPForwardExecutor, profile OpsMCPProfileExecutor, audits ...OpsMCPAuditReader) *OpsMCPRPCAdapter {
	var auditReader OpsMCPAuditReader
	if len(audits) > 0 {
		auditReader = audits[0]
	}
	return NewOpsMCPRPCAdapterWithServices(forward, profile, auditReader, nil)
}

// NewOpsMCPRPCAdapterWithServices creates the complete operations MCP RPC adapter.
func NewOpsMCPRPCAdapterWithServices(
	forward OpsMCPForwardExecutor,
	profile OpsMCPProfileExecutor,
	audits OpsMCPAuditReader,
	leases OpsMCPProfileLeaseExecutor,
) *OpsMCPRPCAdapter {
	return &OpsMCPRPCAdapter{forward: forward, profile: profile, audits: audits, leases: leases}
}

// HandleRPC handles one internal operations MCP request.
func (a *OpsMCPRPCAdapter) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	var request opsMCPRPCRequest
	if err := json.Unmarshal(payload, &request); err != nil || request.Version != opscontract.RPCVersion {
		return nil, fmt.Errorf("internal/access/node: invalid operations MCP RPC request")
	}
	response := opsMCPRPCResponse{Version: opscontract.RPCVersion}
	switch request.Op {
	case opsMCPRPCOpForward:
		if a == nil || a.forward == nil || request.Forward == nil ||
			request.CallerNodeID == 0 || request.Forward.IngressNodeID != request.CallerNodeID ||
			len(request.Forward.Payload) > opscontract.MaxForwardRequestBytes {
			return nil, fmt.Errorf("internal/access/node: operations MCP owner unavailable")
		}
		result, err := a.forward.ExecuteForward(ctx, *request.Forward)
		if err != nil {
			return nil, err
		}
		if len(result.Payload) > opscontract.MaxForwardResponseBytes {
			return nil, fmt.Errorf("internal/access/node: operations MCP response too large")
		}
		response.Forward = &result
	case opsMCPRPCOpProfile:
		if a == nil || a.profile == nil || request.Profile == nil ||
			request.CallerNodeID == 0 || request.Profile.OwnerNodeID != request.CallerNodeID {
			return nil, fmt.Errorf("internal/access/node: operations MCP profile unavailable")
		}
		result, err := a.profile.CaptureProfile(ctx, *request.Profile)
		if err != nil {
			return nil, err
		}
		if len(result.Payload) > opscontract.MaxProfileBytes {
			return nil, fmt.Errorf("internal/access/node: operations MCP profile too large")
		}
		response.Profile = &result
	case opsMCPRPCOpProfileLease:
		if a == nil || a.leases == nil || request.ProfileLease == nil ||
			request.CallerNodeID == 0 || request.ProfileLease.TargetNodeID != request.CallerNodeID {
			return nil, fmt.Errorf("internal/access/node: operations MCP profile lease unavailable")
		}
		if err := a.leases.AuthorizeProfileLease(ctx, *request.ProfileLease); err != nil {
			return nil, err
		}
		response.ProfileLease = &opscontract.ProfileLeaseResponse{
			Version: opscontract.RPCVersion, Allowed: true,
		}
	case opsMCPRPCOpAudits:
		if a == nil || a.audits == nil || request.CallerNodeID == 0 || request.Audits == nil ||
			request.Audits.Limit < 1 || request.Audits.Limit > opscontract.MaxAuditEntries {
			return nil, fmt.Errorf("internal/access/node: operations MCP audits unavailable")
		}
		entries, err := a.audits.RecentAudits(ctx, request.Audits.Limit)
		if err != nil {
			return nil, err
		}
		response.Audits = &opscontract.AuditResponse{
			Version: opscontract.RPCVersion, Entries: entries,
		}
	default:
		return nil, fmt.Errorf("internal/access/node: unknown operations MCP RPC operation")
	}
	return json.Marshal(response)
}

// VerifyOpsMCPProfileLease asks the configured owner to consume one one-time
// authorization before this target begins profile capture.
func (c *Client) VerifyOpsMCPProfileLease(ctx context.Context, ownerNodeID uint64, request opscontract.ProfileLeaseRequest) error {
	if ownerNodeID == 0 || request.Version != opscontract.RPCVersion ||
		request.OwnerNodeID != ownerNodeID || request.TargetNodeID == 0 || request.LeaseID == "" {
		return fmt.Errorf("internal/access/node: invalid operations MCP profile lease request")
	}
	response, err := c.callOpsMCP(ctx, ownerNodeID, opsMCPRPCRequest{
		Version: opscontract.RPCVersion, Op: opsMCPRPCOpProfileLease, ProfileLease: &request,
	})
	if err != nil {
		return err
	}
	if response.ProfileLease == nil || response.ProfileLease.Version != opscontract.RPCVersion ||
		!response.ProfileLease.Allowed {
		return fmt.Errorf("internal/access/node: invalid operations MCP profile lease response")
	}
	return nil
}

// ReadOpsMCPAudits reads one node's recent local audit summaries.
func (c *Client) ReadOpsMCPAudits(ctx context.Context, nodeID uint64, limit int) ([]opscontract.AuditEntry, error) {
	if nodeID == 0 || limit < 1 || limit > opscontract.MaxAuditEntries {
		return nil, fmt.Errorf("internal/access/node: invalid operations MCP audit request")
	}
	response, err := c.callOpsMCP(ctx, nodeID, opsMCPRPCRequest{
		Version: opscontract.RPCVersion, Op: opsMCPRPCOpAudits,
		Audits: &opscontract.AuditRequest{Version: opscontract.RPCVersion, Limit: limit},
	})
	if err != nil {
		return nil, err
	}
	if response.Audits == nil || response.Audits.Version != opscontract.RPCVersion ||
		len(response.Audits.Entries) > opscontract.MaxAuditEntries {
		return nil, fmt.Errorf("internal/access/node: invalid operations MCP audit response")
	}
	return append([]opscontract.AuditEntry(nil), response.Audits.Entries...), nil
}

// ForwardOpsMCP invokes one configured execution owner without forwarding the raw token.
func (c *Client) ForwardOpsMCP(ctx context.Context, ownerNodeID uint64, request opscontract.ForwardRequest) (opscontract.ForwardResponse, error) {
	if ownerNodeID == 0 || request.Version != opscontract.RPCVersion || len(request.Payload) > opscontract.MaxForwardRequestBytes {
		return opscontract.ForwardResponse{}, fmt.Errorf("internal/access/node: invalid operations MCP forward")
	}
	response, err := c.callOpsMCP(ctx, ownerNodeID, opsMCPRPCRequest{
		Version: opscontract.RPCVersion, Op: opsMCPRPCOpForward, Forward: &request,
	})
	if err != nil {
		return opscontract.ForwardResponse{}, err
	}
	if response.Forward == nil || response.Forward.Version != opscontract.RPCVersion ||
		len(response.Forward.Payload) > opscontract.MaxForwardResponseBytes {
		return opscontract.ForwardResponse{}, fmt.Errorf("internal/access/node: invalid operations MCP owner response")
	}
	return *response.Forward, nil
}

// CaptureOpsMCPProfile invokes one target node's bounded profile runtime.
func (c *Client) CaptureOpsMCPProfile(ctx context.Context, nodeID uint64, request opscontract.ProfileRequest) (opscontract.ProfileResponse, error) {
	if nodeID == 0 || request.Version != opscontract.RPCVersion || request.NodeID != nodeID {
		return opscontract.ProfileResponse{}, fmt.Errorf("internal/access/node: invalid operations MCP profile request")
	}
	response, err := c.callOpsMCP(ctx, nodeID, opsMCPRPCRequest{
		Version: opscontract.RPCVersion, Op: opsMCPRPCOpProfile, Profile: &request,
	})
	if err != nil {
		return opscontract.ProfileResponse{}, err
	}
	if response.Profile == nil || response.Profile.Version != opscontract.RPCVersion ||
		len(response.Profile.Payload) > opscontract.MaxProfileBytes {
		return opscontract.ProfileResponse{}, fmt.Errorf("internal/access/node: invalid operations MCP profile response")
	}
	return *response.Profile, nil
}

func (c *Client) callOpsMCP(ctx context.Context, nodeID uint64, request opsMCPRPCRequest) (opsMCPRPCResponse, error) {
	if c == nil || c.node == nil {
		return opsMCPRPCResponse{}, errors.New("internal/access/node: operations MCP RPC client not configured")
	}
	identity, ok := c.node.(interface{ NodeID() uint64 })
	if !ok || identity.NodeID() == 0 {
		return opsMCPRPCResponse{}, errors.New("internal/access/node: operations MCP RPC caller identity unavailable")
	}
	request.CallerNodeID = identity.NodeID()
	payload, err := json.Marshal(request)
	if err != nil {
		return opsMCPRPCResponse{}, err
	}
	responsePayload, err := c.node.CallRPC(ctx, nodeID, OpsMCPRPCServiceID, payload)
	if err != nil {
		return opsMCPRPCResponse{}, err
	}
	var response opsMCPRPCResponse
	if err := json.Unmarshal(responsePayload, &response); err != nil || response.Version != opscontract.RPCVersion {
		return opsMCPRPCResponse{}, fmt.Errorf("internal/access/node: invalid operations MCP RPC response")
	}
	return response, nil
}

type opsMCPRPCRequest struct {
	Version      uint8                            `json:"version"`
	CallerNodeID uint64                           `json:"caller_node_id"`
	Op           string                           `json:"op"`
	Forward      *opscontract.ForwardRequest      `json:"forward,omitempty"`
	Profile      *opscontract.ProfileRequest      `json:"profile,omitempty"`
	ProfileLease *opscontract.ProfileLeaseRequest `json:"profile_lease,omitempty"`
	Audits       *opscontract.AuditRequest        `json:"audits,omitempty"`
}

type opsMCPRPCResponse struct {
	Version      uint8                             `json:"version"`
	Forward      *opscontract.ForwardResponse      `json:"forward,omitempty"`
	Profile      *opscontract.ProfileResponse      `json:"profile,omitempty"`
	ProfileLease *opscontract.ProfileLeaseResponse `json:"profile_lease,omitempty"`
	Audits       *opscontract.AuditResponse        `json:"audits,omitempty"`
}
