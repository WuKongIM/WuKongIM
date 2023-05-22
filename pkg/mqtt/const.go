package mqtt

type ReasonCode byte

const (
	Success                           ReasonCode = 0x00 // CONNACK, PUBACK, PUBREC, PUBREL, PUBCOMP, UNSUBACK, AUTH
	NoMatchingSubscribers             ReasonCode = 0x10 // PUBACK, PUBREC
	UnspecifiedError                  ReasonCode = 0x80 // CONNACK, PUBACK, PUBREC, SUBACK, UNSUBACK, DISCONNECT
	MalformedPacket                   ReasonCode = 0x81 // CONNACK, DISCONNECT
	ProtocolError                     ReasonCode = 0x82 // CONNACK, DISCONNECT
	ImplSpecificError                 ReasonCode = 0x83 // CONNACK, PUBACK, PUBREC, SUBACK, UNSUBACK, DISCONNECT
	NotAuthorized                     ReasonCode = 0x87 // CONNACK, PUBACK, PUBREC, SUBACK, UNSUBACK, DISCONNECT
	ServerBusy                        ReasonCode = 0x89 // CONNACK, DISCONNECT
	BadAuthMethod                     ReasonCode = 0x8C // CONNACK, DISCONNECT
	TopicFilterInvalid                ReasonCode = 0x8F // SUBACK, UNSUBACK, DISCONNECT
	TopicNameInvalid                  ReasonCode = 0x90 // CONNACK, PUBACK, PUBREC, DISCONNECT
	PacketIdentifierInUse             ReasonCode = 0x91 // PUBACK, SUBACK, UNSUBACK
	PacketIdentifierNotFound          ReasonCode = 0x92 // PUBREL, PUBCOMP
	PacketTooLarge                    ReasonCode = 0x95 // CONNACK, PUBACK, PUBREC, DISCONNECT
	QuotaExceeded                     ReasonCode = 0x97 // PUBACK, PUBREC, SUBACK, DISCONNECT
	PayloadFormatInvalid              ReasonCode = 0x99 // CONNACK, DISCONNECT
	RetainNotSupported                ReasonCode = 0x9A // CONNACK, DISCONNECT
	QoSNotSupported                   ReasonCode = 0x9B // CONNACK, DISCONNECT
	UseAnotherServer                  ReasonCode = 0x9C // CONNACK, DISCONNECT
	ServerMoved                       ReasonCode = 0x9D // CONNACK, DISCONNECT
	SharedSubscriptionsNotSupported   ReasonCode = 0x9E // SUBACK, DISCONNECT
	ConnectionRateExceeded            ReasonCode = 0x9F // CONNACK, DISCONNECT
	SubscriptionIdsNotSupported       ReasonCode = 0xA1 // SUBACK, DISCONNECT
	WildcardSubscriptionsNotSupported ReasonCode = 0xA2 // SUBACK, DISCONNECT
)
