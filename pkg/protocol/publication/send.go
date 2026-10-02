package publication

// ValidateSend checks optional publication metadata before routing or hooks.
// Native sends omit it; Will templates need a bound durable intent before use.
// This validates content only, never permission, Session ownership or execution.
func ValidateSend(value []byte) error {
	if len(value) == 0 {
		return nil
	}
	m, err := Decode(value)
	if err != nil {
		return err
	}
	if m.Source == SourceWill && m.ServerWillKey == "" {
		return ErrInvalid
	}
	// Will receives its real expiry basis at append. One is a valid placeholder
	// for this bounded validation; ordinary MQTT retains its ingress clock.
	_, _, err = m.ExpiryDeadlineMS(1)
	return err
}
