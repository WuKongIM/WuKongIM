package mqtt

import (
	"context"
	"errors"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

func (h *Handler) mapConnect(p *wire.Connect, g gt.Context) (sessioncase.ConnectCommand, uint32, error) {
	command := sessioncase.ConnectCommand{ReceiveMaximum: math.MaxUint16, MaxPacketBytes: math.MaxUint32, CleanStart: p.CleanStart}
	var mappingErr error
	seen := make(map[wire.PropertyID]bool)
	for _, v := range p.Properties {
		switch v.ID {
		case wire.SessionExpiryInterval, wire.ReceiveMaximum, wire.MaximumPacketSize:
			if seen[v.ID] {
				mappingErr = publicationError(wire.ProtocolError)
			}
			seen[v.ID] = true
		}
		switch v.ID {
		case wire.SessionExpiryInterval:
			command.SessionExpirySec = v.Number
		case wire.ReceiveMaximum:
			if v.Number == 0 || v.Number > math.MaxUint16 {
				mappingErr = publicationError(wire.ProtocolError)
			} else {
				command.ReceiveMaximum = uint16(v.Number)
			}
		case wire.MaximumPacketSize:
			if v.Number == 0 {
				mappingErr = publicationError(wire.ProtocolError)
			} else if v.Number < command.MaxPacketBytes {
				command.MaxPacketBytes = v.Number
			}
		}
	}
	if mappingErr != nil {
		return command, command.MaxPacketBytes, mappingErr
	}
	id, err := Credentials(p)
	if err != nil {
		return command, command.MaxPacketBytes, err
	}
	command.Key = contract.Key{Namespace: h.options.Namespace, ClientID: id.ClientID}
	command.UID, command.Token, command.DeviceFlag = id.UID, id.Token, id.DeviceFlag
	// Capture only the physical-close capability, not credentials or packet state.
	closer := g.TransportCloser
	command.CloseTransport = func(ctx context.Context) error {
		return closer.CloseTransportAndWait(ctx, gt.CloseReasonPolicyViolation)
	}
	if p.Will != nil {
		will, err := MapWill(p.Will, h.options.Namespace, id.ClientID)
		if err != nil {
			return command, command.MaxPacketBytes, err
		}
		command.Will = &sessioncase.Will{WillTarget: sessioncase.WillTarget{Topic: p.Will.Topic, TargetID: will.Target.ChannelID, TargetType: will.Target.ChannelType}, QoS: p.Will.QoS, DelaySeconds: will.DelaySec, ClientMsgNo: will.ClientMsgNo, Payload: will.Payload, PublicationMetadata: will.Metadata}
	}
	return command, command.MaxPacketBytes, nil
}

func connectReason(err error) byte {
	var packetError *wire.Error
	if errors.As(err, &packetError) {
		switch packetError.Reason {
		case 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x8c, 0x90, 0x95, 0x99, 0x9a, 0x9b:
			return packetError.Reason
		}
		return 0x82
	}
	switch {
	case errors.Is(err, user.ErrInvalidToken), errors.Is(err, sessioncase.ErrBinding), errors.Is(err, sessioncase.ErrWillDenied):
		return 0x87
	case errors.Is(err, runtime.ErrOwnerLimit), errors.Is(err, runtime.ErrConnectionsLimit):
		return 0x97
	case errors.Is(err, sessioncase.ErrConflict):
		return 0x89
	default:
		return 0x88
	}
}

func (h *Handler) connack(c sessioncase.Connection) *wire.Connack {
	return &wire.Connack{SessionPresent: c.SessionPresent, Properties: []wire.Property{
		{ID: wire.MaximumQoS, Number: 1}, {ID: wire.RetainAvailable, Number: 0},
		{ID: wire.WildcardSubscriptionAvailable, Number: 0}, {ID: wire.SharedSubscriptionAvailable, Number: 0},
		{ID: wire.SubscriptionIdentifierAvailable, Number: 0}, {ID: wire.TopicAliasMaximum, Number: 0},
		{ID: wire.MaximumPacketSize, Number: h.options.MaxPacketBytes}, {ID: wire.SessionExpiryInterval, Number: c.SessionExpirySec},
	}}
}

// Client DISCONNECT uses only client/bidirectional reasons from MQTT 5 table 3-10.
// Invalid direction/expiry is not a normal disconnect and must not cancel Will.
func disconnectExpiry(p *wire.Disconnect, original uint32) (*uint32, bool) {
	if p == nil {
		return nil, false
	}
	switch p.Reason {
	case 0, 4, 0x80, 0x81, 0x82, 0x83, 0x90, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99:
	default:
		return nil, false
	}
	var expiry *uint32
	for _, v := range p.Properties {
		switch v.ID {
		case wire.SessionExpiryInterval:
			if expiry != nil || (original == 0 && v.Number != 0) {
				return nil, false
			}
			value := v.Number
			expiry = &value
		case wire.ReasonString, wire.UserProperty:
		default:
			return nil, false
		}
	}
	return expiry, true
}
