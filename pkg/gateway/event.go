package gateway

import gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"

type Handler = gatewaytypes.Handler
type SendBatchItem = gatewaytypes.SendBatchItem
type SendBatchHandler = gatewaytypes.SendBatchHandler
type SessionActivator = gatewaytypes.SessionActivator
type SessionActivationRollbacker = gatewaytypes.SessionActivationRollbacker
type Context = gatewaytypes.Context
type Observer = gatewaytypes.Observer
type AsyncSendObserver = gatewaytypes.AsyncSendObserver
type ConnectionEvent = gatewaytypes.ConnectionEvent
type AuthEvent = gatewaytypes.AuthEvent
type FrameEvent = gatewaytypes.FrameEvent
type FrameHandleEvent = gatewaytypes.FrameHandleEvent
type AsyncSendQueueEvent = gatewaytypes.AsyncSendQueueEvent
type AsyncSendBatchEvent = gatewaytypes.AsyncSendBatchEvent
type AsyncSendDispatchWaitEvent = gatewaytypes.AsyncSendDispatchWaitEvent
