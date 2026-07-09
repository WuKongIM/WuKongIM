package config

type fieldKind string

const (
	kindString     fieldKind = "string"
	kindBool       fieldKind = "bool"
	kindInt        fieldKind = "int"
	kindUint64     fieldKind = "uint64"
	kindUint32     fieldKind = "uint32"
	kindUint16     fieldKind = "uint16"
	kindFloat      fieldKind = "float"
	kindDuration   fieldKind = "duration"
	kindStringList fieldKind = "string_list"
	kindObjectList fieldKind = "object_list"
)

type fieldSpec struct {
	TOMLPath    string
	EnvKey      string
	Kind        fieldKind
	Group       string
	Label       string
	Description string
	Required    bool
	Nullable    bool
	Sensitive   bool
}

var removedConfigKeyReplacements = map[string]string{
	"WK_CLUSTER_GROUP_COUNT":                 "WK_CLUSTER_INITIAL_SLOT_COUNT",
	"WK_CLUSTER_GROUP_REPLICA_N":             "WK_CLUSTER_SLOT_REPLICA_N",
	"WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED": "WK_CHANNEL_MIGRATION_*",
}

var requiredConfigKeys = []string{
	"WK_NODE_ID",
	"WK_NODE_DATA_DIR",
	"WK_CLUSTER_LISTEN_ADDR",
}

var schemaFields = []fieldSpec{
	{TOMLPath: "node.id", EnvKey: "WK_NODE_ID", Kind: kindUint64, Group: "node", Label: "Node ID", Required: true},
	{TOMLPath: "node.data_dir", EnvKey: "WK_NODE_DATA_DIR", Kind: kindString, Group: "node", Label: "Data directory", Required: true},
	{TOMLPath: "cluster.listen_addr", EnvKey: "WK_CLUSTER_LISTEN_ADDR", Kind: kindString, Group: "cluster", Label: "Cluster listen address", Required: true},
	{TOMLPath: "cluster.id", EnvKey: "WK_CLUSTER_ID", Kind: kindString, Group: "cluster", Label: "Cluster ID"},
	{TOMLPath: "cluster.seeds", EnvKey: "WK_CLUSTER_SEEDS", Kind: kindStringList, Group: "cluster", Label: "Seed addresses"},
	{TOMLPath: "cluster.advertise_addr", EnvKey: "WK_CLUSTER_ADVERTISE_ADDR", Kind: kindString, Group: "cluster", Label: "Cluster advertise address"},
	{TOMLPath: "cluster.join_token", EnvKey: "WK_CLUSTER_JOIN_TOKEN", Kind: kindString, Group: "cluster", Label: "Join token", Sensitive: true},
	{TOMLPath: "cluster.nodes", EnvKey: "WK_CLUSTER_NODES", Kind: kindObjectList, Group: "cluster", Label: "Static cluster nodes"},
	{TOMLPath: "cluster.initial_slot_count", EnvKey: "WK_CLUSTER_INITIAL_SLOT_COUNT", Kind: kindUint32, Group: "cluster", Label: "Initial slot count"},
	{TOMLPath: "cluster.hash_slot_count", EnvKey: "WK_CLUSTER_HASH_SLOT_COUNT", Kind: kindUint16, Group: "cluster", Label: "Hash slot count"},
	{TOMLPath: "cluster.slot_replica_n", EnvKey: "WK_CLUSTER_SLOT_REPLICA_N", Kind: kindUint16, Group: "cluster", Label: "Slot replica count"},
	{TOMLPath: "cluster.channel_replica_n", EnvKey: "WK_CLUSTER_CHANNEL_REPLICA_N", Kind: kindUint16, Group: "cluster", Label: "Channel replica count"},
}

func schemaByTOMLPath() map[string]fieldSpec {
	out := make(map[string]fieldSpec, len(schemaFields))
	for _, field := range schemaFields {
		out[field.TOMLPath] = field
	}
	return out
}

func schemaByEnvKey() map[string]fieldSpec {
	out := make(map[string]fieldSpec, len(schemaFields))
	for _, field := range schemaFields {
		out[field.EnvKey] = field
	}
	return out
}
