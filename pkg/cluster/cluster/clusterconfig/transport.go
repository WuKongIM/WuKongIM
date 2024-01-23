package clusterconfig

type ITransport interface {
	Send(m Message) error
	OnMessage(f func(m Message))
}
