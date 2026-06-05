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
type AsyncAuthObserver = gatewaytypes.AsyncAuthObserver
type AsyncSendAdmissionObserver = gatewaytypes.AsyncSendAdmissionObserver
type TransportPressureObserver = gatewaytypes.TransportPressureObserver
type ConnectionEvent = gatewaytypes.ConnectionEvent
type AuthEvent = gatewaytypes.AuthEvent
type AuthFailureClassifier = gatewaytypes.AuthFailureClassifier
type FrameEvent = gatewaytypes.FrameEvent
type FrameHandleEvent = gatewaytypes.FrameHandleEvent
type AsyncSendQueueEvent = gatewaytypes.AsyncSendQueueEvent
type AsyncAuthQueueEvent = gatewaytypes.AsyncAuthQueueEvent
type AsyncAuthAdmissionEvent = gatewaytypes.AsyncAuthAdmissionEvent
type AsyncAuthWaitEvent = gatewaytypes.AsyncAuthWaitEvent
type AsyncSendAdmissionEvent = gatewaytypes.AsyncSendAdmissionEvent
type TransportPressureEvent = gatewaytypes.TransportPressureEvent
type AsyncSendBatchEvent = gatewaytypes.AsyncSendBatchEvent
type AsyncSendDispatchWaitEvent = gatewaytypes.AsyncSendDispatchWaitEvent
