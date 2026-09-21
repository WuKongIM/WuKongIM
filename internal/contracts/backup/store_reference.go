package backup

// StoreReference identifies a repository and its exact credential revision
// without carrying encrypted credentials or a node-local filesystem path.
type StoreReference struct {
	Kind               StoreKind `json:"kind"`
	Endpoint           string    `json:"endpoint,omitempty"`
	Region             string    `json:"region,omitempty"`
	Bucket             string    `json:"bucket,omitempty"`
	Prefix             string    `json:"prefix,omitempty"`
	PathStyle          bool      `json:"path_style,omitempty"`
	CredentialRevision uint64    `json:"credential_revision,omitempty"`
}

// Reference returns the complete non-secret identity used by node RPC. It does
// not retain any alias to the durable credential bytes.
func (s StoreConfig) Reference() StoreReference {
	return StoreReference{
		Kind: s.Kind, Endpoint: s.Endpoint, Region: s.Region, Bucket: s.Bucket,
		Prefix: s.Prefix, PathStyle: s.PathStyle, CredentialRevision: s.CredentialRevision,
	}
}
